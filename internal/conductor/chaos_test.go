package conductor

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
)

// Proves each chaos level maps to the documented behaviour knobs.
func TestChaosMapping(t *testing.T) {
	cases := []struct {
		level      int
		wantMin    int
		wantMax    int
		wantJitter float64
		wantDelay  [2]int
		wantThresh float64
		wantEager  float64
	}{
		{0, 1, 1, 0.2, [2]int{3, 8}, 0.6, 0.2},
		{1, 1, 2, 0.3, [2]int{2, 6}, 0.4, 0.4},
		{2, 1, 3, 0.5, [2]int{1, 4}, 0.2, 0.6},
		{3, 2, 4, 0.8, [2]int{0, 3}, 0.0, 0.8},
		{4, 3, 6, 1.2, [2]int{0, 1}, -0.5, 1.0},
		{5, -1, -1, 1.6, [2]int{0, 0}, -1.0, 1.0},
	}
	for _, tc := range cases {
		p := chaosFor(tc.level)
		if p.MinVoices != tc.wantMin || p.MaxVoices != tc.wantMax {
			t.Fatalf("level %d voices = %d..%d, want %d..%d", tc.level, p.MinVoices, p.MaxVoices, tc.wantMin, tc.wantMax)
		}
		if p.Jitter != tc.wantJitter {
			t.Fatalf("level %d jitter = %v, want %v", tc.level, p.Jitter, tc.wantJitter)
		}
		if p.MinDelay != tc.wantDelay[0] || p.MaxDelay != tc.wantDelay[1] {
			t.Fatalf("level %d delay = %d..%d, want %d..%d", tc.level, p.MinDelay, p.MaxDelay, tc.wantDelay[0], tc.wantDelay[1])
		}
		if p.Threshold != tc.wantThresh {
			t.Fatalf("level %d threshold = %v, want %v", tc.level, p.Threshold, tc.wantThresh)
		}
		if p.BotToBotEagerness != tc.wantEager {
			t.Fatalf("level %d eagerness = %v, want %v", tc.level, p.BotToBotEagerness, tc.wantEager)
		}
	}
}

// Proves chaos level changes voice count monotonically: polite = 1, unhinged = all.
func TestChaos_VoiceCountChangesWithLevel(t *testing.T) {
	c := newTestConductor(nil)
	last := line{speaker: "someone", text: "hey", origin: discordio.OriginHuman}

	setChaos(c, 0)
	if got := len(c.chooseMany(last)); got != 1 {
		t.Fatalf("chaos 0 -> %d voices, want 1", got)
	}
	setChaos(c, 5)
	if got := len(c.chooseMany(last)); got != len(unhingedVoices()) {
		t.Fatalf("chaos 5 -> %d voices, want %d", got, len(unhingedVoices()))
	}
}

// Proves chaos does NOT change the hard safety budget: the session manager is
// level-independent, so even chaos 5 cannot mint extra slots.
func TestChaos_DoesNotBypassHardBudget(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 2, MaxMessagesPerSession: 2}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)

	// Exhaust the budget regardless of any (nonexistent) chaos lever on the
	// manager — Reserve is the single gate and has no chaos dependency.
	for i := 0; i < 2; i++ {
		if _, err := sm.Reserve(time.Now()); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("chaos must not be able to bypass the hard budget")
	}
}

// voicesForTick clamps to available count.
func TestChaosParams_voicesForTickClamps(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	if n := (ChaosParams{MinVoices: 3, MaxVoices: 6}).voicesForTick(rng, 2); n != 2 {
		t.Fatalf("voicesForTick with available=2 returned %d, want 2", n)
	}
	if n := (ChaosParams{MinVoices: -1, MaxVoices: -1}).voicesForTick(rng, 5); n != 5 {
		t.Fatalf("all-voices mode with available=5 returned %d, want 5", n)
	}
}

// Proves the kill switch wins over chaos: a paused/hard-stopped choir never
// generates regardless of chaos level.
func TestChaos_KillSwitchAlwaysWins(t *testing.T) {
	dir := t.TempDir()
	kill := dir + "/kill"
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, KillSwitchPath: kill}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)

	// Kill switch present => Reserve fails at ANY chaos (manager knows no chaos).
	if err := os.WriteFile(kill, []byte("x"), 0644); err != nil {
		t.Fatalf("write kill switch: %v", err)
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("kill switch must block reserve")
	}
}
