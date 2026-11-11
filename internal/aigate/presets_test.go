package aigate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPresets(t *testing.T) {
	e := newEnv(t, Options{})
	mux := http.NewServeMux()
	e.g.Register(mux)
	do := func(method, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/ai/presets", strings.NewReader(body)))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := do("GET", "")
	p := out["presets"].(map[string]any)
	coin := p["coin"].([]any)
	if code != 200 || out["custom"] != false || len(coin) != 5 {
		t.Fatalf("defaults: %d %v", code, out)
	}
	if last := coin[4].(map[string]any); !strings.Contains(last["label"].(string), "热榜") || !strings.Contains(last["prompt"].(string), "rev scan") {
		t.Fatalf("hot-list preset must be last: %v", last)
	}
	if pos := coin[3].(map[string]any); !strings.Contains(pos["prompt"].(string), "get_positions") {
		t.Fatalf("positions preset: %v", pos)
	}
	code, out = do("PUT", `{"coin":[{"label":" {sym} 能不能空？ ","prompt":"{sym} 能不能空？"}],"market":[{"label":"热榜","prompt":"从热榜找机会"}]}`)
	if code != 200 || out["custom"] != true {
		t.Fatalf("put: %d %v", code, out)
	}
	got, custom := e.g.Presets()
	if !custom || got.Coin[0].Label != "{sym} 能不能空？" || got.Coin[0].Prompt != "" || got.Market[0].Prompt != "从热榜找机会" {
		t.Fatalf("saved %+v", got)
	}
	for body, want := range map[string]string{
		`{"coin":[{"label":"x"}],"market":[]}`:                                                  "至少 2 个字",
		`{"coin":[],"market":[{"label":"` + strings.Repeat("长", 41) + `"}]}`:                    "最多 40 个字",
		`{"coin":[{"label":"ok ok","prompt":"` + strings.Repeat("长", 2001) + `"}],"market":[]}`: "最多 2000 个字",
		`{"coin":[{"label":"a1"},{"label":"a2"},{"label":"a3"},{"label":"a4"},{"label":"a5"},{"label":"a6"},{"label":"a7"},{"label":"a8"},{"label":"a9"}],"market":[]}`: "最多 8 条",
	} {
		if code, out := do("PUT", body); code != 400 || !strings.Contains(out["error"].(string), want) {
			t.Errorf("%s: %d %v", body[:20], code, out)
		}
	}
	if code, out = do("DELETE", ""); code != 200 || out["custom"] != false {
		t.Fatalf("reset: %d %v", code, out)
	}
}
