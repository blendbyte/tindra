package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime/quotedprintable"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/version"
)

// EmailMessage is the provider-agnostic envelope passed to an EmailSender.
// HTML is optional; when set, a multipart/alternative message is sent with
// Text as the plain-text fallback. If HTML is empty, only Text is sent.
type EmailMessage struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// EmailSender sends a single email. Implementations are safe for concurrent use.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}

// NewEmailSenderFromEnv builds an EmailSender from environment variables.
// Returns nil, nil if EMAIL_PROVIDER is not set - email alerts are disabled
// but the server still starts normally.
//
// Required env vars:
//
//	EMAIL_PROVIDER  smtp | postmark | brevo | lettermint | ahasend | cloudflare
//	EMAIL_FROM      sender address used for all outbound alerts
//
// Provider-specific vars (set only the ones needed for your chosen provider):
//
//	SMTP_HOST, SMTP_PORT (default 587), SMTP_USERNAME, SMTP_PASSWORD
//	POSTMARK_API_KEY
//	BREVO_API_KEY, EMAIL_FROM_NAME (optional display name)
//	LETTERMINT_API_KEY
//	AHASEND_API_KEY
//	CLOUDFLARE_EMAIL_API_TOKEN, CLOUDFLARE_ACCOUNT_ID
func NewEmailSenderFromEnv() (EmailSender, error) {
	provider := os.Getenv("EMAIL_PROVIDER")
	if provider == "" {
		return nil, nil
	}
	from := os.Getenv("EMAIL_FROM")
	if from == "" {
		return nil, fmt.Errorf("EMAIL_FROM must be set when EMAIL_PROVIDER is configured")
	}

	switch provider {
	case "smtp":
		port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
		if port == 0 {
			port = 587
		}
		return &smtpSender{cfg: smtpConfig{
			Host:     os.Getenv("SMTP_HOST"),
			Port:     port,
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			From:     from,
		}}, nil

	case "postmark":
		return &httpSender{
			endpoint: "https://api.postmarkapp.com/email",
			headers:  map[string]string{"X-Postmark-Server-Token": os.Getenv("POSTMARK_API_KEY")},
			from:     from,
			buildBody: func(from, to, subject, text, html string) any {
				m := map[string]string{
					"From": from, "To": to, "Subject": subject, "TextBody": text,
				}
				if html != "" {
					m["HtmlBody"] = html
				}
				return m
			},
		}, nil

	case "brevo":
		fromName := os.Getenv("EMAIL_FROM_NAME")
		if fromName == "" {
			fromName = from
		}
		return &httpSender{
			endpoint: "https://api.brevo.com/v3/smtp/email",
			headers:  map[string]string{"api-key": os.Getenv("BREVO_API_KEY")},
			from:     from,
			buildBody: func(from, to, subject, text, html string) any {
				m := map[string]any{
					"sender":      map[string]string{"email": from, "name": fromName},
					"to":          []map[string]string{{"email": to}},
					"subject":     subject,
					"textContent": text,
				}
				if html != "" {
					m["htmlContent"] = html
				}
				return m
			},
		}, nil

	case "lettermint":
		return &httpSender{
			endpoint: "https://api.lettermint.co/v1/send",
			headers:  map[string]string{"x-lettermint-token": os.Getenv("LETTERMINT_API_KEY")},
			from:     from,
			buildBody: func(from, to, subject, text, html string) any {
				m := map[string]any{
					"from": from, "to": []string{to}, "subject": subject, "text": text,
				}
				if html != "" {
					m["html"] = html
				}
				return m
			},
		}, nil

	case "ahasend":
		return &httpSender{
			endpoint: "https://api.ahasend.com/v1/email/send",
			headers:  map[string]string{"X-Api-Key": os.Getenv("AHASEND_API_KEY")},
			from:     from,
			buildBody: func(from, to, subject, text, html string) any {
				content := map[string]string{"subject": subject, "text_body": text}
				if html != "" {
					content["html_body"] = html
				}
				return map[string]any{
					"from":       map[string]string{"email": from},
					"recipients": []map[string]string{{"email": to}},
					"content":    content,
				}
			},
		}, nil

	case "cloudflare":
		accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
		return &httpSender{
			endpoint: fmt.Sprintf(
				"https://api.cloudflare.com/client/v4/accounts/%s/email/sending/send",
				accountID,
			),
			headers: map[string]string{
				"Authorization": "Bearer " + os.Getenv("CLOUDFLARE_EMAIL_API_TOKEN"),
			},
			from: from,
			buildBody: func(from, to, subject, text, html string) any {
				return map[string]string{
					"from": from, "to": to, "subject": subject, "text": text,
				}
			},
		}, nil
	}

	return nil, fmt.Errorf("unknown EMAIL_PROVIDER %q - valid values: smtp, postmark, brevo, lettermint, ahasend, cloudflare", provider)
}

// httpSender covers all HTTP-based email providers.
type httpSender struct {
	endpoint  string
	headers   map[string]string
	from      string
	buildBody func(from, to, subject, text, html string) any
	// client is nil in production (uses a default); injected in tests.
	client *http.Client
}

func (s *httpSender) Send(ctx context.Context, msg EmailMessage) error {
	body, err := json.Marshal(s.buildBody(s.from, msg.To, msg.Subject, msg.Text, msg.HTML))
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Tindra/"+version.App+" (+https://tindra.sh)")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	c := s.client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned %d", resp.StatusCode)
	}
	return nil
}

// smtpSender supports STARTTLS (port 587) and implicit TLS (port 465).
// Context cancellation is not propagated - a known limitation of the stdlib smtp package.
type smtpConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type smtpSender struct{ cfg smtpConfig }

func (s *smtpSender) Send(ctx context.Context, msg EmailMessage) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	tlsCfg := &tls.Config{ServerName: s.cfg.Host}

	var c *smtp.Client
	if s.cfg.Port == 465 {
		// Implicit TLS (SMTPS) - connect with TLS from the start.
		conn, err := (&tls.Dialer{Config: tlsCfg}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("tls dial: %w", err)
		}
		c, err = smtp.NewClient(conn, s.cfg.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
	} else {
		// STARTTLS (port 587) or plain (port 25).
		var err error
		c, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err = c.StartTLS(tlsCfg); err != nil {
				c.Close()
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}
	defer c.Close()

	// Only authenticate when credentials are provided AND the server advertises AUTH.
	if s.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
	}

	body := smtpBuildMessage(s.cfg.From, msg)

	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err = fmt.Fprint(wc, body); err != nil {
		wc.Close()
		return fmt.Errorf("write body: %w", err)
	}
	if err = wc.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return c.Quit()
}

// sanitizeHeader removes CR and LF characters from a header value to prevent
// header injection via SMTP.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
}

// qpEncode encodes s with quoted-printable so non-ASCII bytes survive SMTP transport.
func qpEncode(s string) string {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return buf.String()
}

// smtpBuildMessage constructs the RFC 2822 message written to the SMTP DATA command.
// When msg.HTML is set it produces a multipart/alternative message with a plain-text
// fallback; otherwise it sends a simple text/plain message.
func smtpBuildMessage(from string, msg EmailMessage) string {
	baseHeaders := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n",
		sanitizeHeader(from), sanitizeHeader(msg.To), sanitizeHeader(msg.Subject))

	if msg.HTML == "" {
		return baseHeaders +
			"Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
			qpEncode(msg.Text)
	}

	boundary := "=_tindra_" + strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "")
	var b strings.Builder
	b.WriteString(baseHeaders)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	// Plain-text part (first = lowest preference per RFC 2046).
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary)
	b.WriteString(qpEncode(msg.Text))
	b.WriteString("\r\n\r\n")

	// HTML part (last = highest preference).
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary)
	b.WriteString(qpEncode(msg.HTML))
	b.WriteString("\r\n\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}
