package interact

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/trade"
)

// Messages are Chinese, compact, at most maxLines lines, with an emoji only as the leading status
// marker and the time (UTC+8) plus the reference on the last line. They are rendered as plain
// lines; channels that use markup escape them (see htmlText).

const (
	maxLines   = 10
	maxLineLen = 160 // runes
)

var cst = time.FixedZone("UTC+8", 8*3600)

func marker(kind string) string {
	switch kind {
	case "fill", "order", "target", "open":
		return "🟢"
	case "stop", "liquidation", "breaker":
		return "🔴"
	case "prompt":
		return "🟡"
	case "strategy":
		return "🔵"
	}
	return "⚪"
}

func modeLabel(m trade.Mode) string {
	switch m {
	case trade.ModeLive:
		return "模拟"
	case trade.ModeBacktest:
		return "回测"
	case trade.ModeReal:
		return "实盘"
	}
	return string(m)
}

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if utf8.RuneCountInString(s) <= maxLineLen {
		return s
	}
	r := []rune(s)
	return string(r[:maxLineLen-1]) + "…"
}

func header(mark string, ev trade.Event) string {
	tag := ""
	switch {
	case ev.Account != "" && ev.Mode != "":
		tag = "[" + modeLabel(ev.Mode) + "·" + ev.Account + "] "
	case ev.Account != "":
		tag = "[" + ev.Account + "] "
	case ev.Mode != "":
		tag = "[" + modeLabel(ev.Mode) + "] "
	}
	title := ev.Title
	if title == "" {
		title = kindLabel(ev.Kind)
	}
	return clip(mark + " " + tag + title)
}

func footer(ts int64, ref string) string {
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	s := time.UnixMilli(ts).In(cst).Format("01-02 15:04:05") + " UTC+8"
	if ref != "" {
		s += " · " + clip(ref)
	}
	return s
}

// body returns the event lines, cut so the whole message stays within maxLines given `reserved`
// extra lines (header, footer, ...).
func body(lines []string, reserved int) []string {
	room := maxLines - reserved
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, clip(l))
	}
	if len(out) > room {
		out = append(out[:room-1], fmt.Sprintf("…（另 %d 行）", len(out)-room+1))
	}
	return out
}

// renderEvent is a one-way notification.
func renderEvent(ev trade.Event) []string {
	out := []string{header(marker(ev.Kind), ev)}
	out = append(out, body(ev.Lines, 2)...)
	return append(out, footer(ev.Ts, ev.Ref))
}

// renderPrompt is an open question; the line before the footer says what happens on silence.
func renderPrompt(r Record, now time.Time) []string {
	out := []string{header(marker("prompt"), r.Event)}
	out = append(out, body(r.Event.Lines, 3)...)
	out = append(out, fmt.Sprintf("%s内未回复将%s", remaining(time.UnixMilli(r.Deadline).Sub(now)), r.label(r.Default)))
	return append(out, footer(r.CreatedAt, r.Event.Ref))
}

// renderResolved replaces the prompt once it is closed (keyboard removed by the channel).
func renderResolved(r Record) []string {
	out := []string{header(marker("info"), r.Event)}
	out = append(out, body(r.Event.Lines, 3)...)
	out = append(out, resultLine(r))
	return append(out, footer(r.CreatedAt, r.Event.Ref))
}

func resultLine(r Record) string {
	at := ""
	if r.AnsweredAt > 0 {
		at = " · " + time.UnixMilli(r.AnsweredAt).In(cst).Format("15:04:05")
	}
	switch {
	case r.By == byCancelled:
		return "已失效（请求已撤回）" + at
	case r.By == byTimeout:
		return "结果：" + r.label(r.Choice) + "（超时默认）" + at
	default:
		return "结果：" + r.label(r.Choice) + " · " + whoLabel(r.By) + at
	}
}

func whoLabel(by string) string {
	switch {
	case by == byWeb:
		return "网页"
	case strings.HasPrefix(by, "telegram:"):
		return "Telegram " + strings.TrimPrefix(by, "telegram:")
	}
	return by
}

func remaining(d time.Duration) string {
	if d < time.Second {
		return "即刻"
	}
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(math.Ceil(d.Seconds())))
	}
	return fmt.Sprintf("%d 分钟", int(math.Round(d.Minutes())))
}

// renderSummary merges a backlog of events into one message.
func renderSummary(evs []trade.Event) []string {
	counts := map[string]int{}
	for _, e := range evs {
		counts[e.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	var parts []string
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s ×%d", kindLabel(k), counts[k]))
	}
	mark := "⚪"
	for _, e := range evs {
		if marker(e.Kind) == "🔴" {
			mark = "🔴"
		}
	}
	out := []string{fmt.Sprintf("%s 积压 %d 条通知，已合并", mark, len(evs)), clip(strings.Join(parts, " · ")), "最近："}
	const show = 5
	from := 0
	if len(evs) > show {
		from = len(evs) - show
	}
	for _, e := range evs[from:] {
		tag := ""
		if e.Account != "" {
			tag = "[" + e.Account + "] "
		}
		t := e.Title
		if t == "" {
			t = kindLabel(e.Kind)
		}
		out = append(out, clip(time.UnixMilli(e.Ts).In(cst).Format("15:04")+" "+tag+t))
	}
	first, last := evs[0].Ts, evs[len(evs)-1].Ts
	return append(out, time.UnixMilli(first).In(cst).Format("01-02 15:04")+" – "+time.UnixMilli(last).In(cst).Format("01-02 15:04")+" UTC+8")
}

// htmlText escapes plain lines for Telegram's HTML parse mode and bolds the first line.
func htmlText(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == 0 {
			b.WriteString("<b>" + html.EscapeString(l) + "</b>")
		} else {
			b.WriteString(html.EscapeString(l))
		}
	}
	return b.String()
}

// splitText cuts on line boundaries into chunks of at most n runes.
func splitText(s string, n int) []string {
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, line := range strings.Split(s, "\n") {
		l := utf8.RuneCountInString(line) + 1
		if curLen > 0 && curLen+l > n {
			out = append(out, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
			curLen = 0
		}
		if l > n {
			r := []rune(line)
			line, l = string(r[:n-1]), n
		}
		cur.WriteString(line + "\n")
		curLen += l
	}
	if curLen > 0 {
		out = append(out, strings.TrimRight(cur.String(), "\n"))
	}
	return out
}
