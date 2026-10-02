package aigate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"crypto-trader-go/internal/alerts"
)

// Credential kinds. An OAuth token is one the owner generated with their own `claude setup-token`;
// this package never runs a claude.ai login flow.
const (
	KindAPIKey     = "api_key"     // passed to the worker as ANTHROPIC_API_KEY
	KindOAuthToken = "oauth_token" // passed to the worker as CLAUDE_CODE_OAUTH_TOKEN
)

const credSetting = "ai.credential"

// credential is the secret. It lives only in memory, in the settings table (encrypted when
// CTG_SECRET_KEY is set) and in the config message to the worker. Never log it.
type credential struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (c credential) configured() bool { return c.Kind != "" && c.Value != "" }

// stored is the settings-table form.
type stored struct {
	V         int    `json:"v"`
	Kind      string `json:"kind"`
	Enc       string `json:"enc"`             // "aes-gcm" | "plain"
	Data      string `json:"data,omitempty"`  // base64(nonce || ciphertext) when encrypted
	Value     string `json:"value,omitempty"` // when plain
	UpdatedAt int64  `json:"updatedAt"`
}

var credRe = regexp.MustCompile(`^[A-Za-z0-9_\-.]{20,512}$`)

func validateCredential(kind, value string) (credential, error) {
	value = strings.TrimSpace(value)
	switch kind {
	case KindAPIKey, KindOAuthToken:
	default:
		return credential{}, fmt.Errorf("凭证类型只能是 %q 或 %q", KindAPIKey, KindOAuthToken)
	}
	if !credRe.MatchString(value) {
		return credential{}, errors.New("凭证需为 20–512 位字母、数字或 - _ .")
	}
	// Catch the obvious mix-up (the prefixes are public, not secret).
	if kind == KindAPIKey && strings.HasPrefix(value, "sk-ant-oat") {
		return credential{}, errors.New("这看起来是 `claude setup-token` 生成的订阅 token，请把类型选为“订阅 token”")
	}
	if kind == KindOAuthToken && strings.HasPrefix(value, "sk-ant-api") {
		return credential{}, errors.New("这看起来是 API key，请把类型选为“API key”")
	}
	return credential{Kind: kind, Value: value}, nil
}

// sealer encrypts the credential at rest with AES-256-GCM keyed by sha256(CTG_SECRET_KEY).
type sealer struct{ aead cipher.AEAD }

var credAAD = []byte("ctg-ai-credential-v1")

func newSealer(secret string) *sealer {
	if secret == "" {
		return nil
	}
	k := sha256.Sum256([]byte(secret))
	blk, err := aes.NewCipher(k[:])
	if err != nil {
		return nil
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil
	}
	return &sealer{aead: aead}
}

func (s *sealer) seal(plain string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, []byte(plain), credAAD)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *sealer) open(data string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("corrupt ciphertext")
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], credAAD)
	if err != nil {
		return "", errors.New("cannot decrypt")
	}
	return string(plain), nil
}

func encodeCredential(c credential, s *sealer, now int64) (string, error) {
	st := stored{V: 1, Kind: c.Kind, UpdatedAt: now}
	if s != nil {
		d, err := s.seal(c.Value)
		if err != nil {
			return "", err
		}
		st.Enc, st.Data = "aes-gcm", d
	} else {
		st.Enc, st.Value = "plain", c.Value
	}
	b, err := json.Marshal(st)
	return string(b), err
}

// decodeCredential returns the credential, its storage mode and a problem description (never
// containing the secret).
func decodeCredential(raw string, s *sealer) (credential, stored, string) {
	var st stored
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return credential{}, st, "stored credential is unreadable"
	}
	switch st.Enc {
	case "plain":
		return credential{Kind: st.Kind, Value: st.Value}, st, ""
	case "aes-gcm":
		if s == nil {
			return credential{}, st, "stored credential is encrypted but CTG_SECRET_KEY is not set"
		}
		v, err := s.open(st.Data)
		if err != nil {
			return credential{}, st, "stored credential cannot be decrypted (CTG_SECRET_KEY changed?)"
		}
		return credential{Kind: st.Kind, Value: v}, st, ""
	}
	return credential{}, st, "stored credential has an unknown format"
}

// mask shows only the last 4 characters.
func mask(s string) string { return alerts.Mask(s) }

// scrub removes the secret from a text (defence in depth: worker errors may echo env values).
func scrub(text string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) >= 8 {
			text = strings.ReplaceAll(text, s, "***")
		}
	}
	return text
}
