package issues

import (
	"encoding/json"
	"strings"
	"testing"
)

func tagsToMap(tags [][2]string) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t[0]] = t[1]
	}
	return m
}

func TestExtractImplicitTags_topLevelFields(t *testing.T) {
	payload := json.RawMessage(`{
		"level": "error",
		"environment": "production",
		"transaction": "/api/users",
		"server_name": "web-01",
		"request": {"url": "https://example.com/api/users"}
	}`)

	tags := tagsToMap(extractImplicitTags(payload))

	cases := [][2]string{
		{"level", "error"},
		{"environment", "production"},
		{"transaction", "/api/users"},
		{"server_name", "web-01"},
		{"url", "https://example.com/api/users"},
	}
	for _, c := range cases {
		if got := tags[c[0]]; got != c[1] {
			t.Errorf("tag %q: got %q, want %q", c[0], got, c[1])
		}
	}
}

func TestExtractImplicitTags_contexts(t *testing.T) {
	payload := json.RawMessage(`{
		"contexts": {
			"browser": {"name": "Firefox", "version": "127.0"},
			"os":      {"name": "Linux", "version": "6.12.88", "build": "SMP Debian"},
			"runtime": {"name": "php", "version": "8.5.6"}
		}
	}`)

	tags := tagsToMap(extractImplicitTags(payload))

	cases := [][2]string{
		{"browser", "Firefox 127.0"},
		{"browser.name", "Firefox"},
		{"browser.version", "127.0"},
		{"os", "Linux 6.12.88"},
		{"os.name", "Linux"},
		{"os.version", "6.12.88"},
		{"os.build", "SMP Debian"},
		{"runtime", "php 8.5.6"},
		{"runtime.name", "php"},
		{"runtime.version", "8.5.6"},
	}
	for _, c := range cases {
		if got := tags[c[0]]; got != c[1] {
			t.Errorf("tag %q: got %q, want %q", c[0], got, c[1])
		}
	}
}

func TestExtractImplicitTags_contextNoVersion(t *testing.T) {
	payload := json.RawMessage(`{"contexts": {"client_os": {"name": "Ubuntu"}}}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["client_os"]; got != "Ubuntu" {
		t.Errorf("client_os: got %q, want Ubuntu", got)
	}
	if got := tags["client_os.name"]; got != "Ubuntu" {
		t.Errorf("client_os.name: got %q, want Ubuntu", got)
	}
}

func TestExtractImplicitTags_mechanismHandledFalse(t *testing.T) {
	handled := false
	_ = handled
	payload := json.RawMessage(`{
		"exception": {
			"values": [{"mechanism": {"type": "generic", "handled": false}}]
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["mechanism"]; got != "generic" {
		t.Errorf("mechanism: got %q, want generic", got)
	}
	if got := tags["handled"]; got != "no" {
		t.Errorf("handled: got %q, want no", got)
	}
}

func TestExtractImplicitTags_mechanismHandledTrue(t *testing.T) {
	payload := json.RawMessage(`{
		"exception": {
			"values": [{"mechanism": {"type": "user", "handled": true}}]
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["handled"]; got != "yes" {
		t.Errorf("handled: got %q, want yes", got)
	}
}

func TestExtractImplicitTags_emptyPayload(t *testing.T) {
	tags := extractImplicitTags(json.RawMessage(`{}`))
	if len(tags) != 0 {
		t.Errorf("expected no tags for empty payload, got %d", len(tags))
	}
}

func TestExtractImplicitTags_truncatesLongValues(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	payload, _ := json.Marshal(map[string]string{"transaction": string(long)})
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["transaction"]; len(got) != tagMaxLen {
		t.Errorf("expected truncation to %d chars, got %d", tagMaxLen, len(got))
	}
}

func TestExtractImplicitTags_truncatesLongUTF8Values(t *testing.T) {
	// "é" is a 2-byte rune; 250 of them exceed tagMaxLen in bytes but not in runes.
	// The truncation must count runes, not bytes.
	val := strings.Repeat("é", 250)
	payload, _ := json.Marshal(map[string]string{"transaction": val})
	tags := tagsToMap(extractImplicitTags(json.RawMessage(payload)))
	got := tags["transaction"]
	if len([]rune(got)) != tagMaxLen {
		t.Errorf("expected rune length %d after truncation, got %d", tagMaxLen, len([]rune(got)))
	}
}

func TestExtractImplicitTags_mechanismFromLastException(t *testing.T) {
	payload := json.RawMessage(`{
		"exception": {
			"values": [
				{"mechanism": {"type": "chained", "handled": true}},
				{"mechanism": {"type": "unhandled", "handled": false}}
			]
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["mechanism"]; got != "unhandled" {
		t.Errorf("mechanism: got %q, want unhandled (last exception)", got)
	}
	if got := tags["handled"]; got != "no" {
		t.Errorf("handled: got %q, want no (from last exception)", got)
	}
}

func TestMergeImplicitTags_explicitTakesPriority(t *testing.T) {
	explicit := [][2]string{{"level", "warning"}}
	implicit := [][2]string{{"level", "error"}, {"environment", "production"}}
	merged := tagsToMap(mergeImplicitTags(explicit, implicit))
	if merged["level"] != "warning" {
		t.Errorf("explicit level should win: got %q", merged["level"])
	}
	if merged["environment"] != "production" {
		t.Errorf("implicit environment should be added: got %q", merged["environment"])
	}
}

func TestMergeImplicitTags_nilExplicit(t *testing.T) {
	implicit := [][2]string{{"level", "error"}}
	merged := mergeImplicitTags(nil, implicit)
	if len(merged) != 1 || merged[0][1] != "error" {
		t.Errorf("unexpected merged result: %v", merged)
	}
}

func TestExtractImplicitTags_uaChrome(t *testing.T) {
	payload := json.RawMessage(`{
		"request": {
			"headers": {"User-Agent": ["Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"]}
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if tags["browser.name"] != "Chrome" {
		t.Errorf("browser.name: got %q, want Chrome", tags["browser.name"])
	}
	if tags["browser.version"] != "125.0" {
		t.Errorf("browser.version: got %q, want 125.0", tags["browser.version"])
	}
	if tags["os.name"] != "Windows" {
		t.Errorf("os.name: got %q, want Windows", tags["os.name"])
	}
	if tags["os.version"] != "10" {
		t.Errorf("os.version: got %q, want 10", tags["os.version"])
	}
}

func TestExtractImplicitTags_uaFirefoxLinux(t *testing.T) {
	payload := json.RawMessage(`{
		"request": {
			"headers": {"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0"}
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if tags["browser.name"] != "Firefox" {
		t.Errorf("browser.name: got %q, want Firefox", tags["browser.name"])
	}
	if tags["os.name"] != "Linux" {
		t.Errorf("os.name: got %q, want Linux", tags["os.name"])
	}
}

func TestExtractImplicitTags_uaSafariMac(t *testing.T) {
	payload := json.RawMessage(`{
		"request": {
			"headers": {"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"}
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if tags["browser.name"] != "Safari" {
		t.Errorf("browser.name: got %q, want Safari", tags["browser.name"])
	}
	if tags["os.name"] != "Mac OS X" {
		t.Errorf("os.name: got %q, want Mac OS X", tags["os.name"])
	}
	if tags["os.version"] != "14.5" {
		t.Errorf("os.version: got %q, want 14.5", tags["os.version"])
	}
}

func TestExtractImplicitTags_uaContextTakesPriority(t *testing.T) {
	// When SDK supplies explicit browser context, UA fallback must not override it.
	payload := json.RawMessage(`{
		"contexts": {
			"browser": {"name": "Firefox", "version": "127.0"}
		},
		"request": {
			"headers": {"User-Agent": "Mozilla/5.0 Chrome/125.0"}
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if tags["browser.name"] != "Firefox" {
		t.Errorf("context should win over UA: got %q, want Firefox", tags["browser.name"])
	}
}

func TestExtractImplicitTags_uaCrawler(t *testing.T) {
	// uap-go recognises non-standard browsers; previously hand-rolled regexes missed these.
	payload := json.RawMessage(`{
		"request": {
			"headers": {"User-Agent": "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (Chrome, like Gecko; compatible; CCM19 Cookie Crawler/2.1; +https://ccm19.de/)"}
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if tags["browser.name"] != "CCM19 Cookie Crawler" {
		t.Errorf("browser.name: got %q, want CCM19 Cookie Crawler", tags["browser.name"])
	}
	if tags["browser.version"] != "2.1" {
		t.Errorf("browser.version: got %q, want 2.1", tags["browser.version"])
	}
}

func TestExtractImplicitTags_uaAndroidiOS(t *testing.T) {
	cases := []struct {
		ua     string
		osName string
		osVer  string
	}{
		{
			"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/125.0",
			"Android", "14",
		},
		{
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15",
			"iOS", "17.4",
		},
	}
	for _, c := range cases {
		payloadStr, _ := json.Marshal(map[string]any{
			"request": map[string]any{
				"headers": map[string]string{"User-Agent": c.ua},
			},
		})
		tags := tagsToMap(extractImplicitTags(json.RawMessage(payloadStr)))
		if tags["os.name"] != c.osName {
			t.Errorf("ua=%q: os.name got %q, want %q", c.ua, tags["os.name"], c.osName)
		}
		if tags["os.version"] != c.osVer {
			t.Errorf("ua=%q: os.version got %q, want %q", c.ua, tags["os.version"], c.osVer)
		}
	}
}
