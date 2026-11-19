package conductor

import (
	"testing"

	"github.com/meistro57/channelchoir-unhinged/internal/config"
)

// channelIDFor must return the chamber-specific channel ID in multi-chamber
// mode, so each worker polls only its assigned channel — never the shared
// cfg.Discord.ChannelID.
func TestConductor_ChannelIDFor_UsesChamberID(t *testing.T) {
	cfg := &config.Config{Discord: config.DiscordConfig{ChannelID: "global-channel"}}
	c := newTestConductor(cfg)
	c.chamberID = "chamber-a-channel"

	if got := c.channelIDFor(); got != "chamber-a-channel" {
		t.Fatalf("channelIDFor() = %q, want chamber-a-channel", got)
	}
}

// A standalone conductor (no chamber) falls back to the config channel ID.
func TestConductor_ChannelIDFor_FallsBackToConfig(t *testing.T) {
	cfg := &config.Config{Discord: config.DiscordConfig{ChannelID: "global-channel"}}
	c := newTestConductor(cfg)
	if got := c.channelIDFor(); got != "global-channel" {
		t.Fatalf("channelIDFor() = %q, want global-channel", got)
	}
}

// Two chambers get distinct channel IDs and each conductor routes to its own.
func TestOrchestrator_ChambersHaveDistinctChannelIDs(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			ChannelID:       "global-channel",
			AllowedChannels: []string{"111", "222"},
			AllowedGuilds:   []string{"999"},
		},
		Chambers: []config.ChamberConfig{
			{Name: "A", GuildID: "999", ChannelID: "111", Enabled: true, Personas: []string{"Troll"}},
			{Name: "B", GuildID: "999", ChannelID: "222", Enabled: true, Personas: []string{"OneWord"}},
		},
		Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100},
	}
	o, err := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(o.chambers) != 2 {
		t.Fatalf("expected 2 chambers, got %d", len(o.chambers))
	}
	if o.chambers[0].channelIDFor() != "111" {
		t.Fatalf("chamber A channel = %q, want 111", o.chambers[0].channelIDFor())
	}
	if o.chambers[1].channelIDFor() != "222" {
		t.Fatalf("chamber B channel = %q, want 222", o.chambers[1].channelIDFor())
	}
	if o.chambers[0].channelIDFor() == o.chambers[1].channelIDFor() {
		t.Fatalf("chambers must have distinct channel IDs")
	}
}

// Per-chamber mode and session budget are threaded through to the conductor.
func TestOrchestrator_PerChamberModeAndBudgetThreaded(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			AllowedChannels: []string{"111"},
			AllowedGuilds:   []string{"999"},
		},
		Conductor: config.ConductorConfig{Mode: "autonomous"},
		Chambers: []config.ChamberConfig{
			{Name: "gated", GuildID: "999", ChannelID: "111", Enabled: true, Mode: "human_gated", MaxMessagesPerSession: 7, Personas: []string{"Troll"}},
		},
		Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100},
	}
	o, err := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if len(o.chambers) != 1 {
		t.Fatalf("expected 1 chamber, got %d", len(o.chambers))
	}
	c := o.chambers[0]
	// modeFor must reflect the per-chamber human_gated, not the global autonomous.
	if got := c.modeFor(); got != "human_gated" {
		t.Fatalf("modeFor() = %q, want human_gated", got)
	}
}
