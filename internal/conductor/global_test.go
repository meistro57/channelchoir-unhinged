package conductor

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
)

// The shared process-wide ceiling must hold even when multiple independent
// consumers (chambers) hammer Reserve concurrently — this is the cross-chamber
// isolation guarantee: chambers may not collectively exceed the global cap.
func TestGlobal_SharedCeilingAcrossConsumers(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 50, MaxMessagesPerMinute: 0}}
	g := NewGlobal(cfg)

	const consumers = 8
	const attempts = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0

	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := 0; a < attempts; a++ {
				if g.Reserve(time.Now()) == nil {
					mu.Lock()
					success++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if success != 50 {
		t.Fatalf("reserved %d global slots across consumers, want exactly 50 (shared cap)", success)
	}
	if g.PostedTotal() != 50 {
		t.Fatalf("PostedTotal=%d, want 50", g.PostedTotal())
	}
}

// A global kill switch blocks every consumer at once.
func TestGlobal_KillSwitchBlocksAllConsumers(t *testing.T) {
	dir := t.TempDir()
	kill := dir + "/kill"
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, KillSwitchPath: kill}}
	g := NewGlobal(cfg)

	if err := g.Reserve(time.Now()); err != nil {
		t.Fatalf("reserve before kill switch should work: %v", err)
	}

	if err := os.WriteFile(kill, []byte("x"), 0644); err != nil {
		t.Fatalf("write kill switch: %v", err)
	}
	if err := g.Reserve(time.Now()); err == nil {
		t.Fatalf("kill switch present must block a second consumer's reserve")
	}
}

// Per-minute window is shared, so two consumers collectively hitting it are
// bounded together, not independently.
func TestGlobal_SharedPerMinuteWindow(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxMessagesPerMinute: 3}}
	g := NewGlobal(cfg)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if err := g.Reserve(now); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := g.Reserve(now); err == nil {
		t.Fatalf("4th reserve in the shared minute window must be blocked")
	}
}
