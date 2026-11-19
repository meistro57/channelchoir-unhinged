package conductor

import "math/rand"

// ChaosParams are the behaviour knobs a chaos level (0..5) maps onto. They are
// a PURE function of the level: the conductor applies them during scoring and
// pacing, but they can never touch the hard safety ceilings (session budget,
// process-wide caps, per-minute rate, runtime, kill switch) — those live in
// SessionManager.Reserve and are level-independent.
//
// Mapping (the "cause delightful problems" knob):
//
//	level  voices   jitter  delay    threshold   botToBotEagerness  flavour
//	0      1        0.2     3-8s     0.6         0.2                Polite
//	1      1-2      0.3     2-6s     0.4         0.4                Mild
//	2      1-3      0.5     1-4s     0.2         0.6                Active
//	3      2-4      0.8     0-3s     0.0         0.8                Rowdy
//	4      3-6      1.2     0-1s    -0.5         1.0                Chaotic
//	5      all      1.6     0s      -1.0         1.0                Unhinged
type ChaosParams struct {
	// MinVoices/MaxVoices bound how many voices speak per tick. A positive value
	// is a fixed count; MaxVoices of -1 means "all eligible".
	MinVoices int
	MaxVoices int

	// Jitter multiple scales the scoring randomness.
	Jitter float64

	// MinDelay/MaxDelay seconds before a chosen voice posts.
	MinDelay int
	MaxDelay int

	// Threshold is the minimum score required to speak at all (negative = eager).
	Threshold float64

	// BotToBotEagerness gates whether the choir continues a chain: a fresh
	// random draw below this value allows a consecutive choir reply when in
	// autonomous+bot2bot mode. 1.0 = always continue.
	BotToBotEagerness float64
}

// chaosFor returns the ChaosParams for a level, clamped to 0..5.
func chaosFor(level int) ChaosParams {
	if level < 0 {
		level = 0
	}
	if level > 5 {
		level = 5
	}
	switch level {
	case 0:
		return ChaosParams{MinVoices: 1, MaxVoices: 1, Jitter: 0.2, MinDelay: 3, MaxDelay: 8, Threshold: 0.6, BotToBotEagerness: 0.2}
	case 1:
		return ChaosParams{MinVoices: 1, MaxVoices: 2, Jitter: 0.3, MinDelay: 2, MaxDelay: 6, Threshold: 0.4, BotToBotEagerness: 0.4}
	case 2:
		return ChaosParams{MinVoices: 1, MaxVoices: 3, Jitter: 0.5, MinDelay: 1, MaxDelay: 4, Threshold: 0.2, BotToBotEagerness: 0.6}
	case 3:
		return ChaosParams{MinVoices: 2, MaxVoices: 4, Jitter: 0.8, MinDelay: 0, MaxDelay: 3, Threshold: 0.0, BotToBotEagerness: 0.8}
	case 4:
		return ChaosParams{MinVoices: 3, MaxVoices: 6, Jitter: 1.2, MinDelay: 0, MaxDelay: 1, Threshold: -0.5, BotToBotEagerness: 1.0}
	default: // 5
		return ChaosParams{MinVoices: -1, MaxVoices: -1, Jitter: 1.6, MinDelay: 0, MaxDelay: 0, Threshold: -1.0, BotToBotEagerness: 1.0}
	}
}

// voicesForTick resolves how many voices should speak this tick for the given
// chaos level and a source of randomness, without exceeding available voices.
func (p ChaosParams) voicesForTick(rng *rand.Rand, available int) int {
	if available <= 0 {
		return 0
	}
	// "all" is signalled by a negative MaxVoices.
	if p.MaxVoices < 0 {
		return available
	}
	lo, hi := p.MinVoices, p.MaxVoices
	if hi < lo {
		hi = lo
	}
	if lo > available {
		lo = available
	}
	if hi > available {
		hi = available
	}
	if hi == lo {
		return lo
	}
	return lo + rng.Intn(hi-lo+1)
}
