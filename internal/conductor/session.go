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

// Reservation is a slot atomically claimed before any webhook or generation
// work begins. The slot is already spent the moment it is returned; it is never
// refunded on a failed or uncertain send.
type Reservation struct{}

// SessionManager enforces the PER-CHAMBER session budget (session message cap +
// runtime). It delegates the PROCESS-WIDE ceiling (total posts, per-minute
// window, kill switch) to a shared *Global, so multiple chambers stack their
// per-chamber limits on top of one common ceiling. A chamber can be stricter
// than the global cap, never looser.
type SessionManager struct {
	cfg    *config.Config
	global *Global

	mu      sync.Mutex
	current *Session

	// sessionBudgetOverride is a per-chamber session budget set via
	// SetSessionBudget (0 = use config-derived default).
	sessionBudgetOverride int
}

// NewSessionManager builds a single-chamber SessionManager with its OWN private
// Global (backward-compatible with the pre-multi-chamber API).
func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{cfg: cfg, global: NewGlobal(cfg)}
}

// NewSessionManagerShared builds a SessionManager that stacks its per-chamber
// session budget onto the given shared process-wide Global.
func NewSessionManagerShared(cfg *config.Config, g *Global) *SessionManager {
	return &SessionManager{cfg: cfg, global: g}
}

// newSessionManagerFor returns a manager backed by the first shared *Global in
// the variadic slice, or a private one when none is supplied.
func newSessionManagerFor(cfg *config.Config, shared []*Global) *SessionManager {
	if len(shared) > 0 && shared[0] != nil {
		return NewSessionManagerShared(cfg, shared[0])
	}
	return NewSessionManager(cfg)
}

// Global returns the shared ceiling backing this manager (never nil).
func (sm *SessionManager) Global() *Global { return sm.global }

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
// starts/restarts a session. Choir echoes NEVER start a session. A human message
// starts a fresh session (fresh budget + runtime clock) but leaves the global
// postedTotal and per-minute window untouched. External bots join but never
// reset.
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
	case discordio.OriginChoir:
	default:
	}
}

// sessionBudget returns the per-chamber session allowance. An explicit
// per-chamber override (via SessionBudget) wins, then max_messages_per_session,
// then the run-level cap.
func (sm *SessionManager) sessionBudget() int {
	if sm.cfg.Safeguards.MaxMessagesPerSession > 0 {
		return sm.cfg.Safeguards.MaxMessagesPerSession
	}
	return sm.cfg.Safeguards.MaxMessagesPerRun
}

// SetSessionBudget overrides the per-chamber session budget (0 restores the
// config-derived default). Used by chambers with a per-chamber limit.
func (sm *SessionManager) SetSessionBudget(n int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionBudgetOverride = n
}

// Reserve checks the per-chamber session budget, then consumes one slot from the
// SHARED global ceiling, then debits the per-chamber session. On error no slot
// is consumed.
func (sm *SessionManager) Reserve(now time.Time) (*Reservation, error) {
	// Atomically check + debit the PER-CHAMBER session budget under one lock
	// hold, so concurrent goroutines cannot all pass the Remaining check before
	// any debit lands (the TOCTOU race that must never reappear).
	sm.mu.Lock()
	if sm.current == nil {
		sm.mu.Unlock()
		return nil, fmt.Errorf("no active session")
	}
	if sm.current.Remaining <= 0 {
		budget := sm.sessionBudgetLocked()
		sm.mu.Unlock()
		return nil, fmt.Errorf("session message budget exhausted (%d)", budget)
	}
	if sm.cfg.Safeguards.MaxRuntimeSeconds > 0 &&
		int(now.Sub(sm.current.StartedAt).Seconds()) >= sm.cfg.Safeguards.MaxRuntimeSeconds {
		sm.mu.Unlock()
		return nil, fmt.Errorf("session runtime cap reached (%ds)", sm.cfg.Safeguards.MaxRuntimeSeconds)
	}
	sm.current.Remaining--
	sm.mu.Unlock()

	// Consume the SHARED global ceiling (kill switch, run cap, per-minute).
	if err := sm.global.Reserve(now); err != nil {
		// The global was the blocker: refund the per-chamber slot so a failing
		// global ceiling does not silently drain chamber budgets.
		sm.mu.Lock()
		if sm.current != nil {
			sm.current.Remaining++
		}
		sm.mu.Unlock()
		return nil, err
	}

	return &Reservation{}, nil
}

func (sm *SessionManager) CanPost(now time.Time) error {
	sm.mu.Lock()
	if sm.current == nil {
		sm.mu.Unlock()
		return fmt.Errorf("no active session")
	}
	if sm.current.Remaining <= 0 {
		budget := sm.sessionBudgetLocked()
		sm.mu.Unlock()
		return fmt.Errorf("session message budget exhausted (%d)", budget)
	}
	if sm.cfg.Safeguards.MaxRuntimeSeconds > 0 &&
		int(now.Sub(sm.current.StartedAt).Seconds()) >= sm.cfg.Safeguards.MaxRuntimeSeconds {
		sm.mu.Unlock()
		return fmt.Errorf("session runtime cap reached (%ds)", sm.cfg.Safeguards.MaxRuntimeSeconds)
	}
	sm.mu.Unlock()

	return sm.global.CanPost(now)
}

// Commit records the speaker of a previously-reserved slot.
func (sm *SessionManager) Commit(speaker string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.current != nil {
		sm.current.recordSpeaker(speaker)
	}
}

// PostedTotal reports how many global slots have been reserved this run.
func (sm *SessionManager) PostedTotal() int {
	return sm.global.PostedTotal()
}

func (sm *SessionManager) sessionBudgetLocked() int {
	if sm.sessionBudgetOverride > 0 {
		return sm.sessionBudgetOverride
	}
	if sm.cfg.Safeguards.MaxMessagesPerSession > 0 {
		return sm.cfg.Safeguards.MaxMessagesPerSession
	}
	return sm.cfg.Safeguards.MaxMessagesPerRun
}

// fileExists reports whether path exists (used for the kill-switch check).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
