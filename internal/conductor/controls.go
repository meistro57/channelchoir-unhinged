package conductor

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/meistro57/channelchoir-unhinged/internal/config"
)

// Controls is the runtime state and parser for the !choir control interface.
// It holds the ADJUSTABLE chaos/participation settings that operators may
// mutate at runtime, layered UNDER the non-negotiable process-wide safety
// ceilings. No command can raise a ceiling above what the config permits, and
// no command can bypass the kill switch.
//
// Auth model: control commands are only honoured when the author's Discord user
// ID is in the Controls.Operators allowlist. Bots and arbitrary users can never
// change safety settings. Because ChannelChoir is REST-polling (no gateway /
// slash commands), commands arrive as ordinary channel messages with a name
// prefix; we validate the AUTHOR, not the message, before acting.
type Controls struct {
	mu  sync.Mutex
	cfg *config.Config

	// runtime-adjusted state (all bounded by config ceilings):
	paused     bool
	botToBot   *bool           // nil = use config default
	chaosLevel int             // 0..5, influences voices_per_tick / jitter at runtime
	excluded   map[string]bool // persona names excluded from participation

	// chamberName identifies the chamber these controls belong to (empty for a
	// standalone single-channel conductor).
	chamberName string
}

func NewControls(cfg *config.Config) *Controls {
	return &Controls{
		cfg:      cfg,
		excluded: map[string]bool{},
	}
}

// IsPaused reports whether the conductor is paused.
func (c *Controls) IsPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// BotToBotEnabled reports the effective bot-to-bot setting, honouring a runtime
// override if present.
func (c *Controls) BotToBotEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.botToBot != nil {
		return *c.botToBot
	}
	return c.cfg.Conductor.BotToBot
}

// ChaosLevel returns the current chaos level (0..5).
func (c *Controls) ChaosLevel() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chaosLevel
}

// IsExcluded reports whether a persona has been excluded from participation.
func (c *Controls) IsExcluded(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.excluded[name]
}

// Excluded returns a copy of the excluded-persona set.
func (c *Controls) Excluded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.excluded))
	for n := range c.excluded {
		out = append(out, n)
	}
	return out
}

// authorIsOperator reports whether the given Discord user ID is allowlisted.
func (c *Controls) authorIsOperator(authorID string) bool {
	if authorID == "" {
		return false
	}
	for _, op := range c.cfg.Controls.Operators {
		if op == authorID {
			return true
		}
	}
	return false
}

// Parse handles a candidate command message. It returns (response, handled).
// handled is false when the message is not addressed to the controls interface.
// Auth failures still "handle" the message (so the conductor does not treat it
// as ordinary dialogue) and return a refusal.
func (c *Controls) Parse(content, authorID string, isBot bool) (string, bool) {
	if !c.cfg.Controls.Enabled {
		return "", false
	}
	prefix := c.cfg.Controls.Prefix
	if prefix == "" {
		prefix = "!choir"
	}
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return "", false
	}

	// A bot may NEVER issue control commands, regardless of allowlist.
	if isBot {
		return "control commands are restricted to human operators", true
	}
	if !c.authorIsOperator(authorID) {
		return "unauthorised", true
	}

	args := strings.Fields(trimmed[len(prefix):])
	if len(args) == 0 {
		return c.status(), true
	}

	switch strings.ToLower(args[0]) {
	case "status":
		return c.status(), true
	case "pause":
		c.setPaused(true)
		return "paused", true
	case "resume":
		c.setPaused(false)
		return "resumed", true
	case "stop":
		c.setPaused(true)
		return "STOPPED: paused. Trip the kill switch for a hard stop.", true
	case "chaos":
		return c.setChaos(args[1:]), true
	case "bot2bot", "bot-to-bot", "b2b":
		return c.setBotToBot(args[1:]), true
	case "persona", "personas":
		return c.setPersona(args[1:]), true
	case "limit":
		return c.setLimit(args[1:]), true
	case "chambers":
		return c.listChambers(), true
	case "chamber":
		return c.setChamberTarget(args[1:]), true
	default:
		return "unknown command: " + args[0], true
	}
}

// SetChamberName labels these controls with the chamber they belong to.
func (c *Controls) SetChamberName(name string) {
	c.mu.Lock()
	c.chamberName = name
	c.mu.Unlock()
}

// ChamberName reports the chamber label (empty for standalone).
func (c *Controls) ChamberName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chamberName
}

// listChambers reports this chamber's identity. Cross-process chamber listing
// is an orchestrator-level concern; from within a chamber we report self plus
// the configured chamber count when known.
func (c *Controls) listChambers() string {
	name := c.ChamberName()
	if name == "" {
		return "chamber: (standalone)"
	}
	return "chamber: " + name
}

// setChamberTarget handles "chamber <name> [chaos N]". Because each chamber
// runs its own conductor and receives only its own channel's messages, a
// cross-chamber command is routed by the orchestrator. From within a single
// chamber we only honour commands targeting ourselves.
func (c *Controls) setChamberTarget(args []string) string {
	if len(args) == 0 {
		return "chamber <name> [chaos 0-5]. This chamber: " + c.ChamberName()
	}
	name := args[0]
	if name != c.ChamberName() {
		return "chamber " + name + " is not this chamber (cross-chamber routing is orchestrator-level)"
	}
	if len(args) >= 3 && strings.EqualFold(args[1], "chaos") {
		return c.setChaos([]string{args[2]})
	}
	return c.status()
}

func (c *Controls) setPaused(v bool) {
	c.mu.Lock()
	c.paused = v
	c.mu.Unlock()
}

// setChaosDirect sets the chaos level directly (used by NewChamber to seed
// a per-chamber level without going through the command parser).
func (c *Controls) setChaosDirect(n int) {
	c.mu.Lock()
	c.chaosLevel = n
	c.mu.Unlock()
}

func (c *Controls) setChaos(args []string) string {
	if len(args) == 0 {
		return fmt.Sprintf("chaos level: %d", c.ChaosLevel())
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 0 || n > 5 {
		return "chaos must be 0..5"
	}
	c.mu.Lock()
	c.chaosLevel = n
	c.mu.Unlock()
	return fmt.Sprintf("chaos set to %d", n)
}

func (c *Controls) setBotToBot(args []string) string {
	if len(args) == 0 {
		return fmt.Sprintf("bot-to-bot: %v", c.BotToBotEnabled())
	}
	switch strings.ToLower(args[0]) {
	case "on", "true", "1":
		v := true
		c.mu.Lock()
		c.botToBot = &v
		c.mu.Unlock()
		return "bot-to-bot enabled"
	case "off", "false", "0":
		v := false
		c.mu.Lock()
		c.botToBot = &v
		c.mu.Unlock()
		return "bot-to-bot disabled"
	default:
		return "bot2bot on|off"
	}
}

func (c *Controls) setPersona(args []string) string {
	if len(args) == 0 {
		return "persona list|add <name>|remove <name>"
	}
	switch strings.ToLower(args[0]) {
	case "list":
		return "excluded personae: " + strings.Join(c.Excluded(), ", ")
	case "add", "remove":
		if len(args) < 2 {
			return "persona " + args[0] + " <name>"
		}
		name := args[1]
		c.mu.Lock()
		if args[0] == "add" {
			c.excluded[name] = true
		} else {
			delete(c.excluded, name)
		}
		c.mu.Unlock()
		return "persona " + name + " " + args[0] + "ed"
	default:
		return "persona list|add <name>|remove <name>"
	}
}

// setLimit reports the current limits. Raising process-wide ceilings at runtime
// is deliberately NOT supported via chat — ceilings live in the config file and
// are never mutated by a chat command. This keeps the hard caps independent of
// the adjustable chaos settings.
func (c *Controls) setLimit(args []string) string {
	s := c.cfg.Safeguards
	return fmt.Sprintf(
		"limits (config, not chat-adjustable): run=%d session=%d rate=%d/min runtime=%ds",
		s.MaxMessagesPerRun, s.MaxMessagesPerSession, s.MaxMessagesPerMinute, s.MaxRuntimeSeconds)
}

func (c *Controls) status() string {
	return fmt.Sprintf(
		"paused=%v bot2bot=%v chaos=%d excluded=%s",
		c.IsPaused(), c.BotToBotEnabled(), c.ChaosLevel(), strings.Join(c.Excluded(), ","))
}
