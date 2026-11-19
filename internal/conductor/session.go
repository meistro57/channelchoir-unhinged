package conductor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
)

// Session models one bounded conversation. A session has a unique ID, an
// origin (who kicked it off), a hard message budget, a rolling per-minute
// window, and a runtime ceiling. It is the thing that eventually shuts the
// choir up — every post is debited from a session BEFORE it is sent, and once
// the budget is spent, no further generation happens until the configured
// reset condition (a fresh human message) starts a new session.
type Session struct {
	ID        string
	Origin    discordio.Origin
	StartedAt time.Time

	// Remaining is the messages left to generate in this session.
	Remaining int

	// minuteWindow tracks posts in the last 60s for the per-minute cap.
	minuteWindow []time.Time

	// lastSpeaker is the display name of the most recent speaker.
	lastSpeaker string

	// participants is the set of persona names that have spoken this session.
	participants map[string]bool
}

func newSession(origin discordio.Origin, started time.Time) *Session {
	return &Session{
		ID:           newSessionID(),
		Origin:       origin,
		StartedAt:    started,
		participants: map[string]bool{},
		minuteWindow: []time.Time{},
	}
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a clock-derived id; uniqueness is best-effort in tests.
		return fmt.Sprintf("cc-%d", time.Now().UnixNano())
	}
	return "cc-" + hex.EncodeToString(b)
}

// String returns a compact origin+id label for logs.
func (s *Session) String() string {
	return fmt.Sprintf("%s[%s]", s.Origin, s.ID)
}

// recordSpeaker marks a persona as having spoken in this session.
func (s *Session) recordSpeaker(name string) {
	s.lastSpeaker = name
	if name != "" {
		s.participants[name] = true
	}
}

// LastSpeaker returns the most recent speaker name.
func (s *Session) LastSpeaker() string { return s.lastSpeaker }

// Participants returns the persona names that spoke this session.
func (s *Session) Participants() []string {
	out := make([]string, 0, len(s.participants))
	for n := range s.participants {
		out = append(out, n)
	}
	return out
}

// SessionManager owns the active session and enforces the global budget across
// ALL personas. Because every voice shares one manager, "N voices selected in
// the same tick" can never collectively exceed the remaining allowance — the
// manager debits atomically before each post.
type SessionManager struct {
	cfg *config.Config

	mu      sync.Mutex
	current *Session

	// postedTotal counts every message actually posted this process run.
	postedTotal int
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{cfg: cfg}
}

// Active returns the current session (if any).
func (sm *SessionManager) Active() *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.current == nil {
		return nil
	}
	c := *sm.current
	return &c
}

// NoteInbound records an incoming message's origin and decides whether it
// starts/restarts a session. Choir messages NEVER start a session (an echo
// cannot mint a fresh unlimited budget). Human messages always (re)start. An
// external bot message starts a session only if there is none yet, so it does
// not reset an in-flight human-led conversation.
func (sm *SessionManager) NoteInbound(m discordio.Message, choir *discordio.ChoirWebhooks) {
	origin := discordio.Classify(m, choir)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Total message budget is a session affair: create a session lazily.
	if sm.current == nil {
		sm.current = newSession(origin, time.Now())
		sm.current.Remaining = sm.sessionBudget()
		return
	}

	switch origin {
	case discordio.OriginHuman:
		// Human intervention opens a fresh session with a fresh budget.
		sm.current = newSession(origin, time.Now())
		sm.current.Remaining = sm.sessionBudget()
	case discordio.OriginExternal:
		// External bots may join an ongoing session but do not reset it.
	case discordio.OriginChoir:
		// Our own echoes never open or extend a session.
	default:
	}
}

// sessionBudget returns the per-session message allowance. Explicit
// max_messages_per_session wins; otherwise the run-level cap is used.
func (sm *SessionManager) sessionBudget() int {
	if sm.cfg.Safeguards.MaxMessagesPerSession > 0 {
		return sm.cfg.Safeguards.MaxMessagesPerSession
	}
	return sm.cfg.Safeguards.MaxMessagesPerRun
}

// CanPost checks the kill switch and ALL budgets before a post. It returns an
// error naming which limit would be violated, or nil if the post is allowed.
// It is checked-before-post but does NOT debit; Debit must follow a successful
// send (or a dry-run equivalent) to keep the counters honest.
func (sm *SessionManager) CanPost(now time.Time) error {
	if sm.cfg.Safeguards.KillSwitchPath != "" && fileExists(sm.cfg.Safeguards.KillSwitchPath) {
		return fmt.Errorf("kill switch %q present", sm.cfg.Safeguards.KillSwitchPath)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.current == nil {
		return fmt.Errorf("no active session")
	}
	if sm.cfg.Safeguards.MaxMessagesPerRun > 0 && sm.postedTotal >= sm.cfg.Safeguards.MaxMessagesPerRun {
		return fmt.Errorf("hard run message cap reached (%d)", sm.cfg.Safeguards.MaxMessagesPerRun)
	}
	if sm.current.Remaining <= 0 {
		return fmt.Errorf("session message budget exhausted (%d)", sm.sessionBudget())
	}
	if sm.cfg.Safeguards.MaxRuntimeSeconds > 0 &&
		int(now.Sub(sm.current.StartedAt).Seconds()) >= sm.cfg.Safeguards.MaxRuntimeSeconds {
		return fmt.Errorf("session runtime cap reached (%ds)", sm.cfg.Safeguards.MaxRuntimeSeconds)
	}
	if sm.cfg.Safeguards.MaxMessagesPerMinute > 0 {
		// Drop timestamps older than 60s.
		cutoff := now.Add(-time.Minute)
		kept := sm.current.minuteWindow[:0]
		for _, t := range sm.current.minuteWindow {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		sm.current.minuteWindow = kept
		if len(kept) >= sm.cfg.Safeguards.MaxMessagesPerMinute {
			return fmt.Errorf("per-minute cap reached (%d/min)", sm.cfg.Safeguards.MaxMessagesPerMinute)
		}
	}
	return nil
}

// Debit records a successful (or dry-run) post against the session budget and
// the per-minute window. Call it after CanPost passes and the send succeeds.
func (sm *SessionManager) Debit(now time.Time, speaker string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.postedTotal++
	if sm.current != nil {
		if sm.current.Remaining > 0 {
			sm.current.Remaining--
		}
		sm.current.minuteWindow = append(sm.current.minuteWindow, now)
		sm.current.recordSpeaker(speaker)
	}
}

// PostedTotal reports how many messages have been debited this run.
func (sm *SessionManager) PostedTotal() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.postedTotal
}

// fileExists reports whether path exists (used for the kill-switch check).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
