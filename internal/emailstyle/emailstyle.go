// Package emailstyle holds what every Tindra email shares with the web app:
// its colours, type, layout shell and the way it prints titles, durations and
// counts. Mail clients do not run the app's CSS (no custom properties, no
// oklch, often no web fonts), so the light theme's tokens live here as hex and
// templates reference them through placeholders that Expand fills in before
// parsing.
package emailstyle

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"
)

// The web app's light theme (web/src/assets/styles.css), converted from oklch.
// Emails are read on white, so they follow the light tokens.
const (
	Text1    = "#151520" // --text-1
	Text2    = "#565667" // --text-2
	Text3    = "#676777" // --text-3
	Accent   = "#6351ca" // --accent
	Danger   = "#d00021" // --danger
	Warning  = "#b55700" // --warning
	Success  = "#007a3a" // --success
	Info     = "#007fbc" // --info
	Hairline = "#e4e4eb" // --border-soft
	Surface  = "#f3f3f7" // --surface
	// Tints for chips and passing ticks, mixed on white.
	AccentSoft = "#efeefa"
	PassTick   = "#73b693"
)

// Font stacks. The app's Hanken Grotesk and Geist Mono are not available in
// most mail clients, so emails use the closest system faces.
const (
	sans = `-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif`
	mono = `ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace`
)

// Expand replaces style placeholders in a template source. It runs before
// template.Parse, so html/template's CSS sanitizer never sees quoted font
// names as template output.
//
//	__SANS__ / __MONO__    a complete font-family declaration
//	__TEXT1__ … __PASSTICK__ a colour from the palette above
func Expand(src string) string {
	return strings.NewReplacer(
		"__SANS__", "font-family:"+sans+";",
		"__MONO__", "font-family:"+mono+";",
		"__TEXT1__", Text1,
		"__TEXT2__", Text2,
		"__TEXT3__", Text3,
		"__ACCENTSOFT__", AccentSoft,
		"__ACCENT__", Accent,
		"__DANGER__", Danger,
		"__WARNING__", Warning,
		"__SUCCESS__", Success,
		"__HAIRLINE__", Hairline,
		"__SURFACE__", Surface,
		"__PASSTICK__", PassTick,
	).Replace(src)
}

var layoutTmpl = template.Must(template.New("layout").Parse(Expand(
	`<!DOCTYPE html>
<html lang="en" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<!--[if mso]>
<xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml>
<![endif]-->
<title>{{.Title}}</title>
<style>
  body,table,td,a{-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%}
  table,td{mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse}
  img{border:0;height:auto;line-height:100%;outline:none;text-decoration:none;-ms-interpolation-mode:bicubic}
  a[x-apple-data-detectors]{color:inherit!important;text-decoration:none!important}
</style>
</head>
<body style="margin:0;padding:0;background-color:__SURFACE__;word-spacing:normal;">
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="background-color:__SURFACE__;">
<tr><td align="center" style="padding:28px 12px;">

  <!--[if (gte mso 9)|(IE)]>
  <table width="640" align="center" cellpadding="0" cellspacing="0" role="presentation"><tr><td>
  <![endif]-->
  <table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="max-width:640px;margin:0 auto;">

    <tr><td style="background-color:#ffffff;border:1px solid __HAIRLINE__;border-radius:12px;padding:28px 28px 24px;">
      <div style="margin-bottom:28px;">
        {{if .PublicURL}}<a href="{{.PublicURL}}" style="border:0;text-decoration:none;">{{end}}<img src="{{.LogoSrc}}" width="112" alt="Tindra" style="border:0;display:block;width:112px;height:auto;max-width:100%;">{{if .PublicURL}}</a>{{end}}
      </div>
      {{.Body}}
    </td></tr>

    <tr><td style="padding-top:20px;text-align:center;font-size:12px;color:__TEXT3__;line-height:1.6;__SANS__">
      <a href="https://tindra.sh" style="color:__TEXT3__;text-decoration:none;">tindra.sh</a>
      &nbsp;&middot;&nbsp;
      <a href="https://github.com/blendbyte/tindra" style="color:__TEXT3__;text-decoration:none;">GitHub</a>
    </td></tr>

  </table>
  <!--[if (gte mso 9)|(IE)]>
  </td></tr></table>
  <![endif]-->

</td></tr>
</table>
</body>
</html>`)))

// Layout wraps an email body in the shared shell: the logo, a white card and
// the footer. body must already be HTML-safe. publicURL may be empty, in which
// case the logo is not a link.
func Layout(title string, body template.HTML, publicURL string) (string, error) {
	base := strings.TrimRight(publicURL, "/")
	var buf bytes.Buffer
	if err := layoutTmpl.Execute(&buf, struct {
		Title     string
		Body      template.HTML
		LogoSrc   string
		PublicURL string
	}{title, body, base + "/assets/email-logo.png", base}); err != nil {
		return "", fmt.Errorf("render email layout: %w", err)
	}
	return buf.String(), nil
}

// SplitTitle splits "TypeError: Cannot read…" into the exception type and the
// message, as the app does (web/src/utils/issueTitle.ts). Titles without a
// type prefix, or with a colon inside a sentence, stay whole.
func SplitTitle(title string) (typ, message string) {
	i := strings.IndexByte(title, ':')
	if i <= 0 || i > 80 || strings.ContainsAny(title[:i], " \t\n") {
		return "", title
	}
	return title[:i], strings.TrimSpace(title[i+1:])
}

// FormatDuration prints milliseconds like the app's formatDuration: "<1ms",
// "471ms", "1.99s", "127.4s".
func FormatDuration(ms float64) string {
	switch {
	case ms < 1:
		return "<1ms"
	case ms < 1000:
		return fmt.Sprintf("%dms", int64(math.Round(ms)))
	case ms < 10000:
		return fmt.Sprintf("%.2fs", ms/1000)
	default:
		return fmt.Sprintf("%.1fs", ms/1000)
	}
}

// FormatCount prints a count compactly like the dashboard: 950, 1.2k, 3.4M.
func FormatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// FormatSpan prints how long something has lasted like the app's formatSpan:
// "45s", "26m", "3h 2m", "4h", "2d".
func FormatSpan(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	h := m / 60
	if h < 24 {
		if m%60 > 0 {
			return fmt.Sprintf("%dh %dm", h, m%60)
		}
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", h/24)
}
