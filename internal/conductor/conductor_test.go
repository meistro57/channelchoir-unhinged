package conductor

import (
	"math/rand"
	"testing"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/persona"
)

func unhingedVoices() []*persona.Persona {
	return []*persona.Persona{
		{Name: "Troll", Chattiness: 0.9},
		{Name: "OneWord", Chattiness: 0.9},
		{Name: "Interrupter", Chattiness: 0.9},
		{Name: "Derailer", Chattiness: 0.9},
	}
}

func newTestConductor(vpt int, threshold float64, cooldown int) *Conductor {
	cfg := &config.Config{
		Conductor: config.ConductorConfig{
			VoicesPerTick:     vpt,
			SpeakThreshold:    threshold,
			CooldownTurns:     cooldown,
			TriggerWeight:     0.35,
			DirectAddressBoost: 2.0,
			Jitter:            0.001,
			MaxVerse:          -1,
		},
	}
	return &Conductor{
		cfg:       cfg,
		voices:    unhingedVoices(),
		lastSpoke: map[string]int{},
		rng:       rand.New(rand.NewSource(1)),
	}
}

func TestChooseMany_AllVoicesSpeakWhenUncapped(t *testing.T) {
	c := newTestConductor(-1, -1.0, 0)
	last := line{speaker: "someone", text: "hey", human: true}
	got := c.chooseMany(last)
	if len(got) != len(unhingedVoices()) {
		t.Fatalf("expected all %d voices eligible, got %d", len(unhingedVoices()), len(got))
	}
}

func TestChooseMany_CapsAtN(t *testing.T) {
	c := newTestConductor(2, -1.0, 0)
	last := line{speaker: "someone", text: "hey", human: true}
	got := c.chooseMany(last)
	if len(got) != 2 {
		t.Fatalf("expected 2 voices, got %d", len(got))
	}
}

func TestChooseMany_NeverRepliesToSelf(t *testing.T) {
	c := newTestConductor(-1, -1.0, 0)
	last := line{speaker: "Troll", text: "lol", human: false}
	for _, p := range c.chooseMany(last) {
		if p.Name == "Troll" {
			t.Fatalf("a voice must never reply to itself")
		}
	}
}

func TestChooseMany_ZeroCooldownNoSitOut(t *testing.T) {
	c := newTestConductor(-1, -1.0, 0)
	c.turn = 5
	c.lastSpoke["Troll"] = 5
	last := line{speaker: "human", text: "hi", human: true}
	for _, p := range c.chooseMany(last) {
		if p.Name == "Troll" {
			return
		}
	}
	t.Fatalf("with cooldown_turns=0 a voice that just spoke must still be eligible")
}
