// filename: internal/config/config.go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole machine setup sheet. Anything secret should be written
// in choir.yaml as ${ENV_VAR} — it gets expanded from the environment on load,
// so no tokens ever live in the repo.
type Config struct {
	Discord    DiscordConfig    `yaml:"discord"`
	Ollama     OllamaConfig     `yaml:"ollama"`
	Conductor  ConductorConfig  `yaml:"conductor"`
	Safeguards SafeguardsConfig `yaml:"safeguards"`
	Controls   ControlsConfig   `yaml:"controls"`
}

type DiscordConfig struct {
	// BotToken is only used to READ the channel. The voices post through
	// webhooks, which is how they get their own names and avatars.
	BotToken    string `yaml:"bot_token"`
	GuildID     string `yaml:"guild_id"`
	ChannelID   string `yaml:"channel_id"`
	PollSeconds int    `yaml:"poll_seconds"`

	// AllowedChannels is the set of channel IDs the choir is PERMITTED to post
	// in. UNHINGED refuses to START unless this is explicitly set — chaos must
	// be scoped to a known disposable test cell, never the wider guild.
	AllowedChannels []string `yaml:"allowed_channels"`

	// AllowedGuilds is the set of guild IDs the choir may operate in. Empty
	// means "any guild" (kept for dry-run/mocked tests); live deployment
	// should always pin this to the lab guild.
	AllowedGuilds []string `yaml:"allowed_guilds"`
}

type OllamaConfig struct {
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	NumPredict  int     `yaml:"num_predict"`
	TimeoutSecs int     `yaml:"timeout_seconds"`
}

type ConductorConfig struct {
	PersonaDir string `yaml:"persona_dir"`

	// TranscriptSize is how many recent messages the voices can see.
	TranscriptSize int `yaml:"transcript_size"`

	// MaxVerse is the hard cap on consecutive bot-only messages. <= 0 means
	// "infinite" (UNHINGED removes the cap by setting this negative).
	MaxVerse int `yaml:"max_verse"`

	// RestSeconds is the enforced silence after a verse ends.
	RestSeconds int `yaml:"rest_seconds"`

	// CooldownTurns is how many turns a voice must sit out after speaking.
	CooldownTurns int `yaml:"cooldown_turns"`

	// Delay range before a chosen voice actually posts.
	MinDelaySeconds int `yaml:"min_delay_seconds"`
	MaxDelaySeconds int `yaml:"max_delay_seconds"`

	// Scoring weights.
	TriggerWeight      float64 `yaml:"trigger_weight"`
	DirectAddressBoost float64 `yaml:"direct_address_boost"`
	Jitter             float64 `yaml:"jitter"`

	// SpeakThreshold is the minimum score required to speak at all. <= 0 means
	// "someone always answers".
	SpeakThreshold float64 `yaml:"speak_threshold"`

	// VoicesPerTick is how many voices speak each poll tick instead of one
	// single winner. <= 0 means "all eligible voices" (machine-gun).
	VoicesPerTick int `yaml:"voices_per_tick"`

	// BotToBot enables choir-generated messages to trigger further replies.
	// OFF means the choir never replies to its own webhook echoes. ON enables
	// intentional self-conversation, always bounded by the session budget.
	BotToBot bool `yaml:"bot_to_bot"`

	// Mode selects the operating posture:
	//   "autonomous" — choir messages may chain replies until limits stop them.
	//   "human_gated" — once a conversation segment ends, a genuine human
	//                  message is required before the choir continues.
	// Defaults to "autonomous". Neither mode can override process limits.
	Mode string `yaml:"mode"`

	// RecentResponses is the size of the bounded dedup history used to stop
	// identical/near-identical lines from circulating between personas.
	// <= 0 disables dedup. Default 0 (off).
	RecentResponses int `yaml:"recent_responses"`

	// Burst configures rate-limit stress mode.
	Burst BurstConfig `yaml:"burst"`
}

// BurstConfig is UNHINGED's rate-limit stress mode.
type BurstConfig struct {
	Enabled      bool `yaml:"enabled"`
	Count        int  `yaml:"count"`         // messages to dump in one burst
	WindowMillis int  `yaml:"window_millis"` // tight window the burst fits in
}

// SafeguardsConfig is the NON-negotiable safety layer. UNHINGED = unpredictable
// conversation, NOT uncontrolled infrastructure. Hard limits default ON and are
// never silently disabled.
type SafeguardsConfig struct {
	// MaxMessagesPerRun is the hard global cap on total messages posted before
	// the choir halts itself. Default 500 if unset (never "off").
	MaxMessagesPerRun int `yaml:"max_messages_per_run"`

	// MaxMessagesPerMinute caps the post rate inside the session window.
	// Default 0 = no per-minute cap (the run-level cap still holds).
	MaxMessagesPerMinute int `yaml:"max_messages_per_minute"`

	// MaxMessagesPerSession caps generated messages within one session.
	// Resets when a human starts a new session. Default 0 = inherit the
	// run-level cap (max_messages_per_run).
	MaxMessagesPerSession int `yaml:"max_messages_per_session"`

	// MaxRuntimeSeconds is the hard cap on how long a run lives before it halts.
	// Default 3600 (1 hour) if unset (never "off").
	MaxRuntimeSeconds int `yaml:"max_runtime_seconds"`

	// KillSwitchPath is a file path. While the file EXISTS the choir will not
	// post. Create it to kill output instantly from outside the process.
	KillSwitchPath string `yaml:"kill_switch_path"`
}

// ControlsConfig governs the !choir control interface. Commands that touch
// safety settings are restricted to Operators.
type ControlsConfig struct {
	// Enabled turns the !choir command interface on. Default false.
	Enabled bool `yaml:"enabled"`

	// Prefix is the command prefix. Default "!choir".
	Prefix string `yaml:"prefix"`

	// Operators is the allowlist of Discord user IDs permitted to run control
	// commands. Empty = nobody (commands disabled, safety-safe).
	Operators []string `yaml:"operators"`

	// ControlWebhookURL is a DEDICATED webhook (never a persona webhook) that
	// surfaces [CHOIR ...] control/status/error notices to operators. Empty =
	// control replies are log-only.
	ControlWebhookURL string `yaml:"control_webhook_url"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(raw))

	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	c.applyDefaults()

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Discord.PollSeconds == 0 {
		c.Discord.PollSeconds = 4
	}
	if c.Ollama.BaseURL == "" {
		c.Ollama.BaseURL = "http://localhost:11434"
	}
	if c.Ollama.Model == "" {
		c.Ollama.Model = "qwen3:30b-a3b"
	}
	if c.Ollama.Temperature == 0 {
		c.Ollama.Temperature = 0.9
	}
	if c.Ollama.NumPredict == 0 {
		c.Ollama.NumPredict = 160
	}
	if c.Ollama.TimeoutSecs == 0 {
		c.Ollama.TimeoutSecs = 180
	}
	if c.Conductor.PersonaDir == "" {
		c.Conductor.PersonaDir = "personas"
	}
	if c.Conductor.TranscriptSize == 0 {
		c.Conductor.TranscriptSize = 20
	}
	// UNHINGED: damping dials are NOT force-restored to upstream defaults.
	// 0 means "off/infinite" (max_verse 0 = infinite, rest 0 = never rest,
	// cooldown 0 = no sit-out) as the conductor interprets. The real protection
	// is the safeguards block below, which defaults ON and is never disabled.
	// Delay defaults apply ONLY when the whole range is unset (both zero), so an
	// explicit min_delay 0 (unhinged near-zero) is preserved, not overwritten.
	if c.Conductor.MinDelaySeconds == 0 && c.Conductor.MaxDelaySeconds == 0 {
		c.Conductor.MinDelaySeconds = 3
		c.Conductor.MaxDelaySeconds = 12
	}
	if c.Conductor.TriggerWeight == 0 {
		c.Conductor.TriggerWeight = 0.35
	}
	if c.Conductor.DirectAddressBoost == 0 {
		c.Conductor.DirectAddressBoost = 2.0
	}
	if c.Conductor.Jitter == 0 {
		c.Conductor.Jitter = 0.4
	}
	if c.Conductor.Burst.Count == 0 {
		c.Conductor.Burst.Count = 10
	}
	if c.Conductor.Burst.WindowMillis == 0 {
		c.Conductor.Burst.WindowMillis = 1000
	}
	if c.Safeguards.MaxMessagesPerRun == 0 {
		c.Safeguards.MaxMessagesPerRun = 500
	}
	if c.Safeguards.MaxRuntimeSeconds == 0 {
		c.Safeguards.MaxRuntimeSeconds = 3600
	}
	if c.Conductor.Mode == "" {
		c.Conductor.Mode = "autonomous"
	}
	if c.Controls.Prefix == "" {
		c.Controls.Prefix = "!choir"
	}
	if c.Safeguards.KillSwitchPath == "" {
		c.Safeguards.KillSwitchPath = "kill-switch" // relative to CWD
	}
	// SpeakThreshold 0 = "always answer" (upstream posture). VoicesPerTick 0 =
	// "all eligible" (unhinged machine-gun).
}

func (c *Config) validate() error {
	if c.Discord.BotToken == "" {
		return fmt.Errorf("discord.bot_token is empty (set DISCORD_BOT_TOKEN in your environment)")
	}
	if c.Discord.ChannelID == "" {
		return fmt.Errorf("discord.channel_id is empty")
	}
	if len(c.Discord.AllowedChannels) == 0 {
		return fmt.Errorf("discord.allowed_channels is empty — UNHINGED refuses to run without an explicit test-channel allowlist")
	}
	if !contains(c.Discord.AllowedChannels, c.Discord.ChannelID) {
		return fmt.Errorf("discord.channel_id %q is not in discord.allowed_channels %v — post target must be an allowlisted test channel", c.Discord.ChannelID, c.Discord.AllowedChannels)
	}
	if c.Conductor.MaxDelaySeconds < c.Conductor.MinDelaySeconds {
		return fmt.Errorf("conductor.max_delay_seconds must be >= min_delay_seconds")
	}
	if c.Discord.GuildID != "" && len(c.Discord.AllowedGuilds) > 0 && !contains(c.Discord.AllowedGuilds, c.Discord.GuildID) {
		return fmt.Errorf("discord.guild_id %q is not in discord.allowed_guilds %v", c.Discord.GuildID, c.Discord.AllowedGuilds)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (c *OllamaConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSecs) * time.Second
}
