package trade

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Register mounts the /api/v1/trade/* endpoints (docs/trading-design.md §4).
func (s *Service) Register(mux *http.ServeMux) {
	const p = "/api/v1/trade"
	mux.HandleFunc("GET "+p+"/accounts", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Accounts()) })
	mux.HandleFunc("POST "+p+"/accounts", func(w http.ResponseWriter, r *http.Request) {
		var in AccountInput
		if !readJSON(w, r, &in) {
			return
		}
		reply(w, http.StatusCreated)(s.CreateAccount(in))
	})
	mux.HandleFunc("GET "+p+"/accounts/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.AccountDetail(id))
	}))
	mux.HandleFunc("PATCH "+p+"/accounts/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in AccountPatch
		if readJSON(w, r, &in) {
			reply(w, 200)(s.UpdateAccount(id, in))
		}
	}))
	mux.HandleFunc("POST "+p+"/accounts/{id}/deposit", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in struct {
			Amount float64 `json:"amount"`
		}
		if readJSON(w, r, &in) {
			reply(w, 200)(s.Deposit(id, in.Amount))
		}
	}))
	mux.HandleFunc("GET "+p+"/accounts/{id}/equity", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		from, err1 := queryInt(r, "from")
		to, err2 := queryInt(r, "to")
		if err := errors.Join(err1, err2); err != nil {
			httpErr(w, 400, "from/to 需为毫秒时间戳")
			return
		}
		reply(w, 200)(s.Equity(id, from, to))
	}))
	list := func(fn func(int64, Page) (any, error)) http.HandlerFunc {
		return s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
			limit, err1 := queryInt(r, "limit")
			before, err2 := queryInt(r, "before")
			if err := errors.Join(err1, err2); err != nil || limit < 0 || before < 0 {
				httpErr(w, 400, "limit/before 需为非负整数")
				return
			}
			reply(w, 200)(fn(id, Page{Limit: int(limit), Before: before}))
		})
	}
	mux.HandleFunc("GET "+p+"/accounts/{id}/fills", list(func(id int64, pg Page) (any, error) { return s.Fills(id, pg) }))
	mux.HandleFunc("GET "+p+"/accounts/{id}/orders", list(func(id int64, pg Page) (any, error) { return s.Orders(id, pg) }))
	mux.HandleFunc("GET "+p+"/accounts/{id}/signals", list(func(id int64, pg Page) (any, error) { return s.Signals(id, pg) }))
	mux.HandleFunc("GET "+p+"/accounts/{id}/ledger", list(func(id int64, pg Page) (any, error) { return s.Ledger(id, pg) }))
	mux.HandleFunc("POST "+p+"/accounts/{id}/close", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in struct {
			InstID string `json:"instId"`
		}
		if !readJSONOptional(w, r, &in) {
			return
		}
		n, err := s.ClosePositions(r.Context(), id, strings.ToUpper(strings.TrimSpace(in.InstID)))
		reply(w, 200)(map[string]int{"closed": n}, err)
	}))
	mux.HandleFunc("GET "+p+"/accounts/{id}/trades", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.Trades(id))
	}))
	mux.HandleFunc("GET "+p+"/accounts/{id}/candles", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		inst := r.URL.Query().Get("instId")
		if inst == "" {
			reply(w, 200)(nil, errBad("instId required"))
			return
		}
		reply(w, 200)(s.AccountCandles(id, inst, r.URL.Query().Get("bar")))
	}))
	mux.HandleFunc("GET "+p+"/accounts/{id}/report", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.Report(id))
	}))
	mux.HandleFunc("POST "+p+"/backtests/{id}/run", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		if err := s.RunBacktest(s.baseCtx(), id); err != nil {
			httpErr(w, statusOf(err), err.Error())
			return
		}
		reply(w, http.StatusAccepted)(s.Account(id))
	}))
	mux.HandleFunc("POST "+p+"/backtests/{id}/cancel", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		if err := s.CancelBacktest(id); err != nil {
			httpErr(w, statusOf(err), err.Error())
			return
		}
		reply(w, 200)(s.Account(id))
	}))
	mux.HandleFunc("GET "+p+"/strategies", func(w http.ResponseWriter, r *http.Request) { reply(w, 200)(s.Strategies()) })
	mux.HandleFunc("GET "+p+"/strategies/{versionId}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200)(s.StrategyVersion(r.PathValue("versionId")))
	})
	mux.HandleFunc("POST "+p+"/strategies/reload", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.ReloadStrategies(r.Context())
		if err != nil {
			httpErr(w, statusOf(err), err.Error())
			return
		}
		list, err := s.Strategies()
		reply(w, 200)(map[string]any{"added": n, "strategies": list}, err)
	})
	mux.HandleFunc("GET "+p+"/sources", func(w http.ResponseWriter, r *http.Request) { reply(w, 200)(s.Sources()) })
	mux.HandleFunc("POST "+p+"/sources", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code        string `json:"code"`
			FromVersion string `json:"fromVersion"`
		}
		if readJSONMax(w, r, &in, maxSourceBytes+4096) {
			reply(w, http.StatusCreated)(s.CreateSource(in.Code, in.FromVersion))
		}
	})
	mux.HandleFunc("GET "+p+"/sources/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.Source(id))
	}))
	mux.HandleFunc("PUT "+p+"/sources/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in struct {
			Code string `json:"code"`
		}
		if readJSONMax(w, r, &in, maxSourceBytes+4096) {
			reply(w, 200)(s.SaveSource(id, in.Code))
		}
	}))
	mux.HandleFunc("DELETE "+p+"/sources/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(map[string]bool{"ok": true}, s.DeleteSource(id))
	}))
	mux.HandleFunc("POST "+p+"/sources/{id}/publish", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		res, err := s.PublishSource(r.Context(), id)
		if err != nil && res.Check.Diagnostics != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(statusOf(err))
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "check": res.Check})
			return
		}
		reply(w, 200)(res, err)
	}))
	mux.HandleFunc("POST "+p+"/check", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code string `json:"code"`
		}
		if readJSONMax(w, r, &in, maxSourceBytes+4096) {
			reply(w, 200)(s.Check(r.Context(), in.Code))
		}
	})
	mux.HandleFunc("POST "+p+"/trial", func(w http.ResponseWriter, r *http.Request) {
		var in TrialInput
		if readJSONMax(w, r, &in, maxSourceBytes+64<<10) {
			reply(w, 200)(s.Trial(r.Context(), in))
		}
	})
	mux.HandleFunc("GET "+p+"/instances/{id}/logs", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		writeJSON(w, s.RunLog(id))
	}))
	mux.HandleFunc("GET "+p+"/accounts/{id}/instances", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.Instances(id))
	}))
	mux.HandleFunc("POST "+p+"/accounts/{id}/instances", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in InstanceInput
		if readJSON(w, r, &in) {
			reply(w, http.StatusCreated)(s.CreateInstance(id, in))
		}
	}))
	mux.HandleFunc("PATCH "+p+"/instances/{id}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		var in InstancePatch
		if readJSON(w, r, &in) {
			reply(w, 200)(s.UpdateInstance(id, in))
		}
	}))
	mux.HandleFunc("POST "+p+"/instances/{id}/{action}", s.withID(func(w http.ResponseWriter, r *http.Request, id int64) {
		reply(w, 200)(s.InstanceAction(id, r.PathValue("action")))
	}))
}

func (s *Service) withID(fn func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			httpErr(w, 400, "invalid id")
			return
		}
		fn(w, r, id)
	}
}

func queryInt(r *http.Request, k string) (int64, error) {
	v := r.URL.Query().Get(k)
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// reply writes v with the status code, or the error with its status.
func reply(w http.ResponseWriter, code int) func(any, error) {
	return func(v any, err error) {
		if err != nil {
			httpErr(w, statusOf(err), err.Error())
			return
		}
		if code != 200 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(v)
			return
		}
		writeJSON(w, v)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONMax(w, r, v, 64<<10)
}

func readJSONMax(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// readJSONOptional accepts an empty body.
func readJSONOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
