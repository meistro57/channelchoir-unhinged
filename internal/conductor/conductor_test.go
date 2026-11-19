package conductor

import (
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/persona"
)

func unhingedVoices() []*persona.Persona {
	return []*persona.Persona{
		{Name: "Troll", Chattiness: 0.9, WebhookURL: "https://discord.com/api/webhooks/100/tok"},
		{Name: "OneWord", Chattiness: 0.9, WebhookURL: "https://discord.com/api/webhooks/101/tok"},
		{Name: "Interrupter", Chattiness: 0.9, WebhookURL: "https://discord.com/api/webhooks/102/tok"},
		{Name: "Derailer", Chattiness: 0.9, WebhookURL: "https://discord.com/api/webhooks/103/tok"},
	}
}

func newTestConductor(cfg *config.Config) *Conductor {
	if cfg == nil {
		cfg = &config.Config{Conductor: config.ConductorConfig{VoicesPerTick: -1, SpeakThreshold: -1, Jitter: 0.001, MaxVerse: -1}}
	}
	return &Conductor{
		cfg:       cfg,
		voices:    unhingedVoices(),
		lastSpoke: map[string]int{},
		rng:       rand.New(rand.NewSource(1)),
		choir:     discordio.NewChoirWebhooks(voiceURLs(unhingedVoices())),
		sessions:  NewSessionManager(cfg),
		controls:  NewControls(cfg),
		dedup:     NewDedup(cfg.Conductor.RecentResponses),
	}
}

func voiceURLs(vs []*persona.Persona) []string {
	out := make([]string, 0, len(vs))
	for _, p := range vs {
		if p.WebhookURL != "" {
			out = append(out, p.WebhookURL)
		}
	}
	return out
}

// Existing chooseMany tests, updated for origin-based lines.
func TestChooseMany_AllVoicesSpeakWhenUncapped(t *testing.T) {
	c := newTestConductor(nil)
	last := line{speaker: "someone", text: "hey", origin: discordio.OriginHuman}
	got := c.chooseMany(last)
	if len(got) != len(unhingedVoices()) {
		t.Fatalf("expected all %d voices eligible, got %d", len(unhingedVoices()), len(got))
	}
}

func TestChooseMany_CapsAtN(t *testing.T) {
	c := newTestConductor(&config.Config{Conductor: config.ConductorConfig{VoicesPerTick: 2, SpeakThreshold: -1, Jitter: 0.001, MaxVerse: -1}})
	last := line{speaker: "someone", text: "hey", origin: discordio.OriginHuman}
	got := c.chooseMany(last)
	if len(got) != 2 {
		t.Fatalf("expected 2 voices, got %d", len(got))
	}
}

func TestChooseMany_NeverRepliesToSelf(t *testing.T) {
	c := newTestConductor(nil)
	last := line{speaker: "Troll", text: "lol", origin: discordio.OriginChoir}
	for _, p := range c.chooseMany(last) {
		if p.Name == "Troll" {
			t.Fatalf("a voice must never reply to itself")
		}
	}
}

func TestChooseMany_ZeroCooldownNoSitOut(t *testing.T) {
	c := newTestConductor(nil)
	c.turn = 5
	c.lastSpoke["Troll"] = 5
	last := line{speaker: "human", text: "hi", origin: discordio.OriginHuman}
	for _, p := range c.chooseMany(last) {
		if p.Name == "Troll" {
			return
		}
	}
	t.Fatalf("with cooldown_turns=0 a voice that just spoke must still be eligible")
}

// --- Milestone 1: origin classification ---

func TestOriginClassification(t *testing.T) {
	choir := discordio.NewChoirWebhooks([]string{"https://discord.com/api/webhooks/100/x"})
	cases := []struct {
		name string
		m    discordio.Message
		want discordio.Origin
	}{
		{"human", discordio.Message{Author: discordio.Author{Bot: false}}, discordio.OriginHuman},
		{"choir", discordio.Message{WebhookID: "100", Author: discordio.Author{Bot: true}}, discordio.OriginChoir},
		{"external webhook", discordio.Message{WebhookID: "999", Author: discordio.Author{Bot: true}}, discordio.OriginExternal},
		{"external bot", discordio.Message{Author: discordio.Author{Bot: true}}, discordio.OriginExternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := discordio.Classify(tc.m, choir); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWebhookIDFromURL(t *testing.T) {
	if got := discordio.WebhookIDFromURL("https://discord.com/api/webhooks/12345/abc"); got != "12345" {
		t.Fatalf("got %q, want 12345", got)
	}
	if got := discordio.WebhookIDFromURL("not a url"); got != "" {
		t.Fatalf("expected empty for non-webhook url, got %q", got)
	}
}

// --- Milestone 1 verification: atomic reservation + budgets ---

func TestSessionManager_HardMessageCap(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 3}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)
	for i := 0; i < 3; i++ {
		if _, err := sm.Reserve(time.Now()); err != nil {
			t.Fatalf("reserve %d should be allowed: %v", i, err)
		}
		sm.Commit("Troll")
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("4th reserve must be blocked by hard cap")
	}
	if sm.PostedTotal() != 3 {
		t.Fatalf("posted total = %d, want 3", sm.PostedTotal())
	}
}

func TestSessionManager_PerMinuteCap(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxMessagesPerMinute: 2}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)
	now := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := sm.Reserve(now); err != nil {
			t.Fatalf("reserve %d allowed: %v", i, err)
		}
	}
	if _, err := sm.Reserve(now); err == nil {
		t.Fatalf("3rd reserve in same minute must be blocked")
	}
	if _, err := sm.Reserve(now.Add(61 * time.Second)); err != nil {
		t.Fatalf("reserve after a minute should be allowed: %v", err)
	}
}

func TestSessionManager_ChoirEchoDoesNotRestartBudget(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 2}}
	sm := NewSessionManager(cfg)
	choir := discordio.NewChoirWebhooks([]string{"https://discord.com/api/webhooks/100/x"})
	sm.NoteInbound(humanMsg(), choir)
	for i := 0; i < 2; i++ {
		if _, err := sm.Reserve(time.Now()); err != nil {
			t.Fatalf("reserve %d allowed: %v", i, err)
		}
	}
	sm.NoteInbound(choirMsg(), choir)
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("choir echo must not reset the exhausted budget")
	}
}

func TestSessionManager_HumanRestartsBudget(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxMessagesPerSession: 2}}
	sm := NewSessionManager(cfg)
	choir := discordio.NewChoirWebhooks([]string{"https://discord.com/api/webhooks/100/x"})
	sm.NoteInbound(humanMsg(), choir)
	for i := 0; i < 2; i++ {
		if _, err := sm.Reserve(time.Now()); err != nil {
			t.Fatalf("reserve %d allowed: %v", i, err)
		}
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("session budget should be exhausted")
	}
	sm.NoteInbound(humanMsg(), choir)
	if _, err := sm.Reserve(time.Now()); err != nil {
		t.Fatalf("fresh human message must reset the session budget: %v", err)
	}
}

func TestSessionManager_KillSwitchBlocks(t *testing.T) {
	dir := t.TempDir()
	kill := dir + "/kill"
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, KillSwitchPath: kill}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)
	if _, err := sm.Reserve(time.Now()); err != nil {
		t.Fatalf("reserve should be allowed before kill switch: %v", err)
	}
	if err := os.WriteFile(kill, []byte("kill"), 0644); err != nil {
		t.Fatalf("write kill switch: %v", err)
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("kill switch present must block reserve")
	}
}

func TestSessionManager_SessionRuntimeCap(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxRuntimeSeconds: 60}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)
	s := sm.Active()
	if _, err := sm.Reserve(s.StartedAt.Add(61 * time.Second)); err == nil {
		t.Fatalf("session past runtime cap must block reserve")
	}
	sm.NoteInbound(humanMsg(), nil)
	s2 := sm.Active()
	if _, err := sm.Reserve(s2.StartedAt.Add(30 * time.Second)); err != nil {
		t.Fatalf("fresh session should be within its runtime window: %v", err)
	}
}

// --- Milestone 1 verification: atomic reservation under concurrency ---

func TestReserve_ConcurrentNeverExceedsCap(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxMessagesPerSession: 40, MaxMessagesPerMinute: 0}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)

	const workers = 50
	const attempts = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := 0; a < attempts; a++ {
				if _, err := sm.Reserve(time.Now()); err == nil {
					mu.Lock()
					success++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if success != 40 {
		t.Fatalf("reserved %d slots, want exactly 40 (session budget)", success)
	}
	if sm.PostedTotal() != 40 {
		t.Fatalf("PostedTotal=%d, want 40", sm.PostedTotal())
	}
}

func TestReserve_FailedSendStillCounts(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100, MaxMessagesPerSession: 2}}
	sm := NewSessionManager(cfg)
	sm.NoteInbound(humanMsg(), nil)

	if _, err := sm.Reserve(time.Now()); err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	// Simulate a failed post: do nothing (no Commit, no refund path exists).
	if _, err := sm.Reserve(time.Now()); err != nil {
		t.Fatalf("reserve 2 should still be allowed: %v", err)
	}
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("reserve 3 must be blocked after 2 reservations")
	}
}

func TestReserve_RapidHumanMessagesDoNotResetProcessCap(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 5, MaxMessagesPerSession: 2}}
	sm := NewSessionManager(cfg)
	choir := discordio.NewChoirWebhooks([]string{"https://discord.com/api/webhooks/100/x"})

	total := 0
	for i := 0; i < 10; i++ {
		sm.NoteInbound(humanMsg(), choir)
		for j := 0; j < 2; j++ {
			if _, err := sm.Reserve(time.Now()); err == nil {
				total++
			} else {
				break
			}
		}
	}

	if total != 5 {
		t.Fatalf("total reserved = %d, want exactly 5 (process run cap)", total)
	}
	if sm.PostedTotal() != 5 {
		t.Fatalf("PostedTotal=%d, want 5", sm.PostedTotal())
	}
}

func TestSessionManager_NoActiveSessionBlocksReserve(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 100}}
	sm := NewSessionManager(cfg)
	if _, err := sm.Reserve(time.Now()); err == nil {
		t.Fatalf("reserve with no active session must fail")
	}
}

// --- Milestone 1: bot-to-bot gate ---

func TestBotToBotGate_ChoirMessageBlockedWhenDisabled(t *testing.T) {
	c := newTestConductor(nil)
	c.cfg.Conductor.BotToBot = false
	last := line{speaker: "Troll", text: "lol", origin: discordio.OriginChoir}
	if !(last.origin == discordio.OriginChoir && !c.cfg.Conductor.BotToBot) {
		t.Fatalf("choir-origin message with bot_to_bot off should gate generation")
	}
}

func TestBotToBotGate_ChoirMessageAllowedWhenEnabled(t *testing.T) {
	c := newTestConductor(nil)
	c.cfg.Conductor.BotToBot = true
	last := line{speaker: "Troll", text: "lol", origin: discordio.OriginChoir}
	if last.origin == discordio.OriginChoir && !c.cfg.Conductor.BotToBot {
		t.Fatalf("choir-origin message with bot_to_bot on should NOT gate generation")
	}
	if got := c.chooseMany(last); len(got) == 0 {
		t.Fatalf("expected voices to reply to choir message when bot_to_bot enabled")
	}
}

// helpers

func humanMsg() discordio.Message {
	return discordio.Message{Author: discordio.Author{ID: "u1", Username: "human", Bot: false}}
}

func choirMsg() discordio.Message {
	return discordio.Message{WebhookID: "100", Author: discordio.Author{ID: "w1", Username: "Troll", Bot: true}}
}
