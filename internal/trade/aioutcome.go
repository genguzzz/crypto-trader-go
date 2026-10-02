package trade

import (
	"fmt"
	"log"
)

// outcomeRecorder is implemented by reviewers that keep a decision log (internal/aigate); the
// outcome text feeds the AI's periodic reflection.
type outcomeRecorder interface {
	RecordOutcome(decisionID int64, result string) error
}

// recordAIOutcome attaches the result of a fully closed position to the AI decision that approved
// its latest opening signal, if any.
func (s *Service) recordAIOutcome(instanceID int64, instID string, f Fill, kind string) {
	_, _, rev := s.deps()
	rec, ok := rev.(outcomeRecorder)
	if !ok {
		return
	}
	var decisionID, openTs int64
	err := s.db.QueryRow(`SELECT ai_decision_id, ts FROM trade_signals WHERE instance_id=? AND inst_id=? AND outcome=?
		AND action IN (?, ?) AND ai_decision_id > 0 ORDER BY id DESC LIMIT 1`,
		instanceID, instID, OutOrdered, OpenLong, OpenShort).Scan(&decisionID, &openTs)
	if err != nil || decisionID == 0 {
		return
	}
	how := map[string]string{"stop": "止损", "target": "止盈", "liquidation": "强平"}[kind]
	if how == "" {
		how = "平仓"
	}
	hours := float64(f.Ts-openTs) / 3.6e6
	result := fmt.Sprintf("%s，实现 %+.2f USDT（未含手续费），持有 %.1f 小时", how, f.RealizedPnl, hours)
	go func() {
		if err := rec.RecordOutcome(decisionID, result); err != nil {
			log.Printf("trade: record AI outcome %d: %v", decisionID, err)
		}
	}()
}
