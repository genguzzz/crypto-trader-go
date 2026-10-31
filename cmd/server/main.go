// Command server runs the recorder + scanner: OKX public data → SQLite → fact panel, rules,
// track records → HTTP/SSE + embedded web UI, plus simulated (paper) trading. Public data only;
// never places real orders.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
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
	gate.SetAgent(aigate.Agent{MCPURL: "http://" + mcpHost + devapi.MCPPath, MCPToken: dev.InternalKey("ai-agent", 600, 120),
		SandboxToken: dev.InternalKey("ai-sandbox", 120, 30)})
	gate.SetNotify(al.Notify) // "answer ready" for chats nobody was watching (Telegram channel, if set)
	gate.SetApplyParams(func(instanceID int64, params map[string]float64) error {
		_, err := tsvc.UpdateInstance(instanceID, trade.InstancePatch{Params: params})
		return err
	})
	gate.SetOnProposal(func(p aigate.Proposal) {
		hub.Notify(trade.Event{Kind: "ai", AccountID: p.AccountID, Title: "AI 调参申请（需批准）",
			Lines: []string{p.Strategy + "：" + p.Rationale}, Ts: time.Now().UnixMilli()})
	})
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
		Mounts:   []func(*http.ServeMux){tsvc.Register, hub.Register, gate.Register, (&api.Mobile{Dir: filepath.Join(filepath.Dir(*dbPath), "mobile")}).Register},
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
