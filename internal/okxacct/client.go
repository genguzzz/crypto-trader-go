// Package okxacct reads the owner's own OKX account (open swap positions, pending TP/SL, equity) for
// display in the web, the app and the chat agent. Strictly read-only: the only request it can make is
// a signed GET to an allow-listed path, and there is no order, cancel or transfer code (the orders the
// owner approved through proposals live in internal/okxorder, which signs through Config). Every request
// goes through OKX_PRIVATE_PROXY (the API key is bound to that exit IP); without the proxy or any key
// variable the feature is disabled, never served over a direct connection.
package okxacct

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://www.okx.com"
	// OKX's Cloudflare rejects the default Go / Python user agents (403, error 1010).
	UserAgent = "Mozilla/5.0 (compatible; okx-helper/1)"
)

// readPaths are the only endpoints the client will call (all GET, all read-only).
var readPaths = map[string]bool{
	"/api/v5/account/positions":         true,
	"/api/v5/account/balance":           true,
	"/api/v5/trade/orders-algo-pending": true,
}

// readPrefixes is what the agent's generic okx_api tool may read: any GET under these is a read (every
// OKX write — order, cancel, transfer, withdraw — is a POST, and this client only sends GET).
var readPrefixes = []string{"/api/v5/account/", "/api/v5/asset/", "/api/v5/trade/", "/api/v5/finance/",
	"/api/v5/tradingBot/", "/api/v5/copytrading/", "/api/v5/users/"}

// writeName matches the last path segment of OKX's write endpoints (order, cancel, amend, transfer, withdraw, set …).
var writeName = regexp.MustCompile(`^(order|batch-orders|order-algo|close-position|transfer|withdrawal|purchase|redeem|convert-dust|` +
	`(cancel|amend|mass-cancel|set|move|apply|activate|place|adjust|borrow|repay)(-.*)?)$`)

// ReadAllowed reports whether path may be read through the generic tool.
func ReadAllowed(path string) bool {
	if readPaths[path] {
		return true
	}
	if strings.ContainsAny(path, "?#%\\ ") || strings.Contains(path, "..") {
		return false
	}
	if writeName.MatchString(path[strings.LastIndex(path, "/")+1:]) {
		return false // belt and braces: never even GET a path named like a write endpoint
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(path, p) && len(path) > len(p) {
			return true
		}
	}
	return false
}

// Config is the OKX API key and the proxy its IP is bound to. Never logged.
type Config struct {
	Key, Secret, Passphrase string
	Proxy                   *url.URL
}

// ConfigFromEnv reads OKX_API_KEY, OKX_API_SECRET, OKX_API_PASSPHRASE and OKX_PRIVATE_PROXY.
// A malformed proxy is an error; missing variables just leave the feature disabled.
func ConfigFromEnv() (Config, error) {
	c := Config{Key: os.Getenv("OKX_API_KEY"), Secret: os.Getenv("OKX_API_SECRET"), Passphrase: os.Getenv("OKX_API_PASSPHRASE")}
	if p := os.Getenv("OKX_PRIVATE_PROXY"); p != "" {
		u, err := url.Parse(p)
		if err != nil || u.Host == "" || !map[string]bool{"http": true, "https": true, "socks5": true, "socks5h": true}[u.Scheme] {
			return Config{}, errors.New("OKX_PRIVATE_PROXY must be a URL like http://127.0.0.1:7890 or socks5://127.0.0.1:1080")
		}
		c.Proxy = u
	}
	return c, nil
}

// Enabled reports whether every key variable and the proxy are set.
func (c Config) Enabled() bool {
	return c.Key != "" && c.Secret != "" && c.Passphrase != "" && c.Proxy != nil
}

// Client signs GET requests to the private REST API. Construct it only from an enabled Config.
type Client struct {
	BaseURL string
	cfg     Config
	http    *http.Client
	now     func() time.Time
}

func NewClient(cfg Config) *Client {
	return &Client{BaseURL: DefaultBaseURL, cfg: cfg, http: cfg.HTTPClient(), now: time.Now}
}

// HTTPClient returns a client bound to the private proxy (not the environment's): with no proxy every
// request fails instead of going direct. Shared with internal/okxorder.
func (c Config) HTTPClient() *http.Client {
	proxy := c.Proxy
	tr := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			if proxy == nil {
				return nil, errors.New("no private proxy configured")
			}
			return proxy, nil
		},
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

// Sign sets OKX's authentication headers on req; requestPath includes the query and body is the exact
// request body ("" for GET). The key stays in this Config: internal/okxorder signs through it too.
func (c Config) Sign(req *http.Request, requestPath, body string, now time.Time) {
	ts := now.UTC().Format("2006-01-02T15:04:05.000Z")
	req.Header.Set("OK-ACCESS-KEY", c.Key)
	req.Header.Set("OK-ACCESS-SIGN", sign(c.Secret, ts, req.Method, requestPath, body))
	req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
}

// sign is OKX's request signature: base64(HMAC-SHA256(secret, timestamp + method + requestPath + body)).
func sign(secret, ts, method, requestPath, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + method + requestPath + body))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// APIError is an OKX error response (code / msg only; never echoes the request headers).
type APIError struct{ Path, Code, Msg string }

func (e *APIError) Error() string { return "okx " + e.Path + ": " + e.Code + " " + e.Msg }

// get calls one allow-listed read endpoint and decodes its data array into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if !ReadAllowed(path) {
		return fmt.Errorf("okxacct: %s is not an allowed read endpoint", path)
	}
	if c.cfg.Proxy == nil {
		return errors.New("okxacct: no private proxy configured")
	}
	rp := path
	if len(q) > 0 {
		rp += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+rp, nil)
	if err != nil {
		return err
	}
	c.cfg.Sign(req, rp, "", c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("okx %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("okx %s: http %d, not JSON", path, resp.StatusCode)
	}
	if env.Code != "0" {
		return &APIError{path, env.Code, env.Msg}
	}
	return json.Unmarshal(env.Data, out)
}
