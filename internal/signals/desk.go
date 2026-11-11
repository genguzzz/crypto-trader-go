package signals

import "time"

// Desk subscriptions are the trading desk's standing scans (internal/autopilot): the owner's
// configuration rather than something an agent waits for. They never expire or run out of fires, do
// not count towards the caps, and a firing is handed to the desk (DeskSink) instead of waking a chat:
// the desk collects fires, paces its runs and writes its own message.

const (
	deskExpiry   = 100 * 365 * 24 * time.Hour
	deskMaxFires = 1 << 30
)

// DeskSink receives every fire of a desk subscription (called from the evaluation loop: must not block).
type DeskSink func(d Delivery, sub Subscription)

// SetDesk wires the trading desk's inbox.
func (s *Service) SetDesk(f DeskSink) { s.mu.Lock(); s.desk = f; s.mu.Unlock() }

// DeskSubs are the active desk subscriptions with their progress, by strategy key (Label).
func (s *Service) DeskSubs() (map[string]Subscription, error) {
	subs, err := s.query(`status='active' AND kind='desk'`, nil, 200)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Subscription, len(subs))
	for _, x := range subs {
		if _, dup := out[x.Label]; !dup { // newest first: an older duplicate is left for the caller to cancel
			out[x.Label] = x
		}
	}
	return out, nil
}
