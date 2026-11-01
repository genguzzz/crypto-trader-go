package devapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/alerts"
	"crypto-trader-go/internal/market"
)

const (
	OpenPrefix = "/api/open/v1/"
	MCPPath    = "/mcp"
)

// Mount registers the open REST API, its OpenAPI document, the MCP endpoint and the owner's
// management routes (/api/v1/dev/*, same access as the rest of the web UI).
func (s *Service) Mount(mux *http.ServeMux, e *market.Engine, al *alerts.Service) {
	s.eps = s.buildEndpoints(e, al)
	for _, ep := range s.eps {
		if ep.owner {
			continue // MCP only (owner keys); not part of the open REST API
		}
		mux.Handle("GET "+OpenPrefix+ep.Path, cors(s.rest(ep)))
	}
	mux.Handle("GET "+OpenPrefix+"openapi.json", cors(http.HandlerFunc(s.openapi)))
	mux.Handle("OPTIONS "+OpenPrefix+"{path...}", cors(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	mux.Handle(MCPPath, cors(http.HandlerFunc(s.mcp)))

	mux.HandleFunc("GET /api/v1/dev/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Config()) })
	mux.HandleFunc("PUT /api/v1/dev/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&c); err != nil {
			writeErr(w, 400, "invalid JSON")
			return
		}
		out, err := s.SetConfig(c)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/dev/keys", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Keys()) })
	mux.HandleFunc("POST /api/v1/dev/keys", func(w http.ResponseWriter, r *http.Request) {
		var in CreateInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "invalid JSON")
			return
		}
		out, err := s.Create(in)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("PATCH /api/v1/dev/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in UpdateInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "invalid JSON")
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		out, err := s.Update(id, in)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("DELETE /api/v1/dev/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := s.Delete(id); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/dev/log", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Log()) })
	mux.HandleFunc("GET /api/v1/dev/endpoints", func(w http.ResponseWriter, r *http.Request) {
		var eps []*Endpoint // the open API only (owner endpoints are for the owner's agent)
		for _, ep := range s.eps {
			if !ep.owner {
				eps = append(eps, ep)
			}
		}
		writeJSON(w, 200, map[string]any{"restBase": strings.TrimSuffix(OpenPrefix, "/"), "mcpPath": MCPPath, "endpoints": eps,
			"limits": map[string]int{"defaultRatePerMin": DefaultRatePerMin, "maxRatePerMin": MaxRatePerMin, "maxBurst": MaxBurst}})
	})
}

func statusOf(err error) int {
	if errors.Is(err, ErrNotFound) {
		return 404
	}
	return 400
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// cors: the open API authenticates with headers only (no cookies), so any origin may call it.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		hd.Set("Access-Control-Allow-Headers", "Authorization, X-API-Key, Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version")
		hd.Set("Access-Control-Expose-Headers", "X-RateLimit-Limit, X-RateLimit-Burst, X-RateLimit-Remaining, X-RateLimit-Reset, X-RateLimit-Cost, Retry-After")
		hd.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func secs(d time.Duration) string { return strconv.Itoa(int(math.Ceil(d.Seconds()))) }

func setQuota(w http.ResponseWriter, q *Quota, cost int) {
	if q == nil {
		return
	}
	h := w.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(q.Limit))
	h.Set("X-RateLimit-Burst", strconv.Itoa(q.Burst))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(q.Remaining))
	h.Set("X-RateLimit-Reset", secs(q.Reset))
	h.Set("X-RateLimit-Cost", strconv.Itoa(cost))
	if q.Retry > 0 {
		h.Set("Retry-After", secs(q.Retry))
	}
}

func denialBody(d *Denial) map[string]any {
	out := map[string]any{"error": d.Msg}
	if d.Quota != nil && d.Quota.Retry > 0 {
		out["retryAfterMs"] = d.Quota.Retry.Milliseconds()
		out["scope"] = d.Quota.Scope
	}
	return out
}

func (s *Service) rest(ep *Endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		q := r.URL.Query()
		args, bindErr := ep.bind(func(k string) (string, bool) {
			if v := r.PathValue(k); v != "" {
				return v, true
			}
			return q.Get(k), q.Has(k)
		})
		cost := 1
		if bindErr == nil {
			cost = ep.Cost(args)
		}
		c, quota, den := s.Authorize(r, cost)
		setQuota(w, quota, cost)
		entry := LogEntry{Ts: start.UnixMilli(), Via: "rest", Endpoint: ep.Name, Cost: cost}
		if c != nil {
			entry.KeyID, entry.KeyName, entry.Prefix = c.ID, c.Name, c.Prefix
		}
		finish := func(code int, errMsg string) {
			entry.Status, entry.Ms, entry.Error = code, time.Since(start).Milliseconds(), errMsg
			s.record(entry)
		}
		if den != nil {
			if den.Status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="okx-helper"`)
			}
			writeJSON(w, den.Status, denialBody(den))
			finish(den.Status, den.Msg)
			return
		}
		if bindErr != nil {
			writeErr(w, 400, bindErr.Error())
			finish(400, bindErr.Error())
			return
		}
		v, err := ep.run(withCaller(r.Context(), c), args)
		if err != nil {
			writeErr(w, 400, err.Error())
			finish(400, err.Error())
			return
		}
		writeJSON(w, 200, v)
		finish(200, "")
	})
}

// ---- OpenAPI 3.1 ----

func (s *Service) openapi(w http.ResponseWriter, r *http.Request) {
	paths := map[string]any{}
	for _, ep := range s.eps {
		if ep.owner {
			continue
		}
		var params []any
		for _, p := range ep.Params {
			params = append(params, map[string]any{"name": p.Name, "in": p.In, "required": p.Required || p.In == "path", "description": p.Desc, "schema": schemaOf(p)})
		}
		cost := ep.CostTxt
		if cost == "" {
			cost = "1"
		}
		paths["/"+ep.Path] = map[string]any{"get": map[string]any{
			"operationId": ep.Name, "summary": ep.Summary, "description": ep.Desc + "\n\nRate-limit cost: " + cost + ".",
			"parameters": params, "x-ratelimit-cost": cost,
			"responses": map[string]any{
				"200": map[string]any{"description": "JSON result", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{}}}},
				"400": map[string]any{"$ref": "#/components/responses/Error"},
				"401": map[string]any{"$ref": "#/components/responses/Error"},
				"429": map[string]any{"$ref": "#/components/responses/Error"},
				"503": map[string]any{"$ref": "#/components/responses/Error"},
			},
		}}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "OKX Helper open API", "version": "1",
			"description": "Read-only OKX USDT-perpetual research data recorded by this server: pool snapshot with fact panel and rule scores, candles, open interest, funding, " +
				"order book, order flow, liquidations and track records. Times are Unix milliseconds (exchange time), amounts are USDT unless stated. " +
				"Unknown values are omitted or null, never 0. Scores are user rules ranked within the pool, unvalidated, not advice.\n\n" +
				"Authenticate with `Authorization: Bearer <key>` (or `X-API-Key`). Each key has a token bucket (per-minute rate, burst); every response carries " +
				"X-RateLimit-Limit / -Burst / -Remaining / -Reset / -Cost, and 429 responses carry Retry-After. The same tools are available over MCP at " + MCPPath + "."},
		"servers":  []any{map[string]any{"url": strings.TrimSuffix(OpenPrefix, "/")}},
		"security": []any{map[string]any{"bearer": []any{}}, map[string]any{"apiKey": []any{}}},
		"paths":    paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearer": map[string]any{"type": "http", "scheme": "bearer"},
				"apiKey": map[string]any{"type": "apiKey", "in": "header", "name": "X-API-Key"},
			},
			"responses": map[string]any{"Error": map[string]any{"description": "error", "content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{"type": "object", "properties": map[string]any{"error": map[string]any{"type": "string"}, "retryAfterMs": map[string]any{"type": "integer"}}}}}}},
		},
	}
	writeJSON(w, 200, doc)
}

func schemaOf(p Param) map[string]any {
	sc := map[string]any{"type": p.Type}
	if len(p.Enum) > 0 {
		sc["enum"] = p.Enum
	}
	if p.Default != nil {
		sc["default"] = p.Default
	}
	if p.Min != 0 || p.Max != 0 {
		sc["minimum"], sc["maximum"] = p.Min, p.Max
	}
	if p.Desc != "" {
		sc["description"] = p.Desc
	}
	return sc
}

// ---- MCP (streamable HTTP transport, stateless; JSON responses only) ----

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

const mcpInstructions = "Read-only OKX USDT-perpetual research data. Start with get_system_status (freshness) and get_universe (pool snapshot: fact panel, " +
	"percentiles, rule scores); drill into an instrument with get_instrument, get_candles, get_orderflow, get_liquidations, get_series and get_orderbook. " +
	"Times are Unix ms; amounts USDT unless stated; missing values mean unknown, never zero. Scores are unvalidated user rules, not advice; " +
	"check get_rule_track_record before trusting one. Calls are rate-limited per key (see get_usage); prefer small limits."

func (s *Service) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeErr(w, http.StatusMethodNotAllowed, "this MCP server is stateless: POST JSON-RPC messages; no SSE stream is offered")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	var raw json.RawMessage
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
		return
	}
	batch := len(raw) > 0 && raw[0] == '['
	var reqs []rpcReq
	if batch {
		if err := json.Unmarshal(raw, &reqs); err != nil {
			writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, "invalid request"}})
			return
		}
	} else {
		var one rpcReq
		if err := json.Unmarshal(raw, &one); err != nil {
			writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, "invalid request"}})
			return
		}
		reqs = []rpcReq{one}
	}
	// authenticate once per HTTP request (protocol messages are free; tool calls pay per call)
	c, quota, den := s.Authorize(r, 0)
	setQuota(w, quota, 0)
	if den == nil && c.Owner {
		c.Chat, _ = strconv.ParseInt(r.Header.Get("X-Ctg-Chat"), 10, 64)
	}
	if den != nil {
		if den.Status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="okx-helper"`)
		}
		id := json.RawMessage("null")
		if !batch && len(reqs[0].ID) > 0 {
			id = reqs[0].ID
		}
		writeJSON(w, den.Status, rpcResp{JSONRPC: "2.0", ID: id, Error: &rpcErr{-32001, den.Msg}})
		s.record(LogEntry{Ts: time.Now().UnixMilli(), Via: "mcp", Endpoint: reqs[0].Method, Status: den.Status, Error: den.Msg})
		return
	}
	var out []rpcResp
	for _, q := range reqs {
		if resp, ok := s.mcpOne(w, r, c, q); ok {
			out = append(out, resp)
		}
	}
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted) // only notifications / responses
	case batch:
		writeJSON(w, 200, out)
	default:
		writeJSON(w, 200, out[0])
	}
}

// mcpOne handles one message; ok = false for notifications (no response).
func (s *Service) mcpOne(w http.ResponseWriter, r *http.Request, c *Caller, q rpcReq) (rpcResp, bool) {
	if len(q.ID) == 0 || string(q.ID) == "null" {
		return rpcResp{}, false
	}
	resp := rpcResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(q.Params, &p)
		ver := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "okx-helper", "title": "OKX Helper market data", "version": "1"},
			"instructions":    mcpInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := make([]any, 0, len(s.eps))
		for _, ep := range s.eps {
			if ep.owner && !c.Owner {
				continue
			}
			props, req := map[string]any{}, []string{}
			for _, p := range ep.Params {
				props[p.Name] = schemaOf(p)
				if p.Required || p.In == "path" {
					req = append(req, p.Name)
				}
			}
			cost := ep.CostTxt
			if cost == "" {
				cost = "1"
			}
			tools = append(tools, map[string]any{
				"name": ep.Name, "title": ep.Summary, "description": ep.Summary + ". " + ep.Desc + " Rate-limit cost: " + cost + ".",
				"inputSchema": map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false},
				"annotations": map[string]any{"title": ep.Summary, "readOnlyHint": !ep.write, "openWorldHint": false},
			})
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		resp.Result = s.mcpCall(w, r, c, q.Params)
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + q.Method}
	}
	return resp, true
}

func toolText(isErr bool, text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
}

func (s *Service) mcpCall(w http.ResponseWriter, r *http.Request, c *Caller, raw json.RawMessage) map[string]any {
	start := time.Now()
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return toolText(true, "invalid params")
	}
	entry := LogEntry{Ts: start.UnixMilli(), KeyID: c.ID, KeyName: c.Name, Prefix: c.Prefix, Via: "mcp", Endpoint: p.Name}
	finish := func(code int, msg string) {
		entry.Status, entry.Ms, entry.Error = code, time.Since(start).Milliseconds(), msg
		s.record(entry)
	}
	var ep *Endpoint
	for _, x := range s.eps {
		if x.Name == p.Name && (!x.owner || c.Owner) {
			ep = x
		}
	}
	if ep == nil {
		finish(404, "unknown tool")
		return toolText(true, "unknown tool "+p.Name)
	}
	args, err := ep.bind(func(k string) (string, bool) {
		v, ok := p.Arguments[k]
		if !ok || v == nil {
			return "", false
		}
		switch x := v.(type) {
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), true
		case string:
			return x, true
		case map[string]any, []any: // object parameters (subscribe_signal spec) travel as JSON
			b, err := json.Marshal(x)
			return string(b), err == nil
		default:
			return fmt.Sprint(x), true
		}
	})
	if err != nil {
		finish(400, err.Error())
		return toolText(true, err.Error())
	}
	entry.Cost = ep.Cost(args)
	if entry.Cost > 0 {
		_, quota, den := s.Authorize(r, entry.Cost)
		setQuota(w, quota, entry.Cost)
		if den != nil {
			finish(den.Status, den.Msg)
			b, _ := json.Marshal(denialBody(den))
			return toolText(true, string(b))
		}
	}
	v, err := ep.run(withCaller(r.Context(), c), args)
	if err != nil {
		finish(400, err.Error())
		return toolText(true, err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		finish(500, err.Error())
		return toolText(true, err.Error())
	}
	finish(200, "")
	return toolText(false, string(b))
}
