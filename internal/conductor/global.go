package conductor

import (
	"fmt"
	"sync"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
)

// Global is the PROCESS-WIDE safety envelope shared by every chamber. It owns
// the only counters that must not be per-chamber: the total reserved messages
// this run (postedTotal) and the rolling per-minute window, plus the kill
// switch. Per-chamber limits are ADDITIONAL restrictions stacked on top of
// this — a chamber can be stricter than the global ceiling, never looser.
type Global struct {
	mu  sync.Mutex
	cfg *config.Config

	postedTotal  int
	minuteWindow []time.Time
}

func NewGlobal(cfg *config.Config) *Global {
	return &Global{cfg: cfg}
}

// Reserve atomically checks the kill switch, the process-wide message cap, and
// the process-wide per-minute window, and — if all pass — consumes one global
// slot. It is the ceiling that no per-chamber setting can bypass.
func (g *Global) Reserve(now time.Time) error {
	if g.cfg.Safeguards.KillSwitchPath != "" && fileExists(g.cfg.Safeguards.KillSwitchPath) {
		return fmt.Errorf("kill switch %q present", g.cfg.Safeguards.KillSwitchPath)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.cfg.Safeguards.MaxMessagesPerRun > 0 && g.postedTotal >= g.cfg.Safeguards.MaxMessagesPerRun {
		return fmt.Errorf("hard run message cap reached (%d)", g.cfg.Safeguards.MaxMessagesPerRun)
	}
	if g.cfg.Safeguards.MaxMessagesPerMinute > 0 {
		cutoff := now.Add(-time.Minute)
		kept := g.minuteWindow[:0]
		for _, t := range g.minuteWindow {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		g.minuteWindow = kept
		if len(kept) >= g.cfg.Safeguards.MaxMessagesPerMinute {
			return fmt.Errorf("per-minute cap reached (%d/min)", g.cfg.Safeguards.MaxMessagesPerMinute)
		}
	}

	g.postedTotal++
	g.minuteWindow = append(g.minuteWindow, now)
	return nil
}

// CanPost probes the global ceiling without consuming a slot.
func (g *Global) CanPost(now time.Time) error {
	if g.cfg.Safeguards.KillSwitchPath != "" && fileExists(g.cfg.Safeguards.KillSwitchPath) {
		return fmt.Errorf("kill switch %q present", g.cfg.Safeguards.KillSwitchPath)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cfg.Safeguards.MaxMessagesPerRun > 0 && g.postedTotal >= g.cfg.Safeguards.MaxMessagesPerRun {
		return fmt.Errorf("hard run message cap reached (%d)", g.cfg.Safeguards.MaxMessagesPerRun)
	}
	if g.cfg.Safeguards.MaxMessagesPerMinute > 0 {
		cutoff := now.Add(-time.Minute)
		kept := g.minuteWindow[:0]
		for _, t := range g.minuteWindow {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		g.minuteWindow = kept
		if len(kept) >= g.cfg.Safeguards.MaxMessagesPerMinute {
			return fmt.Errorf("per-minute cap reached (%d/min)", g.cfg.Safeguards.MaxMessagesPerMinute)
		}
	}
	return nil
}

// PostedTotal reports how many global slots have been reserved this run.
func (g *Global) PostedTotal() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.postedTotal
}
