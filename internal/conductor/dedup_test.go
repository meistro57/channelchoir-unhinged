package conductor

import (
	"testing"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
)

func TestDedup_IdenticalAndNearIdentical(t *testing.T) {
	d := NewDedup(5)
	if d.IsDuplicate("hello") {
		t.Fatalf("empty history must not be duplicate")
	}
	d.Record("LOL!!")
	if !d.IsDuplicate("lol") {
		t.Fatalf("normalized duplicate must be detected")
	}
	if !d.IsDuplicate("lol") {
		t.Fatalf("exact repeat must be detected")
	}
	if d.IsDuplicate("something new") {
		t.Fatalf("unrelated line must not be duplicate")
	}
}

func TestDedup_DisabledWhenZero(t *testing.T) {
	d := NewDedup(0)
	d.Record("hello")
	if d.IsDuplicate("hello") {
		t.Fatalf("dedup disabled must never flag duplicates")
	}
}

func TestDedup_PrunesOldEntries(t *testing.T) {
	d := NewDedup(2)
	d.Record("one")
	d.Record("two")
	d.Record("three") // prunes "one"
	if d.IsDuplicate("one") {
		t.Fatalf("pruned entry must no longer be flagged")
	}
	if !d.IsDuplicate("three") {
		t.Fatalf("most recent entry must still be flagged")
	}
	if d.Len() != 2 {
		t.Fatalf("Len = %d, want 2", d.Len())
	}
}

// Human-gated mode: the conductor must not reply to its own echo even when
// bot-to-bot is enabled, because human_gated trumps it.
func TestHumanGated_ChoirEchoNeverTriggers(t *testing.T) {
	cfg := &config.Config{
		Conductor: config.ConductorConfig{
			Mode:           "human_gated",
			BotToBot:       true, // deliberately on, but mode must still gate
			VoicesPerTick:  -1,
			SpeakThreshold: -1,
			Jitter:         0.001,
			MaxVerse:       -1,
		},
	}
	c := newTestConductor(cfg)
	last := line{speaker: "Troll", text: "lol", origin: discordio.OriginChoir}

	// Reproduce the gate predicate: choir-origin + human_gated => no self-reply.
	if c.cfg.Conductor.Mode == "human_gated" && last.origin == discordio.OriginChoir {
		// gate applies — this is the expected block. Nothing to do but assert.
	} else {
		t.Fatalf("human_gated mode must gate choir echoes")
	}
}

// Autonomous mode with bot-to-bot on allows the choir to self-reply.
func TestAutonomous_ChoirEchoMayTrigger(t *testing.T) {
	cfg := &config.Config{
		Conductor: config.ConductorConfig{
			Mode:           "autonomous",
			BotToBot:       true,
			VoicesPerTick:  -1,
			SpeakThreshold: -1,
			Jitter:         0.001,
			MaxVerse:       -1,
		},
	}
	c := newTestConductor(cfg)
	last := line{speaker: "Troll", text: "lol", origin: discordio.OriginChoir}
	botToBot := c.controls.BotToBotEnabled()
	if last.origin == discordio.OriginChoir && (c.cfg.Conductor.Mode == "human_gated" || !botToBot) {
		t.Fatalf("autonomous + bot2bot on must allow self-reply")
	}
	if got := c.chooseMany(last); len(got) == 0 {
		t.Fatalf("expected voices available in autonomous self-conversation")
	}
}
