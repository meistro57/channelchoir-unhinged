package config

import "testing"

// A valid unhinged config with the allowlist set passes validation.
func TestValidateAcceptAllowlistedChannel(t *testing.T) {
	c := Config{
		Discord: DiscordConfig{
			BotToken:        "tok",
			ChannelID:       "111",
			AllowedChannels: []string{"111"},
		},
		Conductor: ConductorConfig{
			MinDelaySeconds: 0,
			MaxDelaySeconds: 1,
			MaxVerse:        -1,
		},
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateRejectsEmptyAllowlist(t *testing.T) {
	c := Config{
		Discord: DiscordConfig{BotToken: "tok", ChannelID: "111"},
		Conductor: ConductorConfig{
			MinDelaySeconds: 0, MaxDelaySeconds: 1,
		},
	}
	c.applyDefaults()
	if err := c.validate(); err == nil {
		t.Fatalf("expected empty allowlist to be rejected")
	}
}

func TestValidateRejectsChannelOutsideAllowlist(t *testing.T) {
	c := Config{
		Discord: DiscordConfig{
			BotToken:        "tok",
			ChannelID:       "222",
			AllowedChannels: []string{"111"},
		},
		Conductor: ConductorConfig{
			MinDelaySeconds: 0, MaxDelaySeconds: 1,
		},
	}
	c.applyDefaults()
	if err := c.validate(); err == nil {
		t.Fatalf("expected channel outside allowlist to be rejected")
	}
}

func TestDefaultsNeverDisableSafeguards(t *testing.T) {
	c := Config{}
	c.applyDefaults()
	if c.Safeguards.MaxMessagesPerRun == 0 {
		t.Fatalf("max_messages_per_run must default to a hard non-zero value")
	}
	if c.Safeguards.MaxRuntimeSeconds == 0 {
		t.Fatalf("max_runtime_seconds must default to a hard non-zero value")
	}
}

func TestValidateRejectsGuildOutsideAllowlist(t *testing.T) {
	c := Config{
		Discord: DiscordConfig{
			BotToken:        "tok",
			ChannelID:       "111",
			GuildID:         "999",
			AllowedChannels: []string{"111"},
			AllowedGuilds:   []string{"111"},
		},
		Conductor: ConductorConfig{MinDelaySeconds: 0, MaxDelaySeconds: 1},
	}
	c.applyDefaults()
	if err := c.validate(); err == nil {
		t.Fatalf("expected guild outside allowlist to be rejected")
	}
}

func TestValidateAcceptsGuildInAllowlist(t *testing.T) {
	c := Config{
		Discord: DiscordConfig{
			BotToken:        "tok",
			ChannelID:       "111",
			GuildID:         "111",
			AllowedChannels: []string{"111"},
			AllowedGuilds:   []string{"111"},
		},
		Conductor: ConductorConfig{MinDelaySeconds: 0, MaxDelaySeconds: 1},
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		t.Fatalf("expected guild in allowlist to be accepted: %v", err)
	}
}
