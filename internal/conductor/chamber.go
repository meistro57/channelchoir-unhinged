package conductor

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/persona"
)

// Chamber is one isolated test chamber (a Discord channel) with its own
// transcript, session budget, dedup history, chaos level, and persona roster.
// It stacks its per-chamber limits on top of the shared process-wide Global.
// A chamber is Enabled=false until Mark approves live activation.
type Chamber struct {
	// identity
	Name      string
	GuildID   string
	ChannelID string
	Enabled   bool
	Mode      string // "autonomous" | "human_gated"

	// chaos (0..5)
	chaosMu sync.RWMutex
	chaos   int

	// roster + webhooks
	voices []*persona.Persona
	choir  *discordio.ChoirWebhooks

	// per-chamber state
	sessions *SessionManager
	dedup    *Dedup
	controls *Controls

	// score/rng
	rng *rand.Rand

	transcript    []line
	turn          int
	lastSpoke     map[string]int
	verse         int
	lastMessageID string
}

// NewChamber builds a chamber from its config, resolving its persona roster
// against the provided persona names (resolved earlier). The shared Global is
// the process-wide ceiling every chamber stacks upon.
func NewChamber(cc config.ChamberConfig, allPersonas map[string]*persona.Persona, g *Global, cfg *config.Config) (*Chamber, error) {
	// Resolve roster.
	voices := make([]*persona.Persona, 0, len(cc.Personas))
	seen := map[string]bool{}
	for _, name := range cc.Personas {
		p, ok := allPersonas[name]
		if !ok {
			return nil, fmt.Errorf("chamber %q: persona %q not found", cc.Name, name)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("chamber %q: duplicate persona %q", cc.Name, name)
		}
		seen[p.Name] = true
		voices = append(voices, p)
	}

	mode := cc.Mode
	if mode == "" {
		mode = cfg.Conductor.Mode
	}

	ch := &Chamber{
		Name:      cc.Name,
		GuildID:   cc.GuildID,
		ChannelID: cc.ChannelID,
		Enabled:   cc.Enabled,
		Mode:      mode,
		chaos:     cc.Chaos,
		voices:    voices,
		lastSpoke: map[string]int{},
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	urls := make([]string, 0, len(voices))
	for _, p := range voices {
		if p.WebhookURL != "" {
			urls = append(urls, p.WebhookURL)
		}
	}
	ch.choir = discordio.NewChoirWebhooks(urls)
	ch.sessions = NewSessionManagerShared(cfg, g)
	if cc.MaxMessagesPerSession > 0 {
		ch.sessions.SetSessionBudget(cc.MaxMessagesPerSession)
	}
	ch.dedup = NewDedup(cfg.Conductor.RecentResponses)
	ch.controls = NewControls(cfg)
	// Per-chamber chaos starts at the configured level.
	ch.controls.setChaosDirect(cc.Chaos)

	return ch, nil
}

// ValidateHandoff checks an Ohm-supplied handoff against the allowlist and
// returns a descriptive error for any mismatch, without enabling anything.
// Call BEFORE flipping Enabled=true. This is the "validate before activation"
// gate.
func ValidateHandoff(cc config.ChamberConfig, cfg *config.Config, choir *discordio.ChoirWebhooks) error {
	if cc.ChannelID == "" {
		return fmt.Errorf("chamber %q: channel_id empty", cc.Name)
	}
	if cc.GuildID == "" {
		return fmt.Errorf("chamber %q: guild_id empty", cc.Name)
	}
	if !hasStr(cfg.Discord.AllowedChannels, cc.ChannelID) {
		return fmt.Errorf("chamber %q: channel_id %q not in allowed_channels", cc.Name, cc.ChannelID)
	}
	if len(cfg.Discord.AllowedGuilds) > 0 && !hasStr(cfg.Discord.AllowedGuilds, cc.GuildID) {
		return fmt.Errorf("chamber %q: guild_id %q not in allowed_guilds", cc.Name, cc.GuildID)
	}
	if len(cc.Personas) == 0 {
		return fmt.Errorf("chamber %q: empty persona roster", cc.Name)
	}
	return nil
}

// ChaosLevel returns the chamber's current chaos level.
func (ch *Chamber) ChaosLevel() int {
	ch.chaosMu.RLock()
	defer ch.chaosMu.RUnlock()
	return ch.chaos
}

// SetChaos sets the chamber's chaos level (clamped 0..5).
func (ch *Chamber) SetChaos(n int) {
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	ch.chaosMu.Lock()
	ch.chaos = n
	ch.chaosMu.Unlock()
}

// chaosParams returns the ChaosParams for this chamber's level.
func (ch *Chamber) chaosParams() ChaosParams {
	return chaosFor(ch.ChaosLevel())
}

// Voce returns the chamber's resolved persona roster.
func (ch *Chamber) Roster() []*persona.Persona { return ch.voices }

// hasStr reports whether list contains v.
func hasStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
