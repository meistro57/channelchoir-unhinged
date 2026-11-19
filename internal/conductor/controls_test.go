package conductor

import (
	"testing"

	"github.com/meistro57/channelchoir/internal/config"
)

func cfgWithControls(enabled bool, operators []string) *config.Config {
	return &config.Config{
		Controls: config.ControlsConfig{
			Enabled:   enabled,
			Prefix:    "!choir",
			Operators: operators,
		},
		Safeguards: config.SafeguardsConfig{
			MaxMessagesPerRun: 100,
		},
	}
}

func TestControls_DisabledReturnsNotHandled(t *testing.T) {
	c := NewControls(cfgWithControls(false, []string{"mark"}))
	if _, ok := c.Parse("!choir status", "mark", false); ok {
		t.Fatalf("disabled controls must not handle commands")
	}
}

func TestControls_BotIsRejected(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, ok := c.Parse("!choir status", "mark", true)
	if !ok {
		t.Fatalf("bot command should be handled (and refused)")
	}
	if resp != "control commands are restricted to human operators" {
		t.Fatalf("unexpected refusal: %q", resp)
	}
}

func TestControls_UnauthorisedUserRejected(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, ok := c.Parse("!choir stop", "evil", false)
	if !ok {
		t.Fatalf("unauthorised command should be handled (and refused)")
	}
	if resp != "unauthorised" {
		t.Fatalf("unexpected response: %q", resp)
	}
}

func TestControls_OperatorAccepted(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, ok := c.Parse("!choir status", "mark", false)
	if !ok {
		t.Fatalf("operator command should be handled")
	}
	if resp == "" {
		t.Fatalf("status should return a response")
	}
}

func TestControls_MalformedChaos(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, _ := c.Parse("!choir chaos 99", "mark", false)
	if resp != "chaos must be 0..5" {
		t.Fatalf("chaos 99 rejected, got %q", resp)
	}
	if resp, _ := c.Parse("!choir chaos banana", "mark", false); resp != "chaos must be 0..5" {
		t.Fatalf("chaos banana rejected, got %q", resp)
	}
}

func TestControls_ChaosSetGet(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	if _, _ = c.Parse("!choir chaos 4", "mark", false); c.ChaosLevel() != 4 {
		t.Fatalf("chaos = %d, want 4", c.ChaosLevel())
	}
}

func TestControls_PauseResumeStop(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	c.Parse("!choir pause", "mark", false)
	if !c.IsPaused() {
		t.Fatalf("pause should pause")
	}
	c.Parse("!choir resume", "mark", false)
	if c.IsPaused() {
		t.Fatalf("resume should resume")
	}
	c.Parse("!choir stop", "mark", false)
	if !c.IsPaused() {
		t.Fatalf("stop should pause")
	}
}

func TestControls_BotToBotToggle(t *testing.T) {
	cfg := cfgWithControls(true, []string{"mark"})
	cfg.Conductor.BotToBot = false
	c := NewControls(cfg)
	c.Parse("!choir bot2bot on", "mark", false)
	if !c.BotToBotEnabled() {
		t.Fatalf("bot2bot on should enable")
	}
	c.Parse("!choir bot2bot off", "mark", false)
	if c.BotToBotEnabled() {
		t.Fatalf("bot2bot off should disable")
	}
}

func TestControls_PersonaAddRemove(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	c.Parse("!choir persona add Troll", "mark", false)
	if !c.IsExcluded("Troll") {
		t.Fatalf("persona add should exclude Troll")
	}
	c.Parse("!choir persona remove Troll", "mark", false)
	if c.IsExcluded("Troll") {
		t.Fatalf("persona remove should un-exclude Troll")
	}
}

func TestControls_UnknownCommand(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, ok := c.Parse("!choir bogus", "mark", false)
	if !ok {
		t.Fatalf("unknown command should be handled")
	}
	if resp != "unknown command: bogus" {
		t.Fatalf("got %q", resp)
	}
}

func TestControls_ChambersAndChamberTarget(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	c.SetChamberName("choir-test-cell")

	resp, ok := c.Parse("!choir chambers", "mark", false)
	if !ok || resp != "chamber: choir-test-cell" {
		t.Fatalf("chambers = %q (ok=%v), want chamber listing", resp, ok)
	}

	// Targeting self with a chaos set works.
	resp, _ = c.Parse("!choir chamber choir-test-cell chaos 5", "mark", false)
	if resp != "chaos set to 5" {
		t.Fatalf("chamber self-chaos = %q, want chaos set to 5", resp)
	}
	if c.ChaosLevel() != 5 {
		t.Fatalf("chaos = %d, want 5", c.ChaosLevel())
	}

	// Targeting another chamber is refused (cross-chamber routing is orchestrator-level).
	resp, _ = c.Parse("!choir chamber other-cell chaos 5", "mark", false)
	if resp == "chaos set to 5" {
		t.Fatalf("cross-chamber chaos must not be silently applied: %q", resp)
	}
}

func TestControls_LimitIsReadOnly(t *testing.T) {
	c := NewControls(cfgWithControls(true, []string{"mark"}))
	resp, _ := c.Parse("!choir limit", "mark", false)
	if resp == "" {
		t.Fatalf("limit should report")
	}
	// Attempting to set a limit via chat is read-only: sub-args are ignored.
	resp, _ = c.Parse("!choir limit message 9999", "mark", false)
	if resp == "" {
		t.Fatalf("limit with sub-args should still report (read-only)")
	}
}
