# ChannelChoir Unhinged

A controllable multi-persona Discord conversation engine. Fourteen lunatics, a
conductor with brakes, chaos levels from Polite to Unhinged, and a kill switch
that works even when Discord can't hear it.

This is a fork of [ChannelChoir](https://github.com/meistro57/channelchoir).
Upstream is a turn-taking conductor where one persona replies per tick. Unhinged
rips out the damping — multiple voices per tick, zero cooldown, near-zero delay
— and replaces it with a *controllable* chaos engine, then re-installs the
safety layer underneath so "unpredictable conversation" never becomes
"uncontrolled infrastructure."

## What It Is

A single Go binary that watches one or more Discord **chambers** (channels),
picks which of your AI personas speak next, generates their lines against a
local Ollama model, and posts each through that persona's own webhook — so
every voice shows up with its own name and avatar.

## The Core Idea

Generation is easy. Arbitration is the interesting part. Unhinged makes the
arbitration *explicit and operator-controllable*:

- **Origin-aware routing** — every message is classified as human, choir (our
  own webhook), or external (another bot). Choir echoes trigger replies only
  when bot-to-bot is on *and* the session still has budget.
- **Atomic session budgets** — Reserve/Commit claim a slot from a shared
  process-wide ceiling *before* any webhook or generation work, so concurrent
  speakers and failed sends can never overshoot the cap.
- **Chaos levels (0–5)** — Polite → Mild → Active → Rowdy → Chaotic → Unhinged,
  mapped onto voices-per-tick, scoring jitter, pacing, participation threshold,
  and bot-to-bot eagerness.
- **Multi-chamber orchestration** — each chamber runs its own poll goroutine
  with its own transcript, session, dedup, and chaos, all stacked on one shared
  safety ceiling (a slow Ollama call in one chamber can't stall another).
- **!choir control surface** — operator-only commands for status, chaos,
  persona participation, bot-to-bot, pause/resume/stop, and chamber selection.

## Hard Safety Layer (never disabled)

- max_messages_per_run — process-lifetime absolute ceiling.
- max_messages_per_session — per-conversation budget (resets on a human).
- max_messages_per_minute — shared rolling rate cap.
- max_runtime_seconds — per-session runtime cap.
- kill_switch_path — a file; while it exists, no chamber posts, regardless of
  chaos level, mode, or channel.
- allowed_channels / allowed_guilds — mandatory allowlists.
- allowed_mentions: [] — the choir physically cannot @everyone.

Chaos, mode, and chamber settings are layered *under* these ceilings. They can
tighten things, never loosen them.

## Install

Requires Go 1.23+ and a running Ollama.

```
git clone https://github.com/meistro57/channelchoir-unhinged.git
cd channelchoir-unhinged
go mod tidy
make build
```

### Discord setup

1. Create a bot application, invite it with Read Messages + Read Message
   History. It only listens — it never posts.
2. Per voice: Channel → Integrations → Webhooks → New Webhook, copy each URL.
3. Enable Developer Mode, right-click the channel, Copy Channel ID.
4. (Optional) a dedicated control webhook for [CHOIR ...] operator notices.

### Configure

```
cp .env.example .env            # token + per-voice webhook URLs
cp choir.unhinged.example.yaml choir.yaml
set -a; source .env; set +a
```

Nothing secret goes in YAML — ${VAR} is expanded from the environment, and
choir.yaml is gitignored.

### Single chamber vs multi-chamber

For one channel, use the discord + conductor + safeguards + controls blocks.
For multiple isolated test chambers, add a chambers: list (each with its own
guild/channel, persona roster, chaos, mode, limits, and enabled flag):

```
chambers:
  - name: choir-test-cell
    guild_id: "1572664667455619175"
    channel_id: "1572998079291068437"
    enabled: false            # Mark's approval = flip to true
    personas: [troll, derailer, incoherent]
    chaos: 3
    mode: autonomous
```

A chamber with enabled: false is registered but never polls, generates, or
posts. Validate an Ohm-supplied handoff (guild/channel/webhook/persona) before
flipping it on.

## Usage

```
make dry     # log what each voice would say, post nothing
make run     # live
```

Always make dry first. Watch it, tune the chaos and ceilings, and confirm the
kill-switch file stops output before you let it loose.

## Controls (!choir)

Operator-only (authorised by Discord user ID allowlist; bots rejected outright):

- status — paused, bot-to-bot, chaos, excluded personas
- chaos <0-5> — set the chaos level
- persona add/remove/list <name> — participation
- bot2bot on|off — bot-to-bot chaining
- pause / resume / stop — global (process-wide)
- chambers / chamber <name> — chamber status and targeting
- limit — read-only report (ceilings are config, not chat-adjustable)

## Architecture

```
Discord channel(s)
   |  poll (bot token, read-only)
   v
Orchestrator -- one goroutine per enabled Chamber
   |             each with its own transcript/session/dedup/chaos
   v
Chamber -- origin classification + chaos scoring → N speakers
   v
Ollama -- persona system prompt + rolling transcript
   v
Discord webhook (that voice's name + avatar, allowed_mentions: [])
```

All posts reserve from a shared Global ceiling before anything is generated.

## Tests

```
go test ./...        # 56 tests
go test -race ./...  # clean (concurrency atomicity, shared ceiling)
go vet ./...
```

Coverage includes origin classification, atomic budget reservation under
concurrency, shared ceiling across chambers, per-chamber isolation, chaos
mapping, dedup, kill-switch precedence, handoff validation, and JSONL export.

## JSONL Transcript Export

Messages can be exported as JSONL (session/chamber IDs, timestamp, persona,
origin, chaos level, message ID + reply ref) with no tokens or webhook URLs in
the output.

## Known Limits

- REST polling, not the gateway — a few seconds of latency, zero websocket
  deps. The kill-switch file is the instant stop path regardless.
- Cross-chamber !choir routing — each chamber only sees its own channel; a true
  orchestrator-level command router is the remaining gap.
- No long-term memory — voices remember transcript_size messages.
- Text only — no reactions/threads/presence (webhooks instead of N bots).

## License

MIT
