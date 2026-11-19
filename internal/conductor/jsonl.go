package conductor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/meistro57/channelchoir-unhinged/internal/discordio"
)

// TranscriptRecord is one exported message line. It carries NO credentials:
// no bot token, no webhook URL, no model secrets.
type TranscriptRecord struct {
	SessionID   string    `json:"session_id"`
	ChamberID   string    `json:"chamber_id"`
	ChamberName string    `json:"chamber_name"`
	Timestamp   time.Time `json:"timestamp"`
	Speaker     string    `json:"speaker"`
	Origin      string    `json:"origin"`
	ChaosLevel  int       `json:"chaos_level"`
	MessageID   string    `json:"message_id,omitempty"`
	ReplyToID   string    `json:"reply_to_id,omitempty"`
	Text        string    `json:"text"`
}

type JSONLExporter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func NewJSONLExporter(w io.Writer) *JSONLExporter {
	return &JSONLExporter{w: w}
}

func OpenTranscriptFile(path string) (*JSONLExporter, func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONLExporter(f), f.Close, nil
}

func (x *JSONLExporter) Record(r TranscriptRecord) error {
	if r.Origin == "" {
		r.Origin = "unknown"
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, byte(10))

	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return fmt.Errorf("exporter closed")
	}
	_, err = x.w.Write(b)
	return err
}

func (x *JSONLExporter) Close() {
	x.mu.Lock()
	x.closed = true
	x.mu.Unlock()
}

func originString(o discordio.Origin) string {
	switch o {
	case discordio.OriginHuman:
		return "human"
	case discordio.OriginChoir:
		return "choir"
	case discordio.OriginExternal:
		return "external"
	default:
		return "unknown"
	}
}
