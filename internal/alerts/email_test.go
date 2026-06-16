package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/testutil"
)

// --- httpSender helpers ---

type capturedRequest struct {
	header http.Header
	body   []byte
}

// mockSender injects a test server into s, calls Send, and returns what the server received.
func mockSender(t *testing.T, statusCode int, s *httpSender) capturedRequest {
	t.Helper()
	var captured capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.header = r.Header.Clone()
		captured.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(statusCode)
	}))
	t.Cleanup(srv.Close)

	s.endpoint = srv.URL
	s.client = srv.Client()

	if err := s.Send(context.Background(), EmailMessage{
		To:      "to@example.com",
		Subject: "Test alert",
		Text:    "Something happened.",
	}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	return captured
}

// --- fakeSMTPServer alias (uses shared testutil implementation) ---

func newFakeSMTP(t *testing.T, advertiseAuth bool) *testutil.FakeSMTPServer {
	t.Helper()
	return testutil.NewFakeSMTP(t, advertiseAuth)
}

func newFakeSMTPFailAt(t *testing.T, failCmd string) *testutil.FakeSMTPServer {
	t.Helper()
	return testutil.NewFakeSMTPFailAt(t, failCmd)
}

// --- HTTP provider tests ---

func TestPostmarkSender(t *testing.T) {
	got := mockSender(t, 200, &httpSender{
		headers: map[string]string{"X-Postmark-Server-Token": "pk_test123"},
		from:    "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]string{"From": from, "To": to, "Subject": subject, "TextBody": text}
		},
	})

	if got.header.Get("X-Postmark-Server-Token") != "pk_test123" {
		t.Errorf("auth header: got %q", got.header.Get("X-Postmark-Server-Token"))
	}
	var payload map[string]string
	json.Unmarshal(got.body, &payload)
	if payload["TextBody"] != "Something happened." {
		t.Errorf("TextBody: got %q", payload["TextBody"])
	}
	if payload["From"] != "from@example.com" {
		t.Errorf("From: got %q", payload["From"])
	}
}

func TestBrevoSender(t *testing.T) {
	fromName := "Tindra"
	got := mockSender(t, 201, &httpSender{
		headers: map[string]string{"api-key": "brevo_key"},
		from:    "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]any{
				"sender":      map[string]string{"email": from, "name": fromName},
				"to":          []map[string]string{{"email": to}},
				"subject":     subject,
				"textContent": text,
			}
		},
	})

	if got.header.Get("api-key") != "brevo_key" {
		t.Errorf("auth header: got %q", got.header.Get("api-key"))
	}
	var payload map[string]any
	json.Unmarshal(got.body, &payload)
	if payload["textContent"] != "Something happened." {
		t.Errorf("textContent: got %v", payload["textContent"])
	}
	sender, ok := payload["sender"].(map[string]any)
	if !ok || sender["name"] != "Tindra" {
		t.Errorf("sender.name: got %v", payload["sender"])
	}
}

func TestLettermintSender(t *testing.T) {
	got := mockSender(t, 200, &httpSender{
		headers: map[string]string{"x-lettermint-token": "lm_key"},
		from:    "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]any{"from": from, "to": []string{to}, "subject": subject, "text": text}
		},
	})

	if got.header.Get("x-lettermint-token") != "lm_key" {
		t.Errorf("auth header: got %q", got.header.Get("x-lettermint-token"))
	}
	var payload map[string]any
	json.Unmarshal(got.body, &payload)
	toArr, ok := payload["to"].([]any)
	if !ok || len(toArr) != 1 {
		t.Errorf("to should be array with 1 element, got %v", payload["to"])
	}
}

func TestAhasendSender(t *testing.T) {
	got := mockSender(t, 201, &httpSender{
		headers: map[string]string{"X-Api-Key": "aha_key"},
		from:    "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]any{
				"from":       map[string]string{"email": from},
				"recipients": []map[string]string{{"email": to}},
				"content":    map[string]string{"subject": subject, "text_body": text},
			}
		},
	})

	if got.header.Get("X-Api-Key") != "aha_key" {
		t.Errorf("auth header: got %q", got.header.Get("X-Api-Key"))
	}
	var payload map[string]any
	json.Unmarshal(got.body, &payload)
	content, ok := payload["content"].(map[string]any)
	if !ok {
		t.Fatalf("expected content object, got %T", payload["content"])
	}
	if content["text_body"] != "Something happened." {
		t.Errorf("text_body: got %v", content["text_body"])
	}
	recipients, ok := payload["recipients"].([]any)
	if !ok || len(recipients) != 1 {
		t.Errorf("recipients: got %v", payload["recipients"])
	}
}

func TestCloudflareSender(t *testing.T) {
	got := mockSender(t, 200, &httpSender{
		headers: map[string]string{"Authorization": "Bearer cf_tok"},
		from:    "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]string{"from": from, "to": to, "subject": subject, "text": text}
		},
	})

	if got.header.Get("Authorization") != "Bearer cf_tok" {
		t.Errorf("expected Bearer auth, got %q", got.header.Get("Authorization"))
	}
	var payload map[string]string
	json.Unmarshal(got.body, &payload)
	if payload["text"] != "Something happened." {
		t.Errorf("text: got %q", payload["text"])
	}
}

func TestHTTPSender_non2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &httpSender{
		endpoint: srv.URL,
		client:   srv.Client(),
		headers:  map[string]string{"X-Postmark-Server-Token": "key"},
		from:     "f@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]string{"From": from, "To": to, "Subject": subject, "TextBody": text}
		},
	}

	err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"})
	if err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestHTTPSender_nilClient(t *testing.T) {
	// When client is nil, httpSender creates its own http.Client.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := &httpSender{
		endpoint: srv.URL,
		client:   nil, // exercises the nil-client branch
		from:     "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]string{"to": to}
		},
	}

	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err != nil {
		t.Errorf("expected no error with nil client, got: %v", err)
	}
}

func TestHTTPSender_connectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // close before Send so the connection is refused

	s := &httpSender{
		endpoint: url,
		client:   &http.Client{Timeout: 2 * time.Second},
		from:     "from@example.com",
		buildBody: func(from, to, subject, text, html string) any {
			return map[string]string{}
		},
	}

	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error for connection refused")
	}
}

// --- smtpBuildMessage tests ---

func TestSmtpBuildMessage_textOnly(t *testing.T) {
	msg := EmailMessage{To: "to@example.com", Subject: "Hello", Text: "plain body"}
	raw := smtpBuildMessage("from@example.com", msg)

	for _, want := range []string{
		"From: from@example.com",
		"To: to@example.com",
		"Subject: Hello",
		"Content-Type: text/plain",
		"plain body",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("expected %q in message, got:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "multipart") {
		t.Error("text-only message should not be multipart")
	}
}

func TestSmtpBuildMessage_withHTML(t *testing.T) {
	msg := EmailMessage{To: "to@example.com", Subject: "Hi", Text: "plain", HTML: "<b>bold</b>"}
	raw := smtpBuildMessage("from@example.com", msg)

	for _, want := range []string{
		"multipart/alternative",
		"text/plain",
		"text/html",
		"plain",
		"<b>bold</b>",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("expected %q in multipart message", want)
		}
	}
}

func TestSmtpBuildMessage_mimeVersion(t *testing.T) {
	raw := smtpBuildMessage("f@x.com", EmailMessage{To: "t@x.com", Subject: "s", Text: "t"})
	if !strings.Contains(raw, "MIME-Version: 1.0") {
		t.Error("expected MIME-Version header")
	}
}

func TestSmtpBuildMessage_emptyHTML(t *testing.T) {
	// HTML="" → same as text-only; no multipart boundary should appear.
	raw := smtpBuildMessage("f@x.com", EmailMessage{To: "t@x.com", Subject: "s", Text: "body", HTML: ""})
	if strings.Contains(raw, "boundary") {
		t.Error("empty HTML should produce a plain message without boundary")
	}
}

// --- smtpSender.Send tests (using fakeSMTPServer) ---

func TestSmtpSender_send_noAuth(t *testing.T) {
	srv := newFakeSMTP(t, false)

	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}

	if err := s.Send(context.Background(), EmailMessage{
		To:      "to@example.com",
		Subject: "Test Subject",
		Text:    "Hello world",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.Received()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message captured, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "Subject: Test Subject") {
		t.Errorf("subject missing from captured message:\n%s", msgs[0])
	}
	if !strings.Contains(msgs[0], "Hello world") {
		t.Errorf("body missing from captured message:\n%s", msgs[0])
	}
}

func TestSmtpSender_send_withAuth(t *testing.T) {
	srv := newFakeSMTP(t, true)

	s := &smtpSender{cfg: smtpConfig{
		Host:     "127.0.0.1",
		Port:     srv.Port(),
		Username: "user@example.com",
		Password: "secret",
		From:     "from@example.com",
	}}

	if err := s.Send(context.Background(), EmailMessage{
		To:      "to@example.com",
		Subject: "Auth Test",
		Text:    "Auth body",
	}); err != nil {
		t.Fatalf("Send with auth: %v", err)
	}

	msgs := srv.Received()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestSmtpSender_send_htmlMessage(t *testing.T) {
	srv := newFakeSMTP(t, false)

	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}

	if err := s.Send(context.Background(), EmailMessage{
		To:      "to@example.com",
		Subject: "HTML Test",
		Text:    "plain text",
		HTML:    "<b>bold</b>",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.Received()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	body := msgs[0]
	if !strings.Contains(body, "multipart/alternative") {
		t.Error("expected multipart message for HTML email")
	}
	if !strings.Contains(body, "<b>bold</b>") {
		t.Error("expected HTML content in message")
	}
	if !strings.Contains(body, "plain text") {
		t.Error("expected plain text in message")
	}
}

func TestSmtpSender_send_multipleMsgs(t *testing.T) {
	srv := newFakeSMTP(t, false)

	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}

	for i, subj := range []string{"First", "Second", "Third"} {
		if err := s.Send(context.Background(), EmailMessage{
			To:      "to@example.com",
			Subject: subj,
			Text:    fmt.Sprintf("body %d", i),
		}); err != nil {
			t.Fatalf("Send %q: %v", subj, err)
		}
	}

	msgs := srv.Received()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
}

// --- NewEmailSenderFromEnv provider construction tests ---

func TestNewEmailSenderFromEnv_nil(t *testing.T) {
	os.Unsetenv("EMAIL_PROVIDER")
	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Errorf("expected nil error when no provider set, got %v", err)
	}
	if s != nil {
		t.Error("expected nil sender when no provider set")
	}
}

func TestNewEmailSenderFromEnv_missingFrom(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "postmark")
	os.Unsetenv("EMAIL_FROM")
	_, err := NewEmailSenderFromEnv()
	if err == nil {
		t.Error("expected error when EMAIL_FROM is missing")
	}
}

func TestNewEmailSenderFromEnv_unknownProvider(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "nonsense")
	t.Setenv("EMAIL_FROM", "from@example.com")
	_, err := NewEmailSenderFromEnv()
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestNewEmailSenderFromEnv_smtp(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "smtp")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "user")
	t.Setenv("SMTP_PASSWORD", "pass")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := s.(*smtpSender)
	if !ok {
		t.Fatalf("expected *smtpSender, got %T", s)
	}
	if got.cfg.Host != "smtp.example.com" {
		t.Errorf("host: got %q", got.cfg.Host)
	}
	if got.cfg.Port != 587 {
		t.Errorf("port: got %d", got.cfg.Port)
	}
}

func TestNewEmailSenderFromEnv_smtp_defaultPort(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "smtp")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	os.Unsetenv("SMTP_PORT")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := s.(*smtpSender)
	if got.cfg.Port != 587 {
		t.Errorf("expected default port 587, got %d", got.cfg.Port)
	}
}

func TestNewEmailSenderFromEnv_postmark(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "postmark")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("POSTMARK_API_KEY", "pm_key")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*httpSender); !ok {
		t.Fatalf("expected *httpSender for postmark, got %T", s)
	}
}

func TestNewEmailSenderFromEnv_brevo(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "brevo")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("BREVO_API_KEY", "br_key")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*httpSender); !ok {
		t.Fatalf("expected *httpSender for brevo, got %T", s)
	}
}

func TestNewEmailSenderFromEnv_lettermint(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "lettermint")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("LETTERMINT_API_KEY", "lm_key")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*httpSender); !ok {
		t.Fatalf("expected *httpSender for lettermint, got %T", s)
	}
}

func TestNewEmailSenderFromEnv_ahasend(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "ahasend")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("AHASEND_API_KEY", "aha_key")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*httpSender); !ok {
		t.Fatalf("expected *httpSender for ahasend, got %T", s)
	}
}

func TestNewEmailSenderFromEnv_cloudflare(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "cloudflare")
	t.Setenv("EMAIL_FROM", "from@example.com")
	t.Setenv("CLOUDFLARE_EMAIL_API_TOKEN", "cf_tok")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "acct123")

	s, err := NewEmailSenderFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := s.(*httpSender)
	if !ok {
		t.Fatalf("expected *httpSender for cloudflare, got %T", s)
	}
	if !strings.Contains(got.endpoint, "acct123") {
		t.Errorf("expected account ID in endpoint, got %q", got.endpoint)
	}
}

// --- sanitizeHeader and qpEncode direct tests ---

func TestSanitizeHeader_removesNewlines(t *testing.T) {
	got := sanitizeHeader("Subject\r\nX-Injected: evil")
	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Errorf("CR/LF should be stripped, got: %q", got)
	}
	if got != "SubjectX-Injected: evil" {
		t.Errorf("got %q, want %q", got, "SubjectX-Injected: evil")
	}
}

func TestSanitizeHeader_crOnly(t *testing.T) {
	got := sanitizeHeader("foo\rbar")
	if got != "foobar" {
		t.Errorf("CR should be stripped, got %q", got)
	}
}

func TestSanitizeHeader_cleanInput(t *testing.T) {
	got := sanitizeHeader("clean@example.com")
	if got != "clean@example.com" {
		t.Errorf("clean input should be unchanged, got %q", got)
	}
}

func TestQpEncode_nonASCII(t *testing.T) {
	input := "héllo wörld"
	got := qpEncode(input)
	if got == input {
		t.Error("non-ASCII input should be encoded, not passed through raw")
	}
	if got == "" {
		t.Error("encoded output should not be empty")
	}
}

// --- smtpSender error path tests ---

func TestSmtpSender_dialFailure(t *testing.T) {
	// Port 1 is reserved and nothing listens on it in test environments.
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: 1,
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error when SMTP server unreachable")
	}
}

func TestSmtpSender_mailFromRejected(t *testing.T) {
	srv := newFakeSMTPFailAt(t, "MAIL")
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error when MAIL FROM is rejected")
	}
}

func TestSmtpSender_rcptToRejected(t *testing.T) {
	srv := newFakeSMTPFailAt(t, "RCPT")
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error when RCPT TO is rejected")
	}
}

func TestSmtpSender_port465DialFailure(t *testing.T) {
	// Nothing listens on 127.0.0.1:465 in CI; port 465 forces the implicit-TLS
	// branch so tls.Dialer.DialContext fails, covering the "tls dial: …" return.
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: 465,
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected tls dial error for port 465 with no server")
	}
}

func TestSmtpSender_starttlsFailure(t *testing.T) {
	// FakeSMTP advertises STARTTLS and responds "220 Ready" then closes the
	// connection, so the client's StartTLS handshake fails — covers "starttls: …".
	srv := testutil.NewFakeSMTPWithSTARTTLS(t)
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected starttls error when server drops connection after 220 Ready")
	}
}

func TestSmtpSender_dataRejected(t *testing.T) {
	// FakeSMTP rejects the DATA command with 550, covering the "DATA: …" return.
	srv := newFakeSMTPFailAt(t, "DATA")
	s := &smtpSender{cfg: smtpConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
		From: "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error when DATA command is rejected")
	}
}

func TestSmtpSender_authRejected(t *testing.T) {
	// FakeSMTP advertises AUTH and rejects credentials with 535, covering "auth: …".
	srv := testutil.NewFakeSMTP(t, true)
	srv.FailCmd = "AUTH"
	s := &smtpSender{cfg: smtpConfig{
		Host:     "127.0.0.1",
		Port:     srv.Port(),
		Username: "user@example.com",
		Password: "wrong",
		From:     "from@example.com",
	}}
	if err := s.Send(context.Background(), EmailMessage{To: "t@x.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("expected error when AUTH credentials are rejected")
	}
}
