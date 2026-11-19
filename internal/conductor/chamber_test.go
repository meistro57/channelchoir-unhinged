package conductor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/meistro57/channelchoir/internal/config"
	"github.com/meistro57/channelchoir/internal/discordio"
)

// ValidateHandoff rejects chambers whose channel/guild is outside the
// allowlist or whose roster is empty.
func TestValidateHandoff(t *testing.T) {
	cfg := &config.Config{Discord: config.DiscordConfig{
		AllowedChannels: []string{"111"},
		AllowedGuilds:   []string{"999"},
	}}
	choir := discordio.NewChoirWebhooks(nil)

	// Valid.
	good := config.ChamberConfig{Name: "A", GuildID: "999", ChannelID: "111", Personas: []string{"troll"}}
	if err := ValidateHandoff(good, cfg, choir); err != nil {
		t.Fatalf("valid handoff rejected: %v", err)
	}

	// Channel outside allowlist.
	badChannel := good
	badChannel.ChannelID = "222"
	if err := ValidateHandoff(badChannel, cfg, choir); err == nil {
		t.Fatalf("channel outside allowlist must be rejected")
	}

	// Guild outside allowlist.
	badGuild := good
	badGuild.GuildID = "000"
	if err := ValidateHandoff(badGuild, cfg, choir); err == nil {
		t.Fatalf("guild outside allowlist must be rejected")
	}

	// Empty roster.
	empty := good
	empty.Personas = nil
	if err := ValidateHandoff(empty, cfg, choir); err == nil {
		t.Fatalf("empty roster must be rejected")
	}
}

// JSONL export records the required fields and never leaks webhook URLs or
// tokens.
func TestJSONLExporter_RecordsAndNoCredentials(t *testing.T) {
	var buf bytes.Buffer
	x := NewJSONLExporter(&buf)
	sessID := "cc-test-123"
	r := TranscriptRecord{
		SessionID:   sessID,
		ChamberID:   "111",
		ChamberName: "choir-test-cell",
		Timestamp:   time.Unix(1_700_000_000, 0),
		Speaker:     "Troll",
		Origin:      "choir",
		ChaosLevel:  3,
		MessageID:   "m1",
		ReplyToID:   "m0",
		Text:        "lol",
	}
	if err := x.Record(r); err != nil {
		t.Fatalf("record: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("decode JSONL line: %v", err)
	}

	// No credentials may appear.
	if strings.Contains(line, "discord.com/api/webhooks") || strings.Contains(line, "token") {
		t.Fatalf("JSONL line must not contain webhook URLs or tokens: %s", line)
	}

	// Required fields present.
	for _, k := range []string{"session_id", "chamber_id", "speaker", "origin", "chaos_level", "message_id", "text"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %q in JSONL record", k)
		}
	}
}

// Two chambers sharing one Global must collectively bound the process cap.
func TestChambers_ShareGlobalCeiling(t *testing.T) {
	cfg := &config.Config{Safeguards: config.SafeguardsConfig{MaxMessagesPerRun: 4, MaxMessagesPerSession: 100}}
	g := NewGlobal(cfg)

	smA := NewSessionManagerShared(cfg, g)
	smB := NewSessionManagerShared(cfg, g)
	smA.NoteInbound(humanMsg(), nil)
	smB.NoteInbound(humanMsg(), nil)

	total := 0
	// Hammer both chambers; only 4 total may succeed.
	for i := 0; i < 10; i++ {
		if _, err := smA.Reserve(time.Now()); err == nil {
			total++
		} else {
			break
		}
	}
	for i := 0; i < 10; i++ {
		if _, err := smB.Reserve(time.Now()); err == nil {
			total++
		} else {
			break
		}
	}

	if total != 4 {
		t.Fatalf("two chambers reserved %d total, want exactly 4 (shared ceiling)", total)
	}
}
