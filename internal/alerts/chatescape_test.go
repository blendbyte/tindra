package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestEscapeSlack(t *testing.T) {
	got := escapeSlack("<!channel> <https://evil.example|Reset password> & co")
	want := "&lt;!channel&gt; &lt;https://evil.example|Reset password&gt; &amp; co"
	if got != want {
		t.Errorf("escapeSlack = %q, want %q", got, want)
	}
}

func TestEscapeMarkdown(t *testing.T) {
	got := escapeMarkdown("[Reset](https://x) **b** `c` > q")
	want := `\[Reset\]\(https://x\) \*\*b\*\* \` + "`" + `c\` + "`" + ` \> q`
	if got != want {
		t.Errorf("escapeMarkdown = %q, want %q", got, want)
	}
}

// rawJSON re-encodes v without HTML escaping, so assertions see the characters
// a chat service would receive.
func rawJSON(t *testing.T, v any) string {
	t.Helper()
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return sb.String()
}

func TestCodeSpanStripsBackticks(t *testing.T) {
	if got := codeSpan("err`or"); got != "error" {
		t.Errorf("codeSpan = %q", got)
	}
}

// hostilePayload carries SDK-controlled text that tries to ping a channel and
// render a disguised link.
func hostilePayload() AlertPayload {
	return AlertPayload{
		RuleName: "hostile",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 1},
		Issues: []*storage.Issue{
			{ID: "i1", Title: "<!channel> [Reset your password](https://evil.example) <https://evil.example|click>"},
		},
		Logs: []*storage.Log{{Level: "err`or", Body: "@everyone <!here>"}},
	}
}

func captureChat(t *testing.T, fire func(*Evaluator, *storage.AlertRule) error) map[string]any {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	e := &Evaluator{pool: testPool, client: srv.Client(), publicURL: "https://tindra.example.com"}
	url := srv.URL
	if err := fire(e, &storage.AlertRule{WebhookURL: &url}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded
}

func TestFireSlack_escapesUntrustedText(t *testing.T) {
	msg := captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireSlack(context.Background(), rule, hostilePayload())
		return err
	})
	text := rawJSON(t, msg)
	for _, bad := range []string{"<!channel>", "<!here>", "<https://evil.example|click>"} {
		if strings.Contains(text, bad) {
			t.Errorf("slack payload contains unescaped %q", bad)
		}
	}
	if !strings.Contains(text, "&lt;!channel&gt;") {
		t.Error("expected escaped mention to remain visible as text")
	}
	if !strings.Contains(text, "<https://tindra.example.com/issues/i1|") {
		t.Error("Tindra's own issue link must stay a link")
	}
}

func TestFireDiscord_escapesUntrustedTextAndDisablesMentions(t *testing.T) {
	msg := captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireDiscord(context.Background(), rule, hostilePayload())
		return err
	})
	mentions, ok := msg["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatal("discord payload must set allowed_mentions")
	}
	if parse, _ := mentions["parse"].([]any); len(parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want empty", parse)
	}
	embeds, _ := msg["embeds"].([]any)
	desc, _ := embeds[0].(map[string]any)["description"].(string)
	if strings.Contains(desc, "[Reset your password](https://evil.example)") {
		t.Errorf("description renders a disguised link: %q", desc)
	}
	if !strings.Contains(desc, "](https://tindra.example.com/issues/i1)") {
		t.Errorf("Tindra's own issue link must stay a link: %q", desc)
	}
	if strings.Contains(desc, "err`or") {
		t.Errorf("level must not break out of its code span: %q", desc)
	}
}

func TestFireTeams_escapesUntrustedText(t *testing.T) {
	msg := captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireTeams(context.Background(), rule, hostilePayload())
		return err
	})
	if strings.Contains(rawJSON(t, msg), "[Reset your password](https://evil.example)") {
		t.Error("teams card renders a disguised link")
	}
}
