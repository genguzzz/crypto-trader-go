// Package devapi exposes read-only market data to external agents: API keys and short-lived
// tokens (hashed at rest), per-key token-bucket rate limits with a global ceiling, a REST API
// (/api/open/v1), its OpenAPI document and an MCP server (/mcp). Settings, credentials and
// detailed system internals are never exposed.
package devapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/store"
)

const (
	KindKey   = "key"
	KindToken = "token"

	DefaultRatePerMin   = 60
	MaxRatePerMin       = 6000
	MaxBurst            = 1000
	DefaultGlobalPerMin = 1200
	maxTokenTTLHours    = 30 * 24
	maxKeyTTLHours      = 365 * 24
	logSize             = 200
	settingKey          = "devapi"
)

// Config is the server-side switch and global ceiling (Settings page → 开发者).
type Config struct {
	Enabled      bool `json:"enabled"`
	GlobalPerMin int  `json:"globalPerMin"` // all keys together; protects the recorder
}

type Store interface {
	Setting(key string) (string, bool)
	PutSetting(key, value string) error
	APIKeys() ([]store.APIKey, error)
	AddAPIKey(store.APIKey) (int64, error)
	UpdateAPIKey(store.APIKey) error
	PutAPIKeyUsage(id, lastUsed, requests, limited int64) error
	DeleteAPIKey(id int64) error
}

// bucket is a token bucket: capacity burst, refilled at perMin/60 tokens per second.
type bucket struct {
	tokens float64
	at     time.Time
}

func (b *bucket) take(now time.Time, perMin, burst int, cost float64) (ok bool, remaining float64, retry time.Duration) {
	rate := float64(perMin) / 60
	if b.at.IsZero() {
		b.tokens = float64(burst)
	} else {
		b.tokens = math.Min(float64(burst), b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
	if b.tokens >= cost {
		b.tokens -= cost
		return true, b.tokens, 0
	}
	return false, b.tokens, time.Duration((cost - b.tokens) / rate * float64(time.Second))
}

func (b *bucket) peek(now time.Time, perMin, burst int) float64 {
	if b.at.IsZero() {
		return float64(burst)
	}
	return math.Min(float64(burst), b.tokens+now.Sub(b.at).Seconds()*float64(perMin)/60)
}

type keyState struct {
	store.APIKey
	b     bucket
	dirty bool
}

// LogEntry is one external request (in-memory ring, newest first in Log()).
type LogEntry struct {
	Ts       int64  `json:"ts"`
	KeyID    int64  `json:"keyId"`
	KeyName  string `json:"keyName"`
	Prefix   string `json:"prefix"`
	Via      string `json:"via"` // rest | mcp
	Endpoint string `json:"endpoint"`
	Status   int    `json:"status"`
	Ms       int64  `json:"ms"`
	Cost     int    `json:"cost"`
	Error    string `json:"error,omitempty"`
}

type Service struct {
	st Store

	mu     sync.Mutex
	cfg    Config
	keys   map[int64]*keyState
	byHash map[string]*keyState
	global bucket
	logs   []LogEntry // ring
	logAt  int
	now    func() time.Time
	eps    []*Endpoint // set by Mount
}

func New(st Store) (*Service, error) {
	s := &Service{st: st, cfg: Config{Enabled: true, GlobalPerMin: DefaultGlobalPerMin}, keys: map[int64]*keyState{}, byHash: map[string]*keyState{}, now: time.Now}
	if raw, ok := st.Setting(settingKey); ok {
		var c Config
		if err := json.Unmarshal([]byte(raw), &c); err == nil && c.validate() == nil {
			s.cfg = c
		}
	}
	ks, err := st.APIKeys()
	if err != nil {
		return nil, err
	}
	for _, k := range ks {
		ks := &keyState{APIKey: k}
		s.keys[k.ID], s.byHash[k.Hash] = ks, ks
	}
	return s, nil
}

// Run flushes usage counters every 30 s (never per request) until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush()
			return
		case <-t.C:
			s.flush()
		}
	}
}

func (s *Service) flush() {
	s.mu.Lock()
	var todo []store.APIKey
	for _, k := range s.keys {
		if k.dirty {
			todo = append(todo, k.APIKey)
			k.dirty = false
		}
	}
	s.mu.Unlock()
	for _, k := range todo {
		if err := s.st.PutAPIKeyUsage(k.ID, k.LastUsedAt, k.Requests, k.Limited); err != nil {
			log.Printf("devapi: usage flush: %v", err)
		}
	}
}

func (c *Config) validate() error {
	if c.GlobalPerMin < 10 || c.GlobalPerMin > 60_000 {
		return fmt.Errorf("全局上限需在 10 ~ 60000 次/分钟之间")
	}
	return nil
}

func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Service) SetConfig(c Config) (Config, error) {
	if err := c.validate(); err != nil {
		return s.Config(), err
	}
	b, _ := json.Marshal(c)
	if err := s.st.PutSetting(settingKey, string(b)); err != nil {
		return s.Config(), err
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return c, nil
}

// KeyView is a key as listed to the owner: never the secret or its hash.
type KeyView struct {
	store.APIKey
	Status    string  `json:"status"` // active | expired | revoked
	Remaining float64 `json:"remaining"`
}

func (s *Service) Keys() []KeyView {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]KeyView, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, KeyView{APIKey: k.APIKey, Status: k.status(now), Remaining: math.Floor(k.b.peek(now, k.RatePerMin, k.Burst))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func (k *keyState) status(now time.Time) string {
	switch {
	case k.RevokedAt > 0:
		return "revoked"
	case k.ExpiresAt > 0 && now.UnixMilli() >= k.ExpiresAt:
		return "expired"
	}
	return "active"
}

// CreateInput is the owner's request for a new key or token.
type CreateInput struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	RatePerMin int    `json:"ratePerMin"`
	Burst      int    `json:"burst"`
	TTLHours   int    `json:"ttlHours"` // 0 = never (keys only)
}

// Created carries the secret, returned exactly once.
type Created struct {
	KeyView
	Secret string `json:"secret"`
}

func (in *CreateInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		return fmt.Errorf("名称需为 1~64 个字符")
	}
	if in.Kind == "" {
		in.Kind = KindKey
	}
	if in.Kind != KindKey && in.Kind != KindToken {
		return fmt.Errorf("类型只能是 key 或 token")
	}
	if in.RatePerMin == 0 {
		in.RatePerMin = DefaultRatePerMin
	}
	if in.RatePerMin < 1 || in.RatePerMin > MaxRatePerMin {
		return fmt.Errorf("频率需在 1 ~ %d 次/分钟之间", MaxRatePerMin)
	}
	if in.Burst == 0 {
		in.Burst = DefaultBurst(in.RatePerMin)
	}
	if in.Burst < 1 || in.Burst > MaxBurst {
		return fmt.Errorf("突发容量需在 1 ~ %d 之间", MaxBurst)
	}
	switch {
	case in.Kind == KindToken && (in.TTLHours < 1 || in.TTLHours > maxTokenTTLHours):
		return fmt.Errorf("Token 有效期需在 1 小时 ~ 30 天之间")
	case in.Kind == KindKey && (in.TTLHours < 0 || in.TTLHours > maxKeyTTLHours):
		return fmt.Errorf("Key 有效期需在 0（永不过期）~ 365 天之间")
	}
	return nil
}

// DefaultBurst: a third of a minute's allowance, at least 5, never above the per-minute rate.
func DefaultBurst(perMin int) int {
	return max(1, min(perMin, max(5, perMin/3), MaxBurst))
}

func (s *Service) Create(in CreateInput) (Created, error) {
	if err := in.normalize(); err != nil {
		return Created{}, err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Created{}, err
	}
	tag := "sk"
	if in.Kind == KindToken {
		tag = "at"
	}
	secret := "ctg_" + tag + "_" + base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	k := store.APIKey{Kind: in.Kind, Name: in.Name, Prefix: secret[:12], Hash: hashSecret(secret), RatePerMin: in.RatePerMin,
		Burst: in.Burst, CreatedAt: now.UnixMilli()}
	if in.TTLHours > 0 {
		k.ExpiresAt = now.Add(time.Duration(in.TTLHours) * time.Hour).UnixMilli()
	}
	id, err := s.st.AddAPIKey(k)
	if err != nil {
		return Created{}, err
	}
	k.ID = id
	ks := &keyState{APIKey: k}
	s.mu.Lock()
	s.keys[id], s.byHash[k.Hash] = ks, ks
	s.mu.Unlock()
	return Created{KeyView: KeyView{APIKey: k, Status: "active", Remaining: float64(k.Burst)}, Secret: secret}, nil
}

// UpdateInput changes name / limits, or revokes (revoked cannot be undone).
type UpdateInput struct {
	Name       *string `json:"name"`
	RatePerMin *int    `json:"ratePerMin"`
	Burst      *int    `json:"burst"`
	Revoke     bool    `json:"revoke"`
}

var ErrNotFound = errors.New("key not found")

func (s *Service) Update(id int64, in UpdateInput) (KeyView, error) {
	s.mu.Lock()
	k, ok := s.keys[id]
	if !ok {
		s.mu.Unlock()
		return KeyView{}, ErrNotFound
	}
	next := k.APIKey
	s.mu.Unlock()
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.RatePerMin != nil {
		next.RatePerMin = *in.RatePerMin
	}
	if in.Burst != nil {
		next.Burst = *in.Burst
	}
	chk := CreateInput{Kind: next.Kind, Name: next.Name, RatePerMin: next.RatePerMin, Burst: next.Burst, TTLHours: 1}
	if err := chk.normalize(); err != nil {
		return KeyView{}, err
	}
	next.Name = chk.Name
	if in.Revoke && next.RevokedAt == 0 {
		next.RevokedAt = s.now().UnixMilli()
	}
	if err := s.st.UpdateAPIKey(next); err != nil {
		return KeyView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k.Name, k.RatePerMin, k.Burst, k.RevokedAt = next.Name, next.RatePerMin, next.Burst, next.RevokedAt
	now := s.now()
	return KeyView{APIKey: k.APIKey, Status: k.status(now), Remaining: math.Floor(k.b.peek(now, k.RatePerMin, k.Burst))}, nil
}

func (s *Service) Delete(id int64) error {
	s.mu.Lock()
	k, ok := s.keys[id]
	s.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if err := s.st.DeleteAPIKey(id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.keys, id)
	delete(s.byHash, k.Hash)
	s.mu.Unlock()
	return nil
}

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// secretFrom reads "Authorization: Bearer <secret>" or "X-API-Key: <secret>".
func secretFrom(r *http.Request) string {
	if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
		return strings.TrimSpace(a[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

// Caller is an authenticated key for the duration of one request.
type Caller struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	RatePerMin int    `json:"ratePerMin"`
	Burst      int    `json:"burst"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// Quota describes the bucket after a request, for X-RateLimit-* headers and 429 bodies.
type Quota struct {
	Limit     int           // per minute
	Burst     int           // bucket capacity
	Remaining int           // whole requests left right now
	Reset     time.Duration // until the bucket is full again
	Retry     time.Duration // > 0 only when rejected
	Scope     string        // key | global (which bucket rejected)
}

// Denial is an authentication / limit failure mapped to an HTTP status.
type Denial struct {
	Status int
	Msg    string
	Quota  *Quota
}

func (d *Denial) Error() string { return d.Msg }

// Authorize authenticates the request and charges `cost` against the key and global buckets.
// InternalKey mints an in-memory key for an in-process client (the AI agent): never stored, gone on
// restart, and only reachable by whoever receives the secret. It still passes the global limit.
func (s *Service) InternalKey(name string, ratePerMin, burst int) string {
	raw := make([]byte, 24)
	rand.Read(raw)
	secret := "ctg_in_" + base64.RawURLEncoding.EncodeToString(raw)
	k := &keyState{APIKey: store.APIKey{ID: -1, Kind: KindKey, Name: name, Prefix: secret[:12], Hash: hashSecret(secret),
		RatePerMin: ratePerMin, Burst: burst, CreatedAt: s.now().UnixMilli()}}
	s.mu.Lock()
	s.byHash[k.Hash] = k // not in s.keys: not listed, not persisted
	s.mu.Unlock()
	return secret
}

func (s *Service) Authorize(r *http.Request, cost int) (*Caller, *Quota, *Denial) {
	secret := secretFrom(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		return nil, nil, &Denial{Status: http.StatusServiceUnavailable, Msg: "external API is disabled on this server (Settings → Developer)"}
	}
	if secret == "" {
		return nil, nil, &Denial{Status: http.StatusUnauthorized, Msg: "missing API key: send Authorization: Bearer <key> or X-API-Key"}
	}
	k, ok := s.byHash[hashSecret(secret)]
	now := s.now()
	if !ok {
		return nil, nil, &Denial{Status: http.StatusUnauthorized, Msg: "invalid API key"}
	}
	if st := k.status(now); st != "active" {
		return nil, nil, &Denial{Status: http.StatusUnauthorized, Msg: "API key " + st}
	}
	k.LastUsedAt, k.dirty = now.UnixMilli(), true
	caller := &Caller{ID: k.ID, Kind: k.Kind, Name: k.Name, Prefix: k.Prefix, RatePerMin: k.RatePerMin, Burst: k.Burst, ExpiresAt: k.ExpiresAt}
	if cost <= 0 { // free: MCP protocol messages, usage lookups
		q := quotaOf(k, now)
		return caller, &q, nil
	}
	k.Requests++
	c := float64(cost)
	gBurst := max(10, s.cfg.GlobalPerMin/6)
	if ok, _, retry := s.global.take(now, s.cfg.GlobalPerMin, gBurst, c); !ok {
		k.Limited++
		q := quotaOf(k, now)
		q.Retry, q.Scope = retry, "global"
		return nil, &q, &Denial{Status: http.StatusTooManyRequests, Msg: "server-wide rate limit reached; retry later", Quota: &q}
	}
	if ok, _, retry := k.b.take(now, k.RatePerMin, k.Burst, c); !ok {
		s.global.tokens += c // refund: this request is not served
		k.Limited++
		q := quotaOf(k, now)
		q.Retry, q.Scope = retry, "key"
		return nil, &q, &Denial{Status: http.StatusTooManyRequests, Msg: fmt.Sprintf("rate limit: %d requests/min, burst %d (this call costs %d)", k.RatePerMin, k.Burst, int(c)), Quota: &q}
	}
	q := quotaOf(k, now)
	return caller, &q, nil
}

func quotaOf(k *keyState, now time.Time) Quota {
	left := k.b.peek(now, k.RatePerMin, k.Burst)
	reset := time.Duration((float64(k.Burst) - left) / (float64(k.RatePerMin) / 60) * float64(time.Second))
	return Quota{Limit: k.RatePerMin, Burst: k.Burst, Remaining: int(math.Floor(left)), Reset: reset}
}

// Usage is the caller's current allowance (the /me endpoint and the get_usage tool).
type Usage struct {
	Key          Caller  `json:"key"`
	Remaining    float64 `json:"remaining"`
	RefillPerSec float64 `json:"refillPerSec"`
	GlobalPerMin int     `json:"globalPerMin"`
	Requests     int64   `json:"requests"`
	Limited      int64   `json:"limited"`
	Costs        string  `json:"costs"`
}

func (s *Service) Usage(c *Caller) Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := Usage{Key: *c, RefillPerSec: float64(c.RatePerMin) / 60, GlobalPerMin: s.cfg.GlobalPerMin,
		Costs: "each call costs 1 token unless the endpoint lists a higher cost; tokens refill continuously up to burst"}
	if k, ok := s.keys[c.ID]; ok {
		u.Remaining = math.Floor(k.b.peek(s.now(), k.RatePerMin, k.Burst))
		u.Requests, u.Limited = k.Requests, k.Limited
	}
	return u
}

func (s *Service) record(e LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logs) < logSize {
		s.logs = append(s.logs, e)
		return
	}
	s.logs[s.logAt] = e
	s.logAt = (s.logAt + 1) % logSize
}

// Log returns recent external requests, newest first.
func (s *Service) Log() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, 0, len(s.logs))
	for i := range s.logs {
		j := (s.logAt - 1 - i + 2*len(s.logs)) % len(s.logs)
		out = append(out, s.logs[j])
	}
	return out
}
