package okxorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// allowed is every request the live client can send: the path and the body keys it may carry. There is
// no transfer, withdrawal, account-setting or batch path; anything else is refused before signing.
var allowed = map[string]map[string]bool{
	pathLeverage:    keys("instId", "lever", "mgnMode", "posSide"),
	pathOrder:       keys("instId", "tdMode", "side", "posSide", "ordType", "sz", "px", "reduceOnly", "clOrdId", "attachAlgoOrds"),
	pathClosePos:    keys("instId", "mgnMode", "posSide", "autoCxl", "clOrdId"),
	pathCancelOrder: keys("instId", "clOrdId"),
	pathOrderAlgo: keys("instId", "tdMode", "side", "posSide", "ordType", "closeFraction", "reduceOnly", "tpTriggerPx", "tpOrdPx",
		"tpTriggerPxType", "slTriggerPx", "slOrdPx", "slTriggerPxType", "algoClOrdId"),
	pathAmendAlgos: keys("instId", "algoId", "newTpTriggerPx", "newTpOrdPx", "newTpTriggerPxType", "newSlTriggerPx", "newSlOrdPx",
		"newSlTriggerPxType"),
	pathCancelAlgos: keys("instId", "algoId"), // a JSON array of these
}

func keys(ks ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range ks {
		m[k] = true
	}
	return m
}

// checkRequest refuses anything outside allowed: another method or path, an unknown body key, a margin
// mode other than isolated / cross, an order or TP/SL without a ctg client id, a cancel of someone
// else's order.
func checkRequest(rq Request) error {
	ks := allowed[rq.Path]
	if rq.Method != http.MethodPost || ks == nil {
		return fmt.Errorf("okxorder: %s %s is not allowed", rq.Method, rq.Path)
	}
	var rows []map[string]any
	if rq.Path == pathCancelAlgos {
		if err := json.Unmarshal(rq.Body, &rows); err != nil || len(rows) == 0 || len(rows) > 10 {
			return fmt.Errorf("okxorder: %s needs 1–10 algo ids", rq.Path)
		}
	} else {
		var m map[string]any
		if err := json.Unmarshal(rq.Body, &m); err != nil || m == nil {
			return fmt.Errorf("okxorder: %s body is not an object", rq.Path)
		}
		rows = []map[string]any{m}
	}
	for _, m := range rows {
		for k := range m {
			if !ks[k] {
				return fmt.Errorf("okxorder: %s may not carry %q", rq.Path, k)
			}
		}
		str := func(k string) string { s, _ := m[k].(string); return s }
		if inst := str("instId"); !strings.HasSuffix(inst, "-USDT-SWAP") {
			return fmt.Errorf("okxorder: %s for %q", rq.Path, inst)
		}
		for _, k := range []string{"tdMode", "mgnMode"} {
			if v, ok := m[k]; ok && v != Isolated && v != Cross {
				return fmt.Errorf("okxorder: %s %s %v", rq.Path, k, v)
			}
		}
		switch rq.Path {
		case pathOrder, pathClosePos:
			if !Owned(str("clOrdId")) || !strings.HasPrefix(str("clOrdId"), "ctgp") {
				return fmt.Errorf("okxorder: %s without a ctg client id", rq.Path)
			}
		case pathCancelOrder:
			if !Owned(str("clOrdId")) || !strings.HasPrefix(str("clOrdId"), "ctgp") {
				return errors.New("okxorder: only ctg's own orders can be cancelled")
			}
		case pathOrderAlgo:
			if !Owned(str("algoClOrdId")) || str("closeFraction") != "1" {
				return errors.New("okxorder: only ctg's whole-position TP/SL can be placed")
			}
		case pathAmendAlgos, pathCancelAlgos:
			if str("algoId") == "" {
				return fmt.Errorf("okxorder: %s without an algo id", rq.Path)
			}
		}
	}
	return nil
}

// live sends the allowed POSTs, signed through okxacct.Config, over the private proxy only.
type live struct {
	cfg  okxacct.Config
	base string
	http *http.Client
	now  func() time.Time
}

func (*live) Mode() Mode { return Live }

func (c *live) Place(ctx context.Context, o Order) (Result, error) {
	if o.instID == "" || len(o.reqs) == 0 {
		return Result{}, errors.New("empty order")
	}
	res := Result{Mode: Live, Requests: o.Requests()}
	for _, rq := range res.Requests { // all or nothing for the checks: nothing is sent if one request is not allowed
		if err := checkRequest(rq); err != nil {
			return res, err
		}
	}
	for _, rq := range res.Requests {
		raw, data, err := c.post(ctx, rq)
		if raw != nil {
			res.Responses = append(res.Responses, raw)
		}
		if err == nil {
			var rows []struct {
				OrdID  string `json:"ordId"`
				AlgoID string `json:"algoId"`
				SCode  string `json:"sCode"`
				SMsg   string `json:"sMsg"`
			}
			json.Unmarshal(data, &rows)
			switch {
			case rq.Path == pathOrder && (len(rows) == 0 || rows[0].OrdID == ""):
				err = errors.New("okx order: empty answer")
			case rq.Path == pathOrderAlgo && (len(rows) == 0 || rows[0].AlgoID == ""):
				err = errors.New("okx TP/SL: empty answer")
			}
			for _, r := range rows {
				if r.SCode != "" && r.SCode != "0" && err == nil {
					err = fmt.Errorf("okx %s rejected: %s %s", rq.Path, r.SCode, r.SMsg)
				}
			}
			if err == nil && len(rows) > 0 {
				if rq.Path == pathOrder {
					res.OrdID = rows[0].OrdID
				}
				if rq.Path == pathOrderAlgo {
					res.AlgoID = rows[0].AlgoID
				}
			}
		}
		if err != nil {
			if rq.Optional {
				res.Warnings = append(res.Warnings, err.Error())
				continue
			}
			return res, err
		}
	}
	return res, nil
}

// post sends one allowed request and returns the raw answer and its data array.
func (c *live) post(ctx context.Context, rq Request) (json.RawMessage, json.RawMessage, error) {
	if err := checkRequest(rq); err != nil {
		return nil, nil, err
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
