# Milestone 3 — Make chaos real + control webhook

## 1. Dedicated control webhook

Decision per Eli: control output goes to a dedicated webhook (config
controls.control_webhook_url), NEVER a persona webhook. Notices are prefixed:

- [CHOIR CONTROL] — command acknowledgements, config-change notices
- [CHOIR STATUS]  — status reports
- [CHOIR ERROR]   — error notices

When control_webhook_url is empty (or dry-run), notices are log-only. The
read-only bot token is never used to SEND.

## 2. Exact chaos-to-behaviour mapping (0..5)

chaosFor(level) → ChaosParams, applied in chooseMany/perform/tick. The hard
safety ceilings (Reserve: session budget, process cap, per-minute, runtime,
kill switch) have NO chaos dependency — chaos can never mint extra slots.

| level | voices/tick | jitter | delay   | threshold | bot2bot eagerness | flavour  |
|-------|-------------|--------|---------|-----------|-------------------|----------|
| 0     | 1           | 0.2    | 3-8s    | 0.6       | 0.2               | Polite   |
| 1     | 1-2         | 0.3    | 2-6s    | 0.4       | 0.4               | Mild     |
| 2     | 1-3         | 0.5    | 1-4s    | 0.2       | 0.6               | Active   |
| 3     | 2-4         | 0.8    | 0-3s    | 0.0       | 0.8               | Rowdy    |
| 4     | 3-6         | 1.2    | 0-1s    | -0.5      | 1.0               | Chaotic  |
| 5     | all         | 1.6    | 0s      | -1.0      | 1.0               | Unhinged |

- voices/tick: chooseMany cap via voicesForTick (clamped to available).
- jitter: scoring randomness multiple.
- delay: per-post pacing before generation.
- threshold: min score to speak (negative = always answer).
- bot2bot eagerness: probability a choir chain continues (chaos 5 = always).

## 3. Invariants (all covered by tests)

- Hard limits authoritative at every chaos level (TestChaos_DoesNotBypassHardBudget).
- human_gated overrides autonomous chains even at chaos 5 (human_gated gate runs
  BEFORE eagerness draw in tick).
- Kill switch always wins (TestChaos_KillSwitchAlwaysWins).
- Control changes surfaced via [CHOIR CONTROL] notices.

## Test results

- 45 tests pass; go vet clean; go test -race clean (incl. concurrent Reservation).
- New: TestChaosMapping (exact table), TestChaos_VoiceCountChangesWithLevel,
  TestChaos_DoesNotBypassHardBudget, TestChaosParams_voicesForTickClamps,
  TestChaos_KillSwitchAlwaysWins.

## Remaining risks / live-rehearsal readiness

- System is code-complete and green for an ISOLATED dry-run rehearsal; not yet
  connected to Discord. Live rehearsal requires Mark's approval plus: a real
  bot token, per-persona webhooks, the control webhook URL, and controls.enabled
  flipped on with controls.operators pinned to Mark's ID.
- control_webhook_url is unset (log-only) until a real webhook exists.
- Poll-based REST means command/status latency of ~poll_seconds; acceptable for
  a controlled rehearsal, not for sub-second emergency response (kill switch
  file remains the instant path).
