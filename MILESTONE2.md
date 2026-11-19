# Milestone 2 — Conversation intelligence and controls

## What changed

1. **Content-level duplicate protection** (internal/conductor/dedup.go)
   A bounded ring of recent responses (config: conductor.recent_responses).
   Normalization folds case + strips punctuation + collapses whitespace, so
   "LOL!!" and "lol" compare equal. Duplicates are suppressed before posting;
   the history prunes old entries rather than banning phrases permanently.

2. **Explicit human-intervention mode** (config: conductor.mode)
   - autonomous: choir messages may chain (bounded by bot_to_bot + budgets).
   - human_gated: choir never replies to its own echo, even when bot_to_bot is
     on — only a human/external message advances the conversation.
   Neither mode can override process-wide limits or the kill switch.

3. **!choir control interface** (internal/conductor/controls.go + config)
   Commands: status, chaos <0-5>, persona add/remove/list <name>,
   bot2bot on/off, pause, resume, stop, limit (read-only report).
   Auth: only Discord user IDs in controls.operators are honoured; bots are
   rejected outright; unknown IDs get "unauthorised". Because ChannelChoir is
   REST-polling (no gateway/slash commands), commands arrive as ordinary
   prefixed messages and we validate the AUTHOR before acting.

## Safety boundary

- Process-wide ceilings (max_messages_per_run, per-minute, runtime, kill switch)
  are NOT chat-adjustable. The limit command is read-only. Chaos/participation/
  pause are the only runtime-mutable knobs, all under the ceilings.

## Auth note (architecture)

The read-only bot token cannot SEND; control responses are logged for now.
Surfacing replies to operators (DM vs a dedicated webhook channel) is an
explicit follow-up decision before live enablement. controls.enabled defaults
to false.

## Test results

- 40 tests pass; go vet clean; go test -race clean (incl. 50-worker concurrent
  Reservation atomicity test).
- New coverage: dedup (identical/near-identical/prune/off), human-gated vs
  autonomous gate, control auth (disabled/bot/unauthorised/operator), malformed
  chaos, pause/resume/stop, bot2bot toggle, persona add/remove, read-only limit,
  unknown command.

## Remaining risks

- Control replies are log-only until an operator surface is chosen.
- Chaos level (0..5) currently stored but not yet mapped onto scoring knobs;
  it is wired to the dashboard but the conductor still uses raw config jitter.
  Mapping chaos -> voices_per_tick/jitter is Milestone 3 work.
