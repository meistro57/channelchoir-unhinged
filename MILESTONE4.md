# Milestone 4 — Multi-Chamber Expansion (DESIGN)

## Goal

Split the single-channel conductor into a registry of independent chambers,
each an isolated conversation cell with its own transcript, session, dedup, and
chaos, all sharing one process-wide safety ceiling and kill switch.

## New concepts

### Chamber (per-channel state)
- GuildID, ChannelID
- Name (operator-facing label)
- Persona roster (list of persona NAMES; resolved against the persona dir)
- ChaosLevel (0..5)
- Mode (autonomous | human_gated)
- Session/message limits (per-chamber override)
- Enabled bool (inactive until Mark approves live activation)
- Webhook identities (choir webhook set for THIS channel)

### Conductor (orchestrator)
- Shared SessionManager for the PROCESS-WIDE ceiling + per-minute window + kill
  switch (global). Per-chamber SessionManager only enforces per-chamber session
  budget; the shared one enforces the global ceiling on top.
- registry: map[channelID]*Chamber
- poll loop iterates enabled chambers.

## Isolation invariant

Each chamber has its own SessionManager (per-chamber budget) AND its own
transcript/dedup. A bot message in Chamber A is fetched, classified, and scored
ONLY against Chamber A's transcript and roster. Nothing crosses chambers.

## Ohm-to-Lewis handoff

Ohm supplies {guild_id, channel_id, permission verification, webhook URLs}.
Lewis validates BEFORE enabling: channel in allowed_channels, guild in
allowed_guilds (if set), webhook URLs parse to valid IDs, persona names resolve.
A chamber stays Enabled=false until Mark explicitly approves live activation.

## Operator controls

Add to !choir: chamber <name|id> (select target), chambers (list), chamber
<name> chaos <0-5> (per-chamber chaos). Global pause/stop remain process-wide.

## Config shape (proposed)

    chambers:
      - name: choir-test-cell
        guild_id: "1572664667455619175"
        channel_id: "1572998079291068437"
        enabled: false
        personas: [troll, derailer, incoherent]
        chaos: 3
        mode: autonomous
        max_messages_per_session: 50

## Testing plan (isolated, mocked)

1. Cross-chamber isolation (bot msg in A never triggers B).
2. Shared ceiling (N chambers never exceed global cap).
3. Per-chamber kill (exhausting A stops A, leaves B live).
4. Correct routing (message scored only against its channel roster).
5. Inactive chamber never polls/generates.
6. handoff validation (invalid guild/channel/webhook/persona rejected).

## Backward compatibility

Single-channel config maps to a single implicit chamber; M1-M3 tests keep
passing.

## Out of scope (noted for later)

JSONL transcript/corpus export (Ohm's suggestion) — defer; chamber model makes
it a natural next hook.
