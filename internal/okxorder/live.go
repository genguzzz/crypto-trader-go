package okxorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"crypto-trader-go/internal/okxacct"
)

// New returns the client for mode: live only for exactly Live and a complete key + proxy config,
// otherwise a dry run (so a misconfigured live switch never sends anything).
func New(mode Mode, cfg okxacct.Config) (Client, error) {
	if mode != Live {
		return NewDryRun(), nil
	}
	if !cfg.Enabled() {
		return nil, errors.New("CTG_TRADE_MODE=live needs OKX_API_KEY, OKX_API_SECRET, OKX_API_PASSPHRASE and OKX_PRIVATE_PROXY")
	}
	return &live{cfg: cfg, base: okxacct.DefaultBaseURL, http: cfg.HTTPClient(), now: time.Now}, nil
}

// live sends the two allowed POSTs, signed through okxacct.Config, over the private proxy only.
type live struct {
	cfg  okxacct.Config
	base string
	http *http.Client
	now  func() time.Time
}

func (*live) Mode() Mode { return Live }

func (c *live) Place(ctx context.Context, o Order) (Result, error) {
	if o.instID == "" {
		return Result{}, errors.New("empty order")
	}
	res := Result{Mode: Live, Requests: o.Requests()}
	for _, rq := range res.Requests {
		raw, data, err := c.post(ctx, rq)
		if raw != nil {
			res.Responses = append(res.Responses, raw)
		}
		if err != nil {
			return res, err
		}
		if rq.Path == pathOrder {
			var rows []struct {
				OrdID string `json:"ordId"`
				SCode string `json:"sCode"`
				SMsg  string `json:"sMsg"`
			}
			json.Unmarshal(data, &rows)
			if len(rows) == 0 || rows[0].SCode != "0" || rows[0].OrdID == "" {
				if len(rows) > 0 {
					return res, fmt.Errorf("okx order rejected: %s %s", rows[0].SCode, rows[0].SMsg)
				}
				return res, errors.New("okx order: empty answer")
			}
			res.OrdID = rows[0].OrdID
		}
	}
	return res, nil
}

// post sends one of the two allowed requests and returns the raw answer and its data array.
func (c *live) post(ctx context.Context, rq Request) (json.RawMessage, json.RawMessage, error) {
	if rq.Method != http.MethodPost || (rq.Path != pathLeverage && rq.Path != pathOrder) {
		return nil, nil, fmt.Errorf("okxorder: %s %s is not allowed", rq.Method, rq.Path)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+rq.Path, bytes.NewReader(rq.Body))
	if err != nil {
		return nil, nil, err
	}
	c.cfg.Sign(req, rq.Path, string(rq.Body), c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("okx %s: %w", rq.Path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, nil, fmt.Errorf("okx %s: http %d, not JSON", rq.Path, resp.StatusCode)
	}
	raw := json.RawMessage(body)
	if env.Code != "0" {
		detail := env.Msg
		var rows []struct{ SCode, SMsg string }
		if json.Unmarshal(env.Data, &rows) == nil && len(rows) > 0 && rows[0].SMsg != "" {
			detail += " (" + rows[0].SCode + " " + rows[0].SMsg + ")"
		}
		return raw, nil, &okxacct.APIError{Path: rq.Path, Code: env.Code, Msg: detail}
	}
	return raw, env.Data, nil
}
