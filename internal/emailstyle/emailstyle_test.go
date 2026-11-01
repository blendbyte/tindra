package emailstyle

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	got := Expand(`color:__ACCENT__;background:__ACCENTSOFT__;__MONO__`)
	want := "color:" + Accent + ";background:" + AccentSoft + ";font-family:" + mono + ";"
	if got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}

func TestLayout(t *testing.T) {
	html, err := Layout("Subject <x>", template.HTML("<p>body</p>"), "https://tindra.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<title>Subject &lt;x&gt;</title>",
		"<p>body</p>",
		`src="https://tindra.example.com/assets/email-logo.png"`,
		`<a href="https://tindra.example.com"`,
		Hairline,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("layout missing %q", want)
		}
	}
	// Quoted font names must survive html/template's CSS sanitizer.
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("layout contains a sanitized value")
	}
}

func TestLayout_noPublicURL(t *testing.T) {
	html, err := Layout("t", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `<a href=""`) {
		t.Error("logo links to an empty URL")
	}
}

func TestSplitTitle(t *testing.T) {
	cases := []struct{ title, typ, msg string }{
		{"TypeError: Cannot read x", "TypeError", "Cannot read x"},
		{`App\Exceptions\PaymentException: declined`, `App\Exceptions\PaymentException`, "declined"},
		{"Payment failed: card declined", "", "Payment failed: card declined"},
		{"no colon here", "", "no colon here"},
		{": leading colon", "", ": leading colon"},
	}
	for _, c := range cases {
		typ, msg := SplitTitle(c.title)
		if typ != c.typ || msg != c.msg {
			t.Errorf("SplitTitle(%q) = %q, %q; want %q, %q", c.title, typ, msg, c.typ, c.msg)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[float64]string{0: "<1ms", 0.4: "<1ms", 75: "75ms", 999.4: "999ms", 1990: "1.99s", 5460: "5.46s", 127_400: "127.4s"}
	for ms, want := range cases {
		if got := FormatDuration(ms); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", ms, got, want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1200: "1.2k", 16940: "16.9k", 3_400_000: "3.4M"}
	for n, want := range cases {
		if got := FormatCount(n); got != want {
			t.Errorf("FormatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatSpan(t *testing.T) {
	cases := map[time.Duration]string{
		-time.Second:                  "0s",
		45 * time.Second:              "45s",
		26 * time.Minute:              "26m",
		3*time.Hour + 2*time.Minute:   "3h 2m",
		4 * time.Hour:                 "4h",
		50 * time.Hour:                "2d",
		26*time.Hour + 59*time.Minute: "1d",
	}
	for d, want := range cases {
		if got := FormatSpan(d); got != want {
			t.Errorf("FormatSpan(%v) = %q, want %q", d, got, want)
		}
	}
}
