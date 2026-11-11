package realtrade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Bark pushes proposal events to the owner's iPhone (and through it the paired Apple Watch) via a
// Bark server (https://github.com/Finb/bark-server, self-hosted). A new proposal is a time-sensitive
// notification that opens the proposal; later states of it are ordinary ones. Each target is a Bark
// device URL, https://<server>/<device key>; it is a secret (it can push to the device).
type Bark struct {
	s       *Service
	targets []string
	client  *http.Client
	queue   chan barkJob
}

type barkJob struct {
	kind string
	id   int64
}

// BarkFromEnv reads CTG_BARK_URL (comma-separated device URLs); nil when unset.
func BarkFromEnv(s *Service, client *http.Client) *Bark {
	var targets []string
	for _, t := range strings.Split(os.Getenv("CTG_BARK_URL"), ",") {
		if t = strings.TrimRight(strings.TrimSpace(t), "/"); strings.HasPrefix(t, "https://") || strings.HasPrefix(t, "http://") {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return NewBark(s, targets, client)
}

func NewBark(s *Service, targets []string, client *http.Client) *Bark {
	if client == nil {
		client = http.DefaultClient
	}
	return &Bark{s: s, targets: targets, client: client, queue: make(chan barkJob, 64)}
}

// barkEvents are the events worth a push; approve / reject are the owner's own answers.
var barkEvents = map[string]bool{"proposed": true, StatusExecuted: true, StatusFailed: true, StatusInvalidated: true,
	StatusExpired: true, OrdFilled: true, OrdCanceled: true, "partial": false, "closed": true}

// ProposalEvent queues a push (never blocks).
func (b *Bark) ProposalEvent(kind string, p Proposal) {
	if !barkEvents[kind] {
		return
	}
	select {
	case b.queue <- barkJob{kind: kind, id: p.ID}:
	default:
		b.s.logf("realtrade: bark queue full; dropped %s #%d", kind, p.ID)
	}
}

// Run sends queued pushes until ctx ends.
func (b *Bark) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-b.queue:
			p, err := b.s.get(j.id)
			if err != nil {
				continue
			}
			msg := barkMessage(j.kind, p, b.s.Link(p), b.s.now())
			for _, t := range b.targets {
				var err error
				for try := 0; try < 3 && ctx.Err() == nil; try++ {
					if err = b.send(ctx, t, msg); err == nil {
						break
					}
					select {
					case <-ctx.Done():
					case <-time.After(time.Duration(2<<try) * time.Second):
					}
				}
				if err != nil {
					b.s.logf("realtrade: bark %s #%d: %v", j.kind, p.ID, err)
				}
			}
		}
	}
}

// Test sends a test push to every target.
func (b *Bark) Test(ctx context.Context) error {
	for _, t := range b.targets {
		if err := b.send(ctx, t, barkPush{Title: "OKX Helper", Body: "Bark 推送测试 · " + b.s.now().In(cst8).Format("15:04:05"), Group: "OKX 交易", Level: "active"}); err != nil {
			return err
		}
	}
	return nil
}

type barkPush struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Group string `json:"group,omitempty"`
	URL   string `json:"url,omitempty"`
	Level string `json:"level,omitempty"` // active | timeSensitive | passive
	Sound string `json:"sound,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

var cst8 = time.FixedZone("UTC+8", 8*3600)

func barkMessage(kind string, p Proposal, link string, now time.Time) barkPush {
	if kind == "proposed" {
		body := summary(p)
		if r := clip(p.Rationale, 160); r != "" {
			body += "\n" + r
		}
		if p.ExpiresAt > 0 {
			body += fmt.Sprintf("\n%s 前审核（%s）", time.UnixMilli(p.ExpiresAt).In(cst8).Format("15:04"), sourceText(p))
		}
		return barkPush{Title: fmt.Sprintf("%s #%d 待审核 · %s%s %s", modeText(p.Mode), p.ID, kindText(p.Kind), sideText(p.Side), coin(p.InstID)),
			Body: body, Group: "OKX 待审核", URL: link, Level: "timeSensitive", Sound: "minuet"}
	}
	return barkPush{Title: "OKX 交易", Body: eventLine(kind, p), Group: "OKX 交易", URL: link, Level: "active"}
}

func (b *Bark) send(ctx context.Context, target string, m barkPush) error {
	body, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("bark: %w", redact(err, target))
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out, &r); err != nil || resp.StatusCode != http.StatusOK || r.Code != 200 {
		return fmt.Errorf("bark: HTTP %d %s", resp.StatusCode, clip(r.Message, 120))
	}
	return nil
}

// redact keeps the device key out of logged errors (url.Error prints the URL).
func redact(err error, target string) error {
	i := strings.LastIndex(target, "/")
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), target, target[:i+1]+"…"))
}
