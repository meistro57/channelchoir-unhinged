# Milestone 1 — Controlled Self-Conversation

## What changed

Origin-aware conversation engine. The choir now distinguishes **human**,
**choir** (our own webhooks), and **external** (other bots/webhooks) messages,
and wraps every post in a bounded **session** with a hard budget.

New files:

- internal/discordio/origin.go — Origin type, webhook-id parsing, ChoirWebhooks set, Classify().
- internal/conductor/session.go — Session (unique ID, origin, budget, per-minute window) and SessionManager (atomic CanPost/Debit across all personas).

Modified:

- internal/config/config.go — added guild_id, allowed_guilds, bot_to_bot, max_messages_per_session, max_messages_per_minute.
- internal/discordio/discordio.go — Message.Author promoted to a named Author type.
- internal/conductor/conductor.go — wires origin classification + session budget into tick/perform; bot-to-bot gate.
- choir.unhinged.example.yaml — pinned lab guild + choir-test-cell IDs, new dials.

## Key design decisions

1. Choir messages stay in the transcript. They trigger generation ONLY when
   bot_to_bot is enabled and the session budget still allows it.
2. A choir echo can never mint a fresh session/budget. Human messages always
   (re)start a session; external bots may join but never reset one.
3. Budget is checked BEFORE posting and debited AFTER a successful send, so N
   voices selected in one tick cannot collectively exceed the allowance.
4. max_messages_per_run = process-lifetime ceiling; max_messages_per_session =
   per-conversation budget (resets on human); max_messages_per_minute = rate cap.

## Test results

- 20 tests pass (go test ./..., Go 1.26.5 at /usr/local/go/bin/go).
- go vet clean.

## Unresolved risks

- No live Discord credentials connected; all tests mocked/dry-run.
- .env.example still lists only the original 8 webhook vars; the 6 chaos
  personas add 6 more (pre-existing, not Milestone 1 scope).
- Burst mode and per-minute cap interact: burst can intentionally trip the
  per-minute cap to find Discord rate limits — that is by design for the range.
