// Package features computes the "fact panel": direction-free, unit-labelled per-instrument
// measurements. The same Compute runs on live 1m-backed series and on hourly replay series, so
// live and historical values share one definition (blueprint revision §2.1).
package features

import (
	"math"
	"sort"
)

const (
	Minute = int64(60_000)
	Hour   = 60 * Minute
)

// Series exposes what a feature may read at decision time t (ms, bar-close aligned).
// Implementations must only return data that was available at t.
type Series interface {
	// Close is the close of the bar that closes exactly at t.
	Close(t int64) (float64, bool)
	// Quote is the USDT turnover of bars closing in (from, to]; ok=false if any bar is missing.
	Quote(from, to int64) (float64, bool)
	// OI is the latest coin-denominated open interest sampled at or before t (bounded staleness).
	OI(t int64) (float64, bool)
	// FundingSettled is the last settled funding rate at or before t and its interval in hours.
	FundingSettled(t int64) (rate, intervalH float64, ok bool)
}

type Field struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Unit       string `json:"unit"`
	Desc       string `json:"desc"`
	Replayable bool   `json:"replayable"` // available in hourly replay (else not replayable)
}

var Fields = []Field{
	{"ret_1h", "1h 涨跌", "log", "永续收盘对数收益，1 小时", true},
	{"ret_4h", "4h 涨跌", "log", "永续收盘对数收益，4 小时", true},
	{"ret_24h", "24h 涨跌", "log", "永续收盘对数收益，24 小时", true},
	{"excess_btc_4h", "相对 BTC 4h", "log", "4h 收益减去 BTC-USDT-SWAP 同期收益（实测最强单变量，方向未验证）", true},
	{"trend_z_4h", "4h 趋势强度", "z", "ret_4h ÷ (过去 48 根 1H 收益标准差 × √4)", true},
	{"oi_chg_1h", "OI 变化 1h", "log", "币本位持仓量 oiCcy 的对数变化，1 小时（排除价格导致的 oiUsd 变化）", true},
	{"oi_chg_4h", "OI 变化 4h", "log", "币本位持仓量 oiCcy 的对数变化，4 小时", true},
	{"vol_surprise_4h", "放量 4h", "log", "log(近 4h USDT 成交额 ÷ 过去 7 天 42 个 4h 成交额的中位数)；界面显示为倍数", true},
	{"funding_hourly", "资金费/小时", "rate/h", "最近一次已结算资金费 ÷ 结算间隔小时数（4h/8h 已换算）", true},
	{"funding_pred_hourly", "预测资金费/小时", "rate/h", "当前周期预测资金费 ÷ 间隔小时数（仅实时）", false},
	{"basis", "期现基差", "log", "log(永续价/OKX 同名 USDT 现货价)；无现货映射为未知（仅实时）", false},
	{"spread_bps", "点差", "bps", "(ask−bid)/mid，成本 gate 用（仅实时）", false},
	{"turnover12h", "12h 成交额", "USDT", "连续 720 根已确认 1m K 线 volCcyQuote 之和", false},
	// Research additions 2026-09-28 (docs/research-trend-signals.md). Direction-free like the rest;
	// whether any of them predicts anything is decided only by the rule track record.
	{"beta_btc_48h", "BTC β 48h", "x", "过去 48 根 1H 对数收益对 BTC-USDT-SWAP 的回归斜率（跟随大盘的倍数）", true},
	{"resid_1h", "残差收益 1h", "log", "ret_1h − β×BTC 同期收益：剔除大盘带动后的自身涨跌（β 中性相对强弱）", true},
	{"resid_4h", "残差收益 4h", "log", "ret_4h − β×BTC 同期收益", true},
	{"trend_ema", "EMA 趋势", "z", "(EMA8 − EMA24)（1H 收盘）÷ 价格 ÷ 1H 收益标准差：按波动率归一化的均线趋势（MACD 类，参见 Baz 等 2015）", true},
	{"eff_ratio_24h", "趋势效率 24h", "0–1", "Kaufman 效率比：|24h 净变动| ÷ 24 根 1H 变动绝对值之和；1 = 单边直线，0 = 来回震荡", true},
	{"ret_15m", "15m 涨跌", "log", "永续收盘对数收益，15 分钟（仅实时：回放只有整点数据）", false},
	{"resid_15m", "残差收益 15m", "log", "ret_15m − β×BTC 同期收益（仅实时）", false},
}

var fieldIndex = func() map[string]Field {
	m := map[string]Field{}
	for _, f := range Fields {
		m[f.Key] = f
	}
	return m
}()

func Lookup(key string) (Field, bool) { f, ok := fieldIndex[key]; return f, ok }

type Values map[string]float64

// Compute returns the replayable fields at decision time t. btc may be nil.
func Compute(s Series, btc Series, t int64) Values {
	v := Values{}
	c0, ok := s.Close(t)
	if !ok || c0 <= 0 {
		return v
	}
	ret := func(h int64) (float64, bool) {
		c, ok := s.Close(t - h*Hour)
		if !ok || c <= 0 {
			return 0, false
		}
		return math.Log(c0 / c), true
	}
	for _, h := range []int64{1, 4, 24} {
		if r, ok := ret(h); ok {
			v[map[int64]string{1: "ret_1h", 4: "ret_4h", 24: "ret_24h"}[h]] = r
		}
	}
	if r4, ok := v["ret_4h"]; ok && btc != nil {
		b0, ok1 := btc.Close(t)
		b4, ok2 := btc.Close(t - 4*Hour)
		if ok1 && ok2 && b4 > 0 {
			v["excess_btc_4h"] = r4 - math.Log(b0/b4)
		}
	}
	// hourly closes over the past 48h (hour-aligned, ending at t's hour floor), plus t itself when
	// t is inside an hour (live): volatility, beta and the EMA trend read these
	h0 := t - t%Hour
	cs := hourlyCloses(s, h0)
	var bcs []float64
	if btc != nil {
		bcs = hourlyCloses(btc, h0)
	}
	var sd float64
	if cs != nil {
		sd = stdev(logRets(cs))
	}
	if r4, ok := v["ret_4h"]; ok && sd > 1e-9 { // guard against float noise on flat/constant series
		v["trend_z_4h"] = r4 / (sd * 2)
	}
	if cs != nil && sd > 1e-9 {
		px := cs
		if t != h0 {
			px = append(append([]float64(nil), cs...), c0)
		}
		v["trend_ema"] = (ema(px, 8) - ema(px, 24)) / c0 / sd
	}
	// efficiency ratio over the 24 hours ending at t, from closes at t−k·1h
	w := make([]float64, 0, 25)
	for k := int64(24); k >= 0; k-- {
		c, ok := s.Close(t - k*Hour)
		if !ok || c <= 0 {
			w = nil
			break
		}
		w = append(w, c)
	}
	if w != nil {
		path := 0.0
		for i := 1; i < len(w); i++ {
			path += math.Abs(w[i] - w[i-1])
		}
		if path > 0 {
			v["eff_ratio_24h"] = math.Abs(w[len(w)-1]-w[0]) / path
		}
	}
	if c15, ok := s.Close(t - 15*Minute); ok && c15 > 0 {
		v["ret_15m"] = math.Log(c0 / c15)
	}
	if cs != nil && bcs != nil {
		if beta, ok := slope(logRets(bcs), logRets(cs)); ok {
			v["beta_btc_48h"] = beta
			b0, ok0 := btc.Close(t)
			for _, x := range []struct {
				key, ret string
				d        int64
			}{{"resid_15m", "ret_15m", 15 * Minute}, {"resid_1h", "ret_1h", Hour}, {"resid_4h", "ret_4h", 4 * Hour}} {
				r, ok1 := v[x.ret]
				bp, ok2 := btc.Close(t - x.d)
				if ok0 && ok1 && ok2 && b0 > 0 && bp > 0 {
					v[x.key] = r - beta*math.Log(b0/bp)
				}
			}
		}
	}
	if oi0, ok := s.OI(t); ok && oi0 > 0 {
		for _, h := range []int64{1, 4} {
			if oi, ok := s.OI(t - h*Hour); ok && oi > 0 {
				v[map[int64]string{1: "oi_chg_1h", 4: "oi_chg_4h"}[h]] = math.Log(oi0 / oi)
			}
		}
	}
	if q, ok := s.Quote(t-4*Hour, t); ok {
		h0 := t - t%Hour
		var base []float64
		for k := int64(1); k <= 42; k++ {
			end := h0 - (k-1)*4*Hour - 4*Hour
			if b, ok := s.Quote(end-4*Hour, end); ok {
				base = append(base, b)
			}
		}
		if len(base) >= 21 { // at least half of 7 days
			v["vol_surprise_4h"] = math.Log1p(q) - math.Log1p(median(base))
		}
	}
	if r, iv, ok := s.FundingSettled(t); ok && iv > 0 {
		v["funding_hourly"] = r / iv
	}
	return v
}

// Percentiles maps each value to its percentile (0–100, mid-rank for ties) within pool.
func Percentiles(pool []float64) func(float64) float64 {
	xs := append([]float64(nil), pool...)
	sort.Float64s(xs)
	n := float64(len(xs))
	return func(x float64) float64 {
		if n == 0 {
			return 50
		}
		lo := sort.SearchFloat64s(xs, x)
		hi := sort.Search(len(xs), func(i int) bool { return xs[i] > x })
		return (float64(lo) + float64(hi-lo)/2) / n * 100
	}
}

// hourlyCloses returns the 49 closes at h0−48h … h0, or nil if any is missing.
func hourlyCloses(s Series, h0 int64) []float64 {
	out := make([]float64, 0, 49)
	for k := int64(48); k >= 0; k-- {
		c, ok := s.Close(h0 - k*Hour)
		if !ok || c <= 0 {
			return nil
		}
		out = append(out, c)
	}
	return out
}

func logRets(cs []float64) []float64 {
	out := make([]float64, 0, len(cs))
	for i := 1; i < len(cs); i++ {
		out = append(out, math.Log(cs[i]/cs[i-1]))
	}
	return out
}

// ema of the series with span n (α = 2/(n+1)), seeded with the first value.
func ema(x []float64, n int) float64 {
	a := 2 / float64(n+1)
	e := x[0]
	for _, v := range x[1:] {
		e += a * (v - e)
	}
	return e
}

// slope is the OLS slope of y on x (beta); false when x has no variance.
func slope(x, y []float64) (float64, bool) {
	if len(x) != len(y) || len(x) < 2 {
		return 0, false
	}
	mx, my := 0.0, 0.0
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	cov, vx := 0.0, 0.0
	for i := range x {
		cov += (x[i] - mx) * (y[i] - my)
		vx += (x[i] - mx) * (x[i] - mx)
	}
	if vx < 1e-12 {
		return 0, false
	}
	return cov / vx, true
}

func stdev(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := 0.0
	for _, v := range x {
		m += v
	}
	m /= float64(len(x))
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	return math.Sqrt(s / float64(len(x)-1))
}

func median(x []float64) float64 {
	y := append([]float64(nil), x...)
	sort.Float64s(y)
	if len(y)%2 == 1 {
		return y[len(y)/2]
	}
	return (y[len(y)/2-1] + y[len(y)/2]) / 2
}
