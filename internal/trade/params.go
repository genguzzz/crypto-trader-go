package trade

import (
	"fmt"
	"math"
)

// validParams fills defaults and checks every value against its spec. Values are numbers on the
// wire: bool 0/1, choice and timeframe the option index.
func validParams(specs []ParamSpec, in map[string]float64) (map[string]float64, error) {
	out := map[string]float64{}
	known := map[string]bool{}
	for _, p := range specs {
		known[p.Name] = true
		v, ok := in[p.Name]
		if !ok {
			v = p.Default
		}
		if err := checkParam(p, v); err != nil {
			return nil, err
		}
		out[p.Name] = v
	}
	for k := range in {
		if !known[k] {
			return nil, errBad(fmt.Sprintf("未知参数 %q", k))
		}
	}
	return out, nil
}

func checkParam(p ParamSpec, v float64) error {
	bad := func(why string) error { return errBad(fmt.Sprintf("参数 %s=%v %s", p.Name, v, why)) }
	if !finite(v) {
		return bad("不是有效数字")
	}
	switch p.Type {
	case PTBool:
		if v != 0 && v != 1 {
			return bad("需为 0 或 1")
		}
		return nil
	case PTChoice, PTTimeframe:
		if v != math.Trunc(v) || v < 0 || int(v) >= len(p.Options) {
			return bad("不是有效选项")
		}
		return nil
	case PTInt:
		if v != math.Trunc(v) {
			return bad("需为整数")
		}
	}
	if (p.Min != nil && v < *p.Min) || (p.Max != nil && v > *p.Max) {
		return bad("超出范围")
	}
	return nil
}

// InstanceTimeframe is the bar timeframe an instance runs on: a timeframe param overrides the
// strategy's declared one.
func InstanceTimeframe(inst Instance, ver StrategyVersion) string {
	for _, p := range ver.Params {
		if p.Type != PTTimeframe {
			continue
		}
		v, ok := inst.Params[p.Name]
		if !ok {
			v = p.Default
		}
		if i := int(v); i >= 0 && i < len(p.Options) {
			if _, ok := TFMillis(p.Options[i]); ok {
				return p.Options[i]
			}
		}
	}
	return ver.Timeframe
}
