package conductor

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/persona"
)

// Orchestrator owns the full multi-chamber deployment: it builds one Conductor
// per ENABLED chamber (each with its own persona roster) and runs each in its
// own goroutine, so a slow Ollama call in one chamber can never stall another.
// Every chamber stacks its per-chamber session budget on the SAME process-wide
// Global, which owns the total message cap, the per-minute window, and the kill
// switch.
type Orchestrator struct {
	cfg      *config.Config
	discord  *discordio.Client
	model    *llm.Client
	global   *Global
	dryRun   bool
	chambers []*Conductor // one per enabled chamber (persona roster resolved)
}

// NewOrchestrator builds chamber conductors from the config. Chambers that are
// disabled (Enabled=false) are registered but NOT given a conductor, so they
// never poll, generate, or post. A chamber whose handoff fails validation, or
// whose roster references an unknown persona, is skipped with a logged reason
// rather than silently enabling a partial chamber.
func NewOrchestrator(cfg *config.Config, allVoices []*persona.Persona, dc *discordio.Client, model *llm.Client, dryRun bool) (*Orchestrator, error) {
	nameIndex := map[string]*persona.Persona{}
	for _, p := range allVoices {
		nameIndex[p.Name] = p
	}

	global := NewGlobal(cfg)
	o := &Orchestrator{
		cfg:     cfg,
		discord: dc,
		model:   model,
		global:  global,
		dryRun:  dryRun,
	}

	for _, cc := range cfg.Chambers {
		if !cc.Enabled {
			log.Printf("chamber %q: disabled — registered but inactive until approved", cc.Name)
			continue
		}
		// Validate the Ohm handoff before enabling.
		if err := ValidateHandoff(cc, cfg, nil); err != nil {
			log.Printf("chamber %q: handoff rejected (%v) — not enabled", cc.Name, err)
			continue
		}
		roster, err := resolveRoster(cc, nameIndex)
		if err != nil {
			log.Printf("chamber %q: %v — not enabled", cc.Name, err)
			continue
		}
		// Each chamber conductor shares the global ceiling.
		c := New(cfg, roster, dc, model, dryRun, global)
		// Persist the chamber's identity on the conductor and its controls for
		// status reporting and !choir chamber targeting.
		c.chamberName = cc.Name
		c.chamberID = cc.ChannelID
		c.controls.SetChamberName(cc.Name)
		o.chambers = append(o.chambers, c)
	}

	return o, nil
}

// resolveRoster maps chamber persona NAMES to *persona.Persona, preserving
// config order and rejecting unknown or duplicated names.
func resolveRoster(cc config.ChamberConfig, index map[string]*persona.Persona) ([]*persona.Persona, error) {
	out := make([]*persona.Persona, 0, len(cc.Personas))
	seen := map[string]bool{}
	for _, name := range cc.Personas {
		p, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("persona %q not found", name)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("duplicate persona %q", name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty persona roster")
	}
	return out, nil
}

// Run starts every enabled chamber conductor in its own goroutine and blocks
// until ctx is cancelled (graceful shutdown) or all workers exit. Each chamber
// runs the existing single-channel poll loop independently.
func (o *Orchestrator) Run(ctx context.Context) error {
	if len(o.chambers) == 0 {
		log.Printf("orchestrator: no enabled chambers — standing by (nothing configured to run)")
		<-ctx.Done()
		return nil
	}

	log.Printf("orchestrator: starting %d chamber worker(s), shared ceiling max_messages=%d kill_switch=%q",
		len(o.chambers), o.cfg.Safeguards.MaxMessagesPerRun, o.cfg.Safeguards.KillSwitchPath)

	var wg sync.WaitGroup
	errs := make(chan error, len(o.chambers))
	for _, c := range o.chambers {
		wg.Add(1)
		go func(ch *Conductor) {
			defer wg.Done()
			if err := ch.Run(ctx); err != nil {
				errs <- fmt.Errorf("chamber %q: %w", ch.chamberName, err)
			}
		}(c)
	}

	// Wait for cancellation or all workers to finish, then wait for workers to
	// drain so shutdown is graceful (no goroutine leaks).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		// Graceful shutdown: workers observe ctx.Done() and return nil.
		<-done
		log.Printf("orchestrator: clean shutdown after cancellation")
		return nil
	case <-done:
		log.Printf("orchestrator: all chamber workers exited")
		close(errs)
		for err := range errs {
			log.Printf("orchestrator: worker error: %v", err)
		}
		return nil
	}
}

// EnabledChamberCount reports how many enabled chamber workers are running.
func (o *Orchestrator) EnabledChamberCount() int { return len(o.chambers) }
