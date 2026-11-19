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

// Session models one bounded conversation. It has a unique ID, an origin, a
// hard message budget, and a runtime ceiling. Only the SESSION-level budget and
// runtime clock reset on a fresh human message; the PROCESS-WIDE message
// ceiling and per-minute rate limiter never reset, so no amount of human
// chatter can bypass them.
type Session struct {
	ID        string
	Origin    discordio.Origin
	StartedAt time.Time

	// Remaining is the messages left to generate in this session. A genuine
	// human message may replenish it.
	Remaining int

	lastSpeaker  string
	participants map[string]bool
}

func newSession(origin discordio.Origin, started time.Time) *Session {
	return &Session{
		ID:           newSessionID(),
		Origin:       origin,
		StartedAt:    started,
		participants: map[string]bool{},
	}
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("cc-%d", time.Now().UnixNano())
	}
	return "cc-" + hex.EncodeToString(b)
}

func (s *Session) String() string { return fmt.Sprintf("%s[%s]", s.Origin, s.ID) }

func (s *Session) recordSpeaker(name string) {
	s.lastSpeaker = name
	if name != "" {
		s.participants[name] = true
	}
}

func (s *Session) LastSpeaker() string { return s.lastSpeaker }

func (s *Session) Participants() []string {
	out := make([]string, 0, len(s.participants))
	for n := range s.participants {
		out = append(out, n)
	}
	return out
}

// Reservation is a slot atomically claimed from the global budget BEFORE any
// webhook or generation work begins. The slot is already spent the moment it
// is returned; it is never refunded on a failed or uncertain send, so failures
// can only stop the choir sooner, never let it run away.
type Reservation struct{}

// SessionManager owns the active session and enforces the global budget across
// ALL personas. One shared manager means N voices selected in the same tick can
// never collectively exceed the remaining allowance.
//
// Limiting layers, in order:
//  1. kill switch file              — external, checked on every reserve.
//  2. postedTotal vs max_messages_per_run — PROCESS-wide, never resets.
//  3. session.Remaining              — resets only on a genuine human message.
//  4. per-minute window             — PROCESS-wide, never resets.
//  5. session runtime               — resets only on a genuine human message.
//
// Layers 2 and 4 cannot be reset by human messages: a burst of "hello" cannot
// mint fresh budget or rate allowance.
type SessionManager struct {
	cfg *config.Config

	mu      sync.Mutex
	current *Session

	// postedTotal counts every reserved slot this process run. Monotonic; never
	// reset by NoteInbound.
	postedTotal int

	// minuteWindow is a PROCESS-wide rolling window; never reset, so rapid human
	// messages cannot reset the per-minute rate cap.
	minuteWindow []time.Time
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{cfg: cfg}
}

// Active returns a copy of the current session (nil if none).
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
// starts/restarts a session. Choir echoes NEVER start a session (an echo cannot
// mint a fresh unlimited budget). A human message starts a fresh session (fresh
// session budget + runtime clock) but leaves the process-wide postedTotal and
// per-minute window untouched — so it cannot bypass them. External bots may
// join an ongoing session but never reset it.
func (sm *SessionManager) NoteInbound(m discordio.Message, choir *discordio.ChoirWebhooks) {
	origin := discordio.Classify(m, choir)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.current == nil {
		sm.current = newSession(origin, time.Now())
		sm.current.Remaining = sm.sessionBudget()
		return
	}

	switch origin {
	case discordio.OriginHuman:
		sm.current = newSession(origin, time.Now())
		sm.current.Remaining = sm.sessionBudget()
	case discordio.OriginExternal:
		// Join, do not reset.
	case discordio.OriginChoir:
		// Never open or extend a session.
	default:
	}
}

// sessionBudget returns the per-session message allowance. An explicit
// max_messages_per_session wins; otherwise the run-level cap is used.
func (sm *SessionManager) sessionBudget() int {
	if sm.cfg.Safeguards.MaxMessagesPerSession > 0 {
		return sm.cfg.Safeguards.MaxMessagesPerSession
	}
	return sm.cfg.Safeguards.MaxMessagesPerRun
}

// Reserve atomically checks every limit and, if all pass, consumes one slot
// from the global budget BEFORE any webhook or generation work starts. On
// success the caller owns (and has already spent) the slot. On error no slot
// is consumed and the returned Reservation is nil.
//
// The slot is intentionally NOT refunded on a failed send: the attempt counts,
// keeping the process-wide cap monotonic.
func (sm *SessionManager) Reserve(now time.Time) (*Reservation, error) {
	if err := sm.check(now, true); err != nil {
		return nil, err
	}
	return &Reservation{}, nil
}

// CanPost is a non-consuming probe: it checks every limit without spending a
// slot. Used by tests to assert a post WOULD be allowed. The live run path uses
// Reserve.
func (sm *SessionManager) CanPost(now time.Time) error {
	return sm.check(now, false)
}

// check evaluates every limit under the mutex and, when consume is true, spends
// a slot atomically. It is the single source of truth for both Reserve and
// CanPost, so the probe and the real path can never drift.
func (sm *SessionManager) check(now time.Time, consume bool) error {
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
		cutoff := now.Add(-time.Minute)
		kept := sm.minuteWindow[:0]
		for _, t := range sm.minuteWindow {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		sm.minuteWindow = kept
		if len(kept) >= sm.cfg.Safeguards.MaxMessagesPerMinute {
			return fmt.Errorf("per-minute cap reached (%d/min)", sm.cfg.Safeguards.MaxMessagesPerMinute)
		}
	}

	if consume {
		sm.postedTotal++
		sm.current.Remaining--
		sm.minuteWindow = append(sm.minuteWindow, now)
	}
	return nil
}

// Commit records the speaker of a previously-reserved slot. The budget was
// already consumed by Reserve; Commit only tracks participation.
func (sm *SessionManager) Commit(speaker string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.current != nil {
		sm.current.recordSpeaker(speaker)
	}
}

// PostedTotal reports how many slots have been reserved this run.
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
