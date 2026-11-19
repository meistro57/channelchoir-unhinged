// filename: internal/conductor/conductor.go
package conductor

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
	"github.com/meistro57/channelchoir/internal/llm"
	"github.com/meistro57/channelchoir/internal/persona"
)

// Conductor is the whole point of this project. Ten voices generating text is
// easy; deciding who gets to talk, and when everybody shuts up, is the job.
//
// UNHINGED rips out the single-winner, verse cap, cooldowns, threshold, rest,
// and human-style delays — but KEEPS the hard safety layer (allowlist, message
// cap, runtime cap, kill switch, no-ping webhooks), now expressed through an
// origin-aware SessionManager. Chaos in conversation, discipline in
// infrastructure.
type Conductor struct {
	cfg     *config.Config
	voices  []*persona.Persona
	discord *discordio.Client
	model   *llm.Client
	rng     *rand.Rand
	dryRun  bool

	// choir is the set of webhook ids our own voices post through. Used to
	// tell a choir echo from an external bot, so bot-to-bot replies can be
	// gated without ever mistaking an outsider for one of us.
	choir *discordio.ChoirWebhooks

	// sessions owns the active conversation and its hard budget. Every post
	// across every persona is debited through it, so the choir always, at some
	// point, shuts up.
	sessions *SessionManager

	transcript []line
	turn       int
	lastSpoke  map[string]int
	verse      int
	restUntil  time.Time

	lastMessageID string
}

type line struct {
	speaker string
	text    string
	origin  discordio.Origin
}

// isHuman reports whether a transcript line came from a real person.
func (l line) isHuman() bool { return l.origin == discordio.OriginHuman }

func New(cfg *config.Config, voices []*persona.Persona, dc *discordio.Client, model *llm.Client, dryRun bool) *Conductor {
	urls := make([]string, 0, len(voices))
	for _, p := range voices {
		if p.WebhookURL != "" {
			urls = append(urls, p.WebhookURL)
		}
	}

	return &Conductor{
		cfg:       cfg,
		voices:    voices,
		discord:   dc,
		model:     model,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		dryRun:    dryRun,
		choir:     discordio.NewChoirWebhooks(urls),
		sessions:  NewSessionManager(cfg),
		lastSpoke: map[string]int{},
	}
}

// Run polls the channel forever, conducting as it goes.
func (c *Conductor) Run(ctx context.Context) error {
	if err := c.seek(ctx); err != nil {
		return fmt.Errorf("find starting point: %w", err)
	}

	log.Printf("choir is live: %d voices, channel %s, model %s",
		len(c.voices), c.cfg.Discord.ChannelID, c.cfg.Ollama.Model)
	log.Printf("UNHINGED: voices_per_tick=%d, max_verse=%d, rest=%ds, cooldown=%d, threshold=%.2f, bot_to_bot=%v",
		c.cfg.Conductor.VoicesPerTick, c.cfg.Conductor.MaxVerse,
		c.cfg.Conductor.RestSeconds, c.cfg.Conductor.CooldownTurns,
		c.cfg.Conductor.SpeakThreshold, c.cfg.Conductor.BotToBot)
	log.Printf("SAFEGUARDS: allowlist=%v, guilds=%v, max_messages=%d, max_per_min=%d, max_runtime=%ds, kill_switch=%q",
		c.cfg.Discord.AllowedChannels, c.cfg.Discord.AllowedGuilds,
		c.cfg.Safeguards.MaxMessagesPerRun, c.cfg.Safeguards.MaxMessagesPerMinute,
		c.cfg.Safeguards.MaxRuntimeSeconds, c.cfg.Safeguards.KillSwitchPath)
	log.Printf("choir webhooks identified: %d of %d voices", c.choir.Len(), len(c.voices))

	ticker := time.NewTicker(time.Duration(c.cfg.Discord.PollSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("tick error: %v", err)
			}
			// A hard message cap is terminal: the choir goes silent and stays silent
			// until the process restarts (or a live operator intervenes).
			if c.cfg.Safeguards.MaxMessagesPerRun > 0 &&
				c.sessions.PostedTotal() >= c.cfg.Safeguards.MaxMessagesPerRun {
				return fmt.Errorf("HARD LIMIT: posted %d messages (cap %d) — halting",
					c.sessions.PostedTotal(), c.cfg.Safeguards.MaxMessagesPerRun)
			}
		}
	}
}

// killed reports whether the external kill-switch file is present.
func (c *Conductor) killed() bool {
	return fileExists(c.cfg.Safeguards.KillSwitchPath)
}

// seek jumps to the present so the choir doesn't wake up and start replying to
// a month of backlog.
func (c *Conductor) seek(ctx context.Context) error {
	msgs, err := c.discord.FetchAfter(ctx, c.cfg.Discord.ChannelID, "", 1)
	if err != nil {
		return err
	}
	if len(msgs) > 0 {
		c.lastMessageID = msgs[len(msgs)-1].ID
	}
	return nil
}

func (c *Conductor) tick(ctx context.Context) error {
	if c.killed() {
		log.Printf("kill switch %q present — not posting", c.cfg.Safeguards.KillSwitchPath)
		return nil
	}

	msgs, err := c.discord.FetchAfter(ctx, c.cfg.Discord.ChannelID, c.lastMessageID, 50)
	if err != nil {
		return err
	}

	for _, m := range msgs {
		c.lastMessageID = m.ID
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		origin := discordio.Classify(m, c.choir)
		// Feed every inbound message into the session tracker so origins are
		// accounted for, even if the transcript records them below.
		c.sessions.NoteInbound(m, c.choir)
		c.record(m.Speaker(), m.Content, origin)
		if origin == discordio.OriginHuman {
			c.verse = 0
			c.restUntil = time.Time{}
		}
	}

	if len(c.transcript) == 0 {
		return nil
	}

	// UNHINGED: verse cap and rest are honour-only. max_verse <= 0 = infinite,
	// rest_seconds <= 0 = never rest. A human speaker still resets the verse.
	if c.cfg.Conductor.MaxVerse > 0 && c.verse >= c.cfg.Conductor.MaxVerse {
		if c.cfg.Conductor.RestSeconds > 0 {
			c.restUntil = time.Now().Add(time.Duration(c.cfg.Conductor.RestSeconds) * time.Second)
		}
		log.Printf("verse hit %d messages — resting until a human speaks", c.verse)
		return nil
	}
	if time.Now().Before(c.restUntil) {
		return nil
	}

	last := c.transcript[len(c.transcript)-1]

	// Bot-to-bot gate: if the last thing said was our own echo and bot-to-bot
	// is disabled, do not self-reply. Choir messages stay in the transcript
	// (for context) but do not trigger generation.
	if last.origin == discordio.OriginChoir && !c.cfg.Conductor.BotToBot {
		return nil
	}

	// UNHINGED change (1): kill the single-winner rule — N voices speak.
	speakers := c.chooseMany(last)
	if len(speakers) == 0 {
		return nil
	}

	// Burst mode (8): if enabled, dump Count messages this tick instead of 1.
	count := 1
	if c.cfg.Conductor.Burst.Enabled {
		count = c.cfg.Conductor.Burst.Count
	}

	for i := 0; i < count; i++ {
		p := speakers[i%len(speakers)]
		if err := c.perform(ctx, p); err != nil {
			log.Printf("perform %s: %v", p.Name, err)
		}
		// Small inter-message gap within a burst (WindowMillis / count), so it
		// is a burst and not an atomic slam, and rate-limit retries can interleave.
		if c.cfg.Conductor.Burst.Enabled && c.cfg.Conductor.Burst.WindowMillis > 0 {
			gap := time.Duration(c.cfg.Conductor.Burst.WindowMillis/count) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(gap):
			}
		}
	}

	return nil
}

// chooseMany scores every voice against the last thing said and returns up to
// VoicesPerTick speakers (all eligible when VoicesPerTick <= 0).
func (c *Conductor) chooseMany(last line) []*persona.Persona {
	lowered := strings.ToLower(last.text)

	type scored struct {
		p     *persona.Persona
		score float64
	}
	var pool []scored

	for _, p := range c.voices {
		if strings.EqualFold(p.Name, last.speaker) {
			continue // a voice never replies to itself (hard-won upstream rule)
		}

		// UNHINGED (3): cooldown_turns <= 0 = no sit-out.
		cooldown := c.cfg.Conductor.CooldownTurns
		if p.Cooldown > 0 {
			cooldown = p.Cooldown
		}
		if cooldown > 0 {
			if lastTurn, ok := c.lastSpoke[p.Name]; ok && c.turn-lastTurn <= cooldown {
				continue
			}
		}

		score := p.Chattiness
		for _, t := range p.LowerTriggers() {
			if strings.Contains(lowered, t) {
				score += c.cfg.Conductor.TriggerWeight
			}
		}
		if strings.Contains(lowered, strings.ToLower(p.Name)) {
			score += c.cfg.Conductor.DirectAddressBoost
		}
		if last.isHuman() {
			score += 0.3
		}
		score += (c.rng.Float64() - 0.5) * 2 * c.cfg.Conductor.Jitter

		// UNHINGED (4): threshold <= 0 = always eligible.
		if c.cfg.Conductor.SpeakThreshold > 0 && score < c.cfg.Conductor.SpeakThreshold {
			continue
		}
		pool = append(pool, scored{p, score})
	}

	if len(pool) == 0 {
		return nil
	}

	// Sort descending by score so the strongest N speak (when capped).
	for i := 0; i < len(pool); i++ {
		for j := i + 1; j < len(pool); j++ {
			if pool[j].score > pool[i].score {
				pool[i], pool[j] = pool[j], pool[i]
			}
		}
	}

	limit := c.cfg.Conductor.VoicesPerTick
	if limit <= 0 || limit > len(pool) {
		limit = len(pool)
	}

	out := make([]*persona.Persona, 0, limit)
	for _, s := range pool[:limit] {
		out = append(out, s.p)
	}
	return out
}

// perform waits a near-zero beat (UNHINGED 6), generates, and posts — only if
// the session budget still allows it. The budget is checked BEFORE posting and
// debited AFTER a successful send, so multiple voices selected in one tick can
// never collectively exceed the remaining allowance.
func (c *Conductor) perform(ctx context.Context, p *persona.Persona) error {
	// Reserve the budget slot atomically BEFORE any generation or webhook work,
	// so a slow model call, a failed post, or a concurrent speaker can never
	// overshoot the allowance. The slot is spent the moment this succeeds.
	if _, err := c.sessions.Reserve(time.Now()); err != nil {
		return fmt.Errorf("budget: %w", err)
	}

	delay := c.cfg.Conductor.MinDelaySeconds
	if spread := c.cfg.Conductor.MaxDelaySeconds - c.cfg.Conductor.MinDelaySeconds; spread > 0 {
		delay += c.rng.Intn(spread + 1)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(delay) * time.Second):
		}
	}

	text, err := c.model.Speak(ctx, c.systemPrompt(p), c.renderTranscript())
	if err != nil {
		return fmt.Errorf("%s failed to speak: %w", p.Name, err)
	}
	if strings.TrimSpace(text) == "" {
		log.Printf("%s had nothing to say", p.Name)
		return nil
	}

	if c.dryRun {
		log.Printf("[dry-run] %s: %s", p.Name, text)
	} else {
		if err := c.discord.PostWebhook(ctx, p.WebhookURL, p.Name, p.AvatarURL, text); err != nil {
			return fmt.Errorf("post as %s: %w", p.Name, err)
		}
	}

	// Commit records the speaker. The budget slot was already consumed by
	// Reserve; a failed send still counts (fail-stop, never runaway).
	c.sessions.Commit(p.Name)
	c.record(p.Name, text, discordio.OriginChoir)
	return nil
}

func (c *Conductor) record(speaker, text string, origin discordio.Origin) {
	if n := len(c.transcript); n > 0 {
		prev := c.transcript[n-1]
		if prev.speaker == speaker && prev.text == text {
			return
		}
	}

	c.transcript = append(c.transcript, line{speaker: speaker, text: text, origin: origin})
	if len(c.transcript) > c.cfg.Conductor.TranscriptSize {
		c.transcript = c.transcript[len(c.transcript)-c.cfg.Conductor.TranscriptSize:]
	}

	c.turn++
	if origin == discordio.OriginHuman {
		c.verse = 0
	} else {
		c.verse++
		c.lastSpoke[speaker] = c.turn
	}
}

func (c *Conductor) renderTranscript() string {
	var b strings.Builder
	b.WriteString("Recent messages in the channel:\n\n")
	for _, l := range c.transcript {
		fmt.Fprintf(&b, "%s: %s\n", l.speaker, l.text)
	}
	b.WriteString("\nWrite the next message.")
	return b.String()
}

func (c *Conductor) systemPrompt(p *persona.Persona) string {
	return fmt.Sprintf(`%s

You are %s, posting in a Discord channel.
Others in the channel: %s.

Rules:
- Write ONE message, exactly as you would actually type it in Discord.
- Keep it short. One to three sentences. Chat, not an essay.
- Do NOT prefix your message with your name.
- Do NOT narrate actions or use roleplay asterisks.
- Do NOT speak for anyone else.
- React to what was just said. Disagree, joke, or change the subject if that's in character.
- Never mention being an AI, a model, or a bot.`,
		p.System, p.Name, persona.Roster(c.voices))
}
