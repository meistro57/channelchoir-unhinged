package conductor

import (
	"context"
	"testing"
	"time"

	"github.com/meistro57/channelchoir-unhinged/internal/config"
	"github.com/meistro57/channelchoir-unhinged/internal/persona"
)

func testVoices() []*persona.Persona {
	return []*persona.Persona{
		{Name: "Troll", WebhookURL: "https://discord.com/api/webhooks/100/tok"},
		{Name: "OneWord", WebhookURL: "https://discord.com/api/webhooks/101/tok"},
	}
}

func TestOrchestrator_DisabledChambersSkipped(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			AllowedChannels: []string{"111"},
			AllowedGuilds:   []string{"999"},
		},
		Chambers: []config.ChamberConfig{
			{Name: "on", GuildID: "999", ChannelID: "111", Enabled: true, Personas: []string{"Troll"}},
			{Name: "off", GuildID: "999", ChannelID: "111", Enabled: false, Personas: []string{"OneWord"}},
		},
		Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100},
	}
	o, err := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if o.EnabledChamberCount() != 1 {
		t.Fatalf("expected 1 enabled chamber, got %d", o.EnabledChamberCount())
	}
	if o.chambers[0].chamberName != "on" {
		t.Fatalf("enabled chamber name = %q, want on", o.chambers[0].chamberName)
	}
}

func TestOrchestrator_BadHandoffSkipped(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			AllowedChannels: []string{"111"},
			AllowedGuilds:   []string{"999"},
		},
		Chambers: []config.ChamberConfig{
			{Name: "bad", GuildID: "000", ChannelID: "111", Enabled: true, Personas: []string{"Troll"}},
		},
		Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100},
	}
	o, err := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if o.EnabledChamberCount() != 0 {
		t.Fatalf("bad handoff should be skipped, got %d enabled", o.EnabledChamberCount())
	}
}

func TestOrchestrator_UnknownPersonaSkipped(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{AllowedChannels: []string{"111"}, AllowedGuilds: []string{"999"}},
		Chambers: []config.ChamberConfig{
			{Name: "bad", GuildID: "999", ChannelID: "111", Enabled: true, Personas: []string{"Ghost"}},
		},
		Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100},
	}
	o, _ := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	if o.EnabledChamberCount() != 0 {
		t.Fatalf("unknown persona should be skipped, got %d enabled", o.EnabledChamberCount())
	}
}

// Run with no enabled chambers blocks until ctx cancels, then returns cleanly.
func TestOrchestrator_NoChambersGracefulShutdown(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100}}
	o, _ := NewOrchestrator(cfg, testVoices(), nil, nil, true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := o.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("shutdown took too long: %v", time.Since(start))
	}
}
