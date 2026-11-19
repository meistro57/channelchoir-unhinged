package conductor

import (
	"strings"
	"unicode"
)

// Dedup keeps a bounded ring of recently-posted (normalized) responses and
// reports whether a candidate line is a duplicate of something recently said,
// so identical/near-identical phrases cannot circulate between personas
// forever. The history is deliberately bounded (RecentResponses) — it prunes a
// phrase a short while after it stops being repeated, rather than banning it
// permanently.
type Dedup struct {
	history []string
	max     int
}

func NewDedup(max int) *Dedup {
	return &Dedup{max: max}
}

// normalize folds case, trims, and strips punctuation + collapse whitespace so
// "LOL!!" and "lol" compare equal.
func normalize(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// IsDuplicate reports whether s is identical or near-identical (normalized
// equality) to a recently-posted response. Always false when dedup is disabled.
func (d *Dedup) IsDuplicate(s string) bool {
	if d == nil || d.max <= 0 {
		return false
	}
	n := normalize(s)
	if n == "" {
		return false
	}
	for _, h := range d.history {
		if h == n {
			return true
		}
	}
	return false
}

// Record appends a successfully-posted line to the bounded history. It does NOT
// record duplicates (they are rejected before posting by IsDuplicate).
func (d *Dedup) Record(s string) {
	if d == nil || d.max <= 0 {
		return
	}
	n := normalize(s)
	if n == "" {
		return
	}
	// Drop the oldest if we would exceed the bound.
	if len(d.history) >= d.max {
		d.history = d.history[1:]
	}
	d.history = append(d.history, n)
}

// Len reports the current number of remembered responses.
func (d *Dedup) Len() int {
	if d == nil {
		return 0
	}
	return len(d.history)
}
