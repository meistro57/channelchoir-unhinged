package discordio

import "regexp"

// Origin classifies where a message came from. The conductor uses this to
// decide whether a message may (re)start a conversation and whether the choir
// is allowed to reply to it.
//
//   - Human: a real person (not a bot, not a webhook).
//   - Choir: one of our own webhooks (the voices talking to each other).
//   - External: any other bot or a webhook we do not own.
type Origin int

const (
	OriginUnknown Origin = iota
	OriginHuman
	OriginChoir
	OriginExternal
)

func (o Origin) String() string {
	switch o {
	case OriginHuman:
		return "human"
	case OriginChoir:
		return "choir"
	case OriginExternal:
		return "external"
	default:
		return "unknown"
	}
}

// webhookIDRe pulls the numeric webhook id out of a Discord webhook URL of the
// form https://discord.com/api/webhooks/{id}/{token}.
var webhookIDRe = regexp.MustCompile(`/api/webhooks/([0-9]+)/`)

// WebhookIDFromURL returns the webhook id embedded in u, or "" if u is not a
// recognisable Discord webhook URL.
func WebhookIDFromURL(u string) string {
	m := webhookIDRe.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

// ChoirWebhooks is the set of webhook ids the choir owns (one per voice).
// Build it once before the run starts; it is read-only afterward.
type ChoirWebhooks struct {
	ids map[string]struct{}
}

func NewChoirWebhooks(webhookURLs []string) *ChoirWebhooks {
	c := &ChoirWebhooks{ids: map[string]struct{}{}}
	for _, u := range webhookURLs {
		if id := WebhookIDFromURL(u); id != "" {
			c.ids[id] = struct{}{}
		}
	}
	return c
}

func (c *ChoirWebhooks) Owns(webhookID string) bool {
	_, ok := c.ids[webhookID]
	return ok
}

func (c *ChoirWebhooks) Len() int { return len(c.ids) }

// Classify assigns an Origin to a message given the set of choir webhooks.
func Classify(m Message, choir *ChoirWebhooks) Origin {
	switch {
	case m.WebhookID == "" && !m.Author.Bot:
		return OriginHuman
	case m.WebhookID != "" && choir != nil && choir.Owns(m.WebhookID):
		return OriginChoir
	case m.WebhookID != "" || m.Author.Bot:
		return OriginExternal
	default:
		return OriginUnknown
	}
}
