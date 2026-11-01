// Command server runs the recorder + scanner: OKX public data → SQLite → fact panel, rules,
// track records → HTTP/SSE + embedded web UI, plus simulated (paper) trading. Real orders only as
// trade proposals the owner approves (internal/realtrade), dry run unless CTG_TRADE_MODE=live.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/alerts"
	"crypto-trader-go/internal/api"
	"crypto-trader-go/internal/devapi"
	"crypto-trader-go/internal/interact"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
	"crypto-trader-go/internal/realtrade"
	"crypto-trader-go/internal/refdata"
	"crypto-trader-go/internal/store"
	"crypto-trader-go/internal/strategyhost"
	"crypto-trader-go/internal/trade"
	"crypto-trader-go/internal/trade/mdlive"
	"crypto-trader-go/web"
)

func main() {
	addr := flag.String("addr", envOr("CTG_ADDR", "127.0.0.1:8686"), "listen address")
	dbPath := flag.String("db", envOr("CTG_DB", "data/ctg.db"), "SQLite path")
	threshold := flag.Float64("threshold", 5_000_000, "first-run default 12h USDT turnover threshold (settings page overrides)")
	topN := flag.Int("top", 200, "active pool cap")
	cats := flag.String("categories", "", "comma list of instCategory to include (empty = all: 1 crypto, 3 stock/ETF, 4 commodity)")
	replayH := flag.Int("replay-hours", 336, "hourly replay depth for rule records")
	capMB := flag.Int("storage-cap-mb", market.DefaultCapMB, "first-run default SQLite size cap in MB (settings page overrides); see retention.go")
	serveWeb := flag.Bool("web", true, "also serve the embedded web UI (false = API only, e.g. behind a separate frontend container)")
	doh := flag.Bool("doh", os.Getenv("CTG_DOH") == "1", "resolve OKX hosts via DNS-over-HTTPS (for polluted DNS)")
	flag.Parse()

	// Optional outbound HTTP proxy from the environment only (may carry credentials; never logged).
	var proxy *url.URL
	if p := os.Getenv("CTG_PROXY"); p != "" {
		u, err := url.Parse(p)
		if err != nil || u.Host == "" {
			log.Fatal("CTG_PROXY must be a URL like http://user:pass@127.0.0.1:7890")
		}
		proxy = u
		log.Printf("outbound via HTTP proxy %s (credentials %v), DoH %v", u.Host, u.User != nil, *doh)
	}
	okx.ConfigureNetwork(proxy, *doh)

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	var catSet map[string]bool
	if *cats != "" {
		catSet = map[string]bool{}
		for _, c := range strings.Split(*cats, ",") {
			catSet[strings.TrimSpace(c)] = true
		}
	}
	cfg := market.Config{Threshold: *threshold, TopN: *topN, Categories: catSet, ReplayHours: *replayH, StorageCap: int64(*capMB) << 20}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng := market.New(cfg, okx.NewREST(), st)
	eng.Ref = refdata.New(okx.HTTPClient())
	eng.Ref.Symbols = eng.CryptoBases
	eng.Ref.Load = func() (string, bool) { return st.Setting("refdata.coingecko") }
	eng.Ref.Save = func(s string) error { return st.PutSetting("refdata.coingecko", s) }
	go eng.Ref.Run(ctx)
	if err := eng.Start(ctx); err != nil {
		log.Fatal(err)
	}
	al, err := alerts.New(st, eng, okx.HTTPClient())
	if err != nil {
		log.Fatal(err)
	}
	eng.OnConfirmed(al.OnSnapshot)
	go al.Run(ctx)
	dev, err := devapi.New(st)
	if err != nil {
		log.Fatal(err)
	}
	go dev.Run(ctx)

	// The owner's own OKX positions, read-only, only through OKX_PRIVATE_PROXY (disabled without it or the key).
	acctCfg, err := okxacct.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	acct := okxacct.New(acctCfg)
	acct.CtVal = func(id string) float64 { i, _ := eng.InstrumentMeta(id); return i.CtVal }
	if acct.Enabled() {
		log.Printf("okx account: read-only positions via private proxy %s", acctCfg.Proxy.Host)
		dev.SetPositions(func(ctx context.Context, inst string) (any, error) { return acct.Snapshot(ctx, inst) })
		dev.SetOKXRead(func(ctx context.Context, path, query string) (any, error) {
			q, err := url.ParseQuery(query)
			if err != nil {
				return nil, fmt.Errorf("query: %w", err)
			}
			return acct.Read(ctx, path, q)
		})
		dev.SetOKXHistory(func(ctx context.Context, a map[string]string) (any, error) {
			begin, err := okxacct.ParseDay(a["begin"])
			if err != nil {
				return nil, err
			}
			end, err := okxacct.ParseDay(a["end"])
			if err != nil {
				return nil, err
			}
			if len(strings.TrimSpace(a["end"])) == 10 { // a plain date includes that whole day
				end += 24*3600*1000 - 1
			}
			n, _ := strconv.Atoi(a["sample"])
			return acct.History(ctx, okxacct.HistoryQuery{Kind: a["kind"], InstID: strings.ToUpper(a["instId"]), Ccy: strings.ToUpper(a["ccy"]),
				Group: a["group"], Begin: begin, End: end, Sample: n})
		})
	} else if acctCfg.Key != "" || acctCfg.Proxy != nil {
		log.Printf("okx account: disabled (needs OKX_API_KEY, OKX_API_SECRET, OKX_API_PASSPHRASE and OKX_PRIVATE_PROXY)")
	}

	// Real trades: only proposals the owner approves (market + TP/SL, isolated margin), sent only when
	// CTG_TRADE_MODE is exactly "live"; otherwise built, validated and recorded (dry run).
	orders, err := okxorder.New(okxorder.ModeFromEnv(), acctCfg)
	if err != nil {
		log.Fatal(err)
	}
	rt, err := realtrade.New(st, orders, realtrade.Options{Limits: realtrade.LimitsFromEnv(), PublicURL: envOr("CTG_PUBLIC_URL", "https://ctg.gugenzzz.top"),
		Quote: func(id string) (float64, int64, bool) { q, ok := eng.TradeQuote(id); return q.Last, q.Ts, ok },
		Spec: func(id string) (okxorder.Spec, bool) {
			i, ok := eng.InstrumentMeta(id)
			return okxorder.Spec{CtVal: i.CtVal, LotSz: i.LotSz, MinSz: i.MinSz, TickSz: i.TickSz, MaxLever: i.MaxLever}, ok
		}})
	if err != nil {
		log.Fatal(err)
	}
	rt.SetAccount(acct)
	rt.SetOnChange(func() { eng.Publish("realtrade", struct{}{}) })
	dev.SetTradeProposals(rt.ProposeTool, rt.ListTool)
	al.SetOnFired(func(evs []store.AlertEvent) {
		for _, ev := range evs {
			rt.Publish(realtrade.Signal{Type: "alert", InstID: ev.InstID, Vars: map[string]string{"rule": ev.RuleName, "ruleId": strconv.FormatInt(ev.RuleID, 10),
				"message": ev.Message, "value": strconv.FormatFloat(ev.Value, 'f', -1, 64)}})
		}
	})
	tradeBot, err := realtrade.BotFromEnv(rt, okx.HTTPClient())
	if err != nil {
		log.Fatal(err)
	}
	if tradeBot != nil {
		go tradeBot.Run(ctx)
	}
	log.Printf("real trading: %s (proposals need the owner's approval), trade bot %v", orders.Mode(), tradeBot != nil)

	// Paper trading: order layer + strategy runtime + interaction layer + optional AI review.
	tsvc, err := trade.New(st, mdlive.New(eng), trade.Options{})
	if err != nil {
		log.Fatal(err)
	}
	host := strategyhost.New(strategyhost.Options{})
	go func() {
		if err := host.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("strategy host: %v", err)
		}
	}()
	hub, err := interact.New(st, okx.HTTPClient(), interact.Options{
		Commands: trade.Commands{S: tsvc},
		OnChange: func() { eng.Publish("interact", struct{}{}) },
	})
	if err != nil {
		log.Fatal(err)
	}
	go hub.Run(ctx)
	gate, err := aigate.New(st, aigate.Options{})
	if err != nil {
		log.Fatal(err)
	}
	// Chat agent tools: the external API's MCP endpoint on loopback with keys that live only in memory.
	mcpHost := *addr
	if strings.HasPrefix(mcpHost, ":") || strings.HasPrefix(mcpHost, "0.0.0.0:") {
		mcpHost = "127.0.0.1:" + mcpHost[strings.LastIndex(mcpHost, ":")+1:]
	}
	gate.SetAgent(aigate.Agent{MCPURL: "http://" + mcpHost + devapi.MCPPath, MCPToken: dev.OwnerKey("ai-agent", 600, 120),
		SandboxToken: dev.InternalKey("ai-sandbox", 120, 30)})
	gate.SetCandles(func(inst string, limit int) ([]aigate.Bar, error) { // AI plans followed on 1m candles
		cs, err := eng.Candles(inst, "1m", limit)
		out := make([]aigate.Bar, len(cs))
		for i, c := range cs {
			out[i] = aigate.Bar{T: c.T, H: c.H, L: c.L, C: c.C}
		}
		return out, err
	})
	gate.SetNotify(al.Notify) // "answer ready" for chats nobody was watching (Telegram channel, if set)
	gate.SetApplyParams(func(instanceID int64, params map[string]float64) error {
		_, err := tsvc.UpdateInstance(instanceID, trade.InstancePatch{Params: params})
		return err
	})
	gate.SetOnProposal(func(p aigate.Proposal) {
		hub.Notify(trade.Event{Kind: "ai", AccountID: p.AccountID, Title: "AI 调参申请（需批准）",
			Lines: []string{p.Strategy + "：" + p.Rationale}, Ts: time.Now().UnixMilli()})
	})
	rt.SetRunner(func(title, prompt, instID string) (int64, error) { // signal / schedule triggers: a headless chat
		return gate.StartChat(aigate.ChatInput{Message: prompt, InstID: instID, Title: title})
	})
	go rt.Run(ctx)
	go func() {
		if err := gate.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("ai gate: %v", err)
		}
	}()
	tsvc.SetRunner(host)
	tsvc.SetInteractor(hub)
	tsvc.SetReviewer(gate)
	tsvc.SetOnChange(func(acct int64, kind string) {
		eng.Publish("trade", map[string]any{"accountId": acct, "kind": kind})
	})
	eng.OnConfirmed(tsvc.OnMinute)
	go tsvc.Run(ctx)
	var dist fs.FS
	if *serveWeb {
		if dist, err = fs.Sub(web.Dist, "dist"); err != nil {
			log.Fatal(err)
		}
	}
	srv := &http.Server{Addr: *addr, Handler: (&api.Server{E: eng, Web: dist, Alerts: al, Dev: dev,
		Icons:    &api.Icons{Dir: filepath.Join(filepath.Dir(*dbPath), "icons"), Client: okx.HTTPClient()},
		Mounts:   []func(*http.ServeMux){tsvc.Register, hub.Register, gate.Register, acct.Register, rt.Register, (&api.Mobile{Dir: filepath.Join(filepath.Dir(*dbPath), "mobile")}).Register},
		AdminKey: os.Getenv("CTG_ADMIN_KEY")}).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("listening on http://%s (threshold %.0f, top %d, db %s)", *addr, *threshold, *topN, *dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // let the writer flush
	st.DB.Close()
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
