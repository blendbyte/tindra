package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/pquerna/otp/totp"
)

func TestTOTPIssuer(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		want      string
	}{
		{"empty falls back", "", "Tindra"},
		{"whitespace only falls back", "   ", "Tindra"},
		{"https url", "https://tindra.example.com", "tindra.example.com"},
		{"http url", "http://tindra.example.com", "tindra.example.com"},
		{"trailing slash", "https://tindra.example.com/", "tindra.example.com"},
		{"with path", "https://example.com/tindra", "example.com"},
		{"with port", "https://tindra.example.com:8443", "tindra.example.com"},
		{"with port and path", "https://tindra.example.com:8443/app", "tindra.example.com"},
		{"surrounding whitespace", "  https://tindra.example.com  ", "tindra.example.com"},
		{"scheme-less host", "tindra.example.com", "tindra.example.com"},
		{"scheme-less host with port", "tindra.example.com:8443", "tindra.example.com"},
		{"scheme-less host with path", "tindra.example.com/app", "tindra.example.com"},
		{"userinfo is dropped", "https://user:pass@tindra.example.com", "tindra.example.com"},
		{"ipv6 literal falls back", "http://[::1]:9000", "Tindra"},
		{"ipv4 literal is kept", "http://192.168.1.5:8080", "192.168.1.5"},
		{"localhost with port", "http://localhost:8080", "localhost"},
		{"query string", "https://tindra.example.com?a=b", "tindra.example.com"},
		{"scheme only falls back", "https://", "Tindra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := totpIssuer(tt.publicURL); got != tt.want {
				t.Errorf("totpIssuer(%q) = %q, want %q", tt.publicURL, got, tt.want)
			}
		})
	}
}

// The issuer must never contain the characters that delimit an otpauth label,
// otherwise the URI splits into the wrong issuer/account pair.
func TestTOTPIssuer_neverContainsLabelDelimiters(t *testing.T) {
	inputs := []string{
		"",
		"https://tindra.example.com",
		"https://tindra.example.com:8443/app?x=1",
		"http://user:pass@[::1]:9000/deep/path",
		"tindra.example.com:8443",
		"///",
		"::::",
	}

	for _, in := range inputs {
		got := totpIssuer(in)
		if got == "" {
			t.Errorf("totpIssuer(%q) returned an empty issuer", in)
		}
		if strings.ContainsAny(got, ":/") {
			t.Errorf("totpIssuer(%q) = %q, must not contain ':' or '/'", in, got)
		}
	}
}

// Regression test for the malformed URI reported in discussion #12: using
// PUBLIC_URL verbatim produced "otpauth://totp/https://host:user@example.com",
// which authenticator apps mis-split or reject.
func TestTOTPIssuer_producesParseableOTPAuthURI(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer("https://tindra.example.com"),
		AccountName: "john@example.com",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	uri := key.URL()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse %q: %v", uri, err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("expected otpauth://totp, got scheme %q host %q", u.Scheme, u.Host)
	}

	label := strings.TrimPrefix(u.Path, "/")
	if strings.Contains(label, "/") {
		t.Errorf("label %q contains a slash, URI is %q", label, uri)
	}
	issuer, account, found := strings.Cut(label, ":")
	if !found {
		t.Fatalf("label %q has no issuer:account delimiter", label)
	}
	if issuer != "tindra.example.com" {
		t.Errorf("issuer = %q, want %q", issuer, "tindra.example.com")
	}
	if account != "john@example.com" {
		t.Errorf("account = %q, want %q", account, "john@example.com")
	}
	if strings.Contains(account, ":") {
		t.Errorf("account %q contains a stray colon", account)
	}
	if q := u.Query().Get("issuer"); q != "tindra.example.com" {
		t.Errorf("issuer query param = %q, want %q", q, "tindra.example.com")
	}
	if u.Query().Get("secret") == "" {
		t.Error("expected a secret query param")
	}
}
