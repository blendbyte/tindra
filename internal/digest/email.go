package digest

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// Report is the full data payload for a weekly digest email.
type Report struct {
	UserName    string
	From        time.Time
	To          time.Time
	TotalErrors int64
	TotalTx     int64
	DailyErrors []DayStat
	DailyTx     []DayStat
	Projects    []ProjectStat
	Issues      IssuesSummary
	TopIssues   []TopIssue
	TopTx       []TopTransaction
	PublicURL   string
}

func (r *Report) dateRange() string {
	return r.From.UTC().Format("Jan 2") + " – " + r.To.Add(-time.Second).UTC().Format("Jan 2, 2006")
}

func (r *Report) subject() string {
	name := r.UserName
	if name == "" {
		name = "your projects"
	}
	return fmt.Sprintf("Weekly update for %s - %s", name, r.dateRange())
}

// formatCount turns large numbers into compact form: 146000 → "146k"
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatMs(ms int64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2gs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

// barHeight returns a pixel height (max 40px) proportional to the value.
func barHeights(stats []DayStat, maxPx int) []int {
	if len(stats) == 0 {
		return nil
	}
	var max int64
	for _, s := range stats {
		if s.Count > max {
			max = s.Count
		}
	}
	heights := make([]int, len(stats))
	if max == 0 {
		return heights
	}
	for i, s := range stats {
		h := int(float64(s.Count) / float64(max) * float64(maxPx))
		if h < 2 && s.Count > 0 {
			h = 2
		}
		heights[i] = h
	}
	return heights
}

// sf is substituted into the template as literal text before template.Parse,
// so html/template's CSS sanitizer never sees it as a template action.
const sf = `font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;`

// txDelta returns a formatted delta label and CSS color for a p50 change.
// Returns ("", "") when there's no previous data or the change is < 5%.
func txDelta(cur, prev int64) (string, string) {
	if prev == 0 {
		return "", ""
	}
	pct := float64(cur-prev) / float64(prev) * 100
	if pct > -5 && pct < 5 {
		return "", ""
	}
	if pct > 0 {
		return fmt.Sprintf("↑%.0f%%", pct), "#ef4444"
	}
	return fmt.Sprintf("↓%.0f%%", -pct), "#22c55e"
}

var digestBodyTmpl = template.Must(template.New("digest-body").Funcs(template.FuncMap{
	"formatCount": formatCount,
	"formatMs":    formatMs,
	"txDeltaHTML": func(cur, prev int64) template.HTML {
		label, color := txDelta(cur, prev)
		if label == "" {
			return ""
		}
		return template.HTML(fmt.Sprintf(` <span style="color:%s;font-weight:600;%s">%s</span>`, color, sf, label))
	},
}).Parse(strings.ReplaceAll(`
<!-- Header -->
<p style="margin:0 0 4px;font-size:13px;color:#6b7280;__FF__">{{.DateRange}}</p>
<h1 style="margin:0 0 24px;font-size:22px;font-weight:700;color:#111827;letter-spacing:-0.3px;line-height:1.3;__FF__">Weekly update{{if .UserName}} for {{.UserName}}{{end}}</h1>

<!-- Stat cards: errors + transactions -->
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:24px;">
  <tr>
    <!-- Errors card -->
    <td width="48%" style="background:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:16px 18px;vertical-align:top;">
      <p style="margin:0 0 2px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">Total errors</p>
      <p style="margin:0 0 12px;font-size:28px;font-weight:700;color:#111827;line-height:1.1;__FF__">{{formatCount .TotalErrors}}</p>
      <!-- Bar chart -->
      <table cellpadding="0" cellspacing="0" role="presentation" style="width:100%;">
        <tr style="vertical-align:bottom;">
          {{range $i, $bar := .ErrorBars}}
          <td style="width:12%;text-align:center;vertical-align:bottom;padding:0 1px;">
            <div style="width:100%;height:{{$bar}}px;background-color:#6366f1;border-radius:2px 2px 0 0;"></div>
          </td>
          {{end}}
        </tr>
        <tr>
          {{range .DayLabels}}
          <td style="width:12%;text-align:center;padding-top:4px;font-size:9px;color:#9ca3af;__FF__">{{.}}</td>
          {{end}}
        </tr>
      </table>
    </td>
    <td width="4%">&nbsp;</td>
    <!-- Transactions card -->
    <td width="48%" style="background:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:16px 18px;vertical-align:top;">
      <p style="margin:0 0 2px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">Total transactions</p>
      <p style="margin:0 0 12px;font-size:28px;font-weight:700;color:#111827;line-height:1.1;__FF__">{{formatCount .TotalTx}}</p>
      <table cellpadding="0" cellspacing="0" role="presentation" style="width:100%;">
        <tr style="vertical-align:bottom;">
          {{range $i, $bar := .TxBars}}
          <td style="width:12%;text-align:center;vertical-align:bottom;padding:0 1px;">
            <div style="width:100%;height:{{$bar}}px;background-color:#8b5cf6;border-radius:2px 2px 0 0;"></div>
          </td>
          {{end}}
        </tr>
        <tr>
          {{range .DayLabels}}
          <td style="width:12%;text-align:center;padding-top:4px;font-size:9px;color:#9ca3af;__FF__">{{.}}</td>
          {{end}}
        </tr>
      </table>
    </td>
  </tr>
</table>

<!-- Project breakdown table -->
{{if .Projects}}
<p style="margin:0 0 8px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">By project</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:24px;border:1px solid #e5e7eb;border-radius:8px;border-collapse:separate;border-spacing:0;overflow:hidden;">
  <thead>
    <tr style="background:#f3f4f6;">
      <th style="padding:8px 14px;text-align:left;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Project</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Errors</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Transactions</th>
    </tr>
  </thead>
  <tbody>
    {{range .Projects}}
    <tr style="border-top:1px solid #f3f4f6;">
      <td style="padding:10px 14px;font-size:13px;color:#111827;font-weight:500;__FF__">{{.ProjectName}}</td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;font-family:'Courier New',Courier,monospace;">{{formatCount .Errors}}</td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;font-family:'Courier New',Courier,monospace;">{{formatCount .Transactions}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
{{end}}

<!-- Issues breakdown -->
<p style="margin:0 0 8px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">Issues breakdown</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:8px;">
  <tr>
    {{if gt .Issues.New 0}}<td style="padding:0 8px 0 0;">
      <div style="background:#f59e0b;border-radius:4px;padding:8px 12px;text-align:center;">
        <p style="margin:0;font-size:18px;font-weight:700;color:#ffffff;line-height:1;__FF__">{{.Issues.New}}</p>
        <p style="margin:4px 0 0;font-size:10px;color:rgba(255,255,255,0.85);__FF__">New</p>
      </div>
    </td>{{end}}
    {{if gt .Issues.Regressed 0}}<td style="padding:0 8px 0 0;">
      <div style="background:#ef4444;border-radius:4px;padding:8px 12px;text-align:center;">
        <p style="margin:0;font-size:18px;font-weight:700;color:#ffffff;line-height:1;__FF__">{{.Issues.Regressed}}</p>
        <p style="margin:4px 0 0;font-size:10px;color:rgba(255,255,255,0.85);__FF__">Regressed</p>
      </div>
    </td>{{end}}
    {{if gt .Issues.Ongoing 0}}<td style="padding:0;">
      <div style="background:#e5e7eb;border-radius:4px;padding:8px 12px;text-align:center;">
        <p style="margin:0;font-size:18px;font-weight:700;color:#6b7280;line-height:1;__FF__">{{.Issues.Ongoing}}</p>
        <p style="margin:4px 0 0;font-size:10px;color:#9ca3af;__FF__">Ongoing</p>
      </div>
    </td>{{end}}
  </tr>
</table>

<!-- Top issues -->
{{if .TopIssues}}
<p style="margin:20px 0 8px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">Issues with the most errors</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:24px;border:1px solid #e5e7eb;border-radius:8px;border-collapse:separate;border-spacing:0;overflow:hidden;">
  <thead>
    <tr style="background:#f3f4f6;">
      <th style="padding:8px 14px;text-align:left;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Issue</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Errors</th>
    </tr>
  </thead>
  <tbody>
    {{range .TopIssues}}
    <tr style="border-top:1px solid #f3f4f6;">
      <td style="padding:10px 14px;">
        <a href="{{$.IssueURL .IssueID}}" style="font-size:13px;font-weight:500;color:#6366f1;text-decoration:none;display:block;__FF__">{{.Title}}</a>
        <p style="margin:3px 0 0;font-size:11px;color:#9ca3af;__FF__">{{.ProjectName}}</p>
      </td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;vertical-align:top;font-family:'Courier New',Courier,monospace;">{{formatCount .Count}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
{{end}}

<!-- Top transactions -->
{{if .TopTx}}
<p style="margin:0 0 8px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.6px;color:#9ca3af;__FF__">Most frequent transactions</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:24px;border:1px solid #e5e7eb;border-radius:8px;border-collapse:separate;border-spacing:0;overflow:hidden;">
  <thead>
    <tr style="background:#f3f4f6;">
      <th style="padding:8px 14px;text-align:left;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Transaction</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">Count</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">p50</th>
      <th style="padding:8px 14px;text-align:right;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:#9ca3af;__FF__">p95</th>
    </tr>
  </thead>
  <tbody>
    {{range .TopTx}}
    <tr style="border-top:1px solid #f3f4f6;">
      <td style="padding:10px 14px;">
        <span style="font-size:13px;font-weight:500;color:#111827;font-family:'Courier New',Courier,monospace;">{{.Transaction}}</span>
        <p style="margin:3px 0 0;font-size:11px;color:#9ca3af;__FF__">{{.ProjectName}}</p>
      </td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;vertical-align:top;font-family:'Courier New',Courier,monospace;">{{formatCount .Count}}</td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;vertical-align:top;white-space:nowrap;__FF__">{{formatMs .P50Ms}}{{txDeltaHTML .P50Ms .P50PrevMs}}</td>
      <td style="padding:10px 14px;text-align:right;font-size:13px;color:#6b7280;vertical-align:top;white-space:nowrap;__FF__">{{formatMs .P95Ms}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
{{end}}

<!-- Divider + footer -->
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin:4px 0 20px;">
  <tr><td style="border-top:1px solid #f3f4f6;font-size:0;">&nbsp;</td></tr>
</table>
<p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;__FF__">
  You&#39;re receiving this because weekly digests are enabled for your account.
  <a href="{{.SettingsURL}}" style="color:#9ca3af;__FF__">Manage notification settings</a>.
</p>
`, "__FF__", sf)))

type digestTemplateData struct {
	UserName    string
	DateRange   string
	TotalErrors int64
	TotalTx     int64
	ErrorBars   []int
	TxBars      []int
	DayLabels   []string
	Projects    []ProjectStat
	Issues      IssuesSummary
	TopIssues   []TopIssue
	TopTx       []TopTransaction
	SettingsURL string
	baseURL     string
}

func (d *digestTemplateData) IssueURL(issueID string) string {
	return strings.TrimRight(d.baseURL, "/") + "/issues/" + issueID
}

// buildDayLabels returns Mon/Tue/... labels for a 7-day window ending the day before `to`.
func buildDayLabels(from time.Time) []string {
	labels := make([]string, 7)
	for i := range labels {
		labels[i] = from.AddDate(0, 0, i).UTC().Format("Mon")[0:1]
	}
	return labels
}

// fillDailyStats pads a sparse DayStat slice to exactly 7 entries covering from→to,
// inserting zeros for any missing days.
func fillDailyStats(stats []DayStat, from, to time.Time) []DayStat {
	filled := make([]DayStat, 7)
	idx := make(map[string]int64, len(stats))
	for _, s := range stats {
		idx[s.Date.UTC().Format("2006-01-02")] = s.Count
	}
	for i := range filled {
		day := from.AddDate(0, 0, i)
		filled[i] = DayStat{Date: day, Count: idx[day.UTC().Format("2006-01-02")]}
	}
	return filled
}

// RenderDigestEmail returns (subject, html, plaintext) for the weekly digest.
func RenderDigestEmail(r *Report) (subject, html, text string, err error) {
	base := strings.TrimRight(r.PublicURL, "/")
	logoSrc := base + "/assets/email-logo.png"

	dailyErrors := fillDailyStats(r.DailyErrors, r.From, r.To)
	dailyTx := fillDailyStats(r.DailyTx, r.From, r.To)

	data := &digestTemplateData{
		UserName:    r.UserName,
		DateRange:   r.dateRange(),
		TotalErrors: r.TotalErrors,
		TotalTx:     r.TotalTx,
		ErrorBars:   barHeights(dailyErrors, 40),
		TxBars:      barHeights(dailyTx, 40),
		DayLabels:   buildDayLabels(r.From),
		Projects:    r.Projects,
		Issues:      r.Issues,
		TopIssues:   r.TopIssues,
		TopTx:       r.TopTx,
		SettingsURL: base + "/settings/profile",
		baseURL:     base,
	}

	var bodyBuf bytes.Buffer
	if err = digestBodyTmpl.Execute(&bodyBuf, data); err != nil {
		err = fmt.Errorf("render digest body: %w", err)
		return
	}

	html, err = renderBaseLayout(r.subject(), template.HTML(bodyBuf.String()), logoSrc)
	if err != nil {
		return
	}

	subject = r.subject()
	text = buildDigestPlaintext(r, data.DateRange)
	return
}

func buildDigestPlaintext(r *Report, dateRange string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Weekly update - %s\n\n", dateRange)
	fmt.Fprintf(&b, "Errors: %s   Transactions: %s\n\n", formatCount(r.TotalErrors), formatCount(r.TotalTx))

	if len(r.Projects) > 0 {
		b.WriteString("By project:\n")
		for _, p := range r.Projects {
			fmt.Fprintf(&b, "  %-24s %6s errors  %8s txns\n",
				p.ProjectName, formatCount(p.Errors), formatCount(p.Transactions))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Issues: %d new, %d regressed, %d ongoing\n\n",
		r.Issues.New, r.Issues.Regressed, r.Issues.Ongoing)

	if len(r.TopIssues) > 0 {
		b.WriteString("Issues with the most errors:\n")
		for _, iss := range r.TopIssues {
			title := iss.Title
			if r := []rune(title); len(r) > 60 {
				title = string(r[:57]) + "..."
			}
			fmt.Fprintf(&b, "  %6s  %s (%s)\n", formatCount(iss.Count), title, iss.ProjectName)
		}
		b.WriteString("\n")
	}

	if len(r.TopTx) > 0 {
		b.WriteString("Most frequent transactions:\n")
		for _, tx := range r.TopTx {
			fmt.Fprintf(&b, "  %6s  %-40s p50 %s  p95 %s  (%s)\n",
				formatCount(tx.Count), tx.Transaction,
				formatMs(tx.P50Ms), formatMs(tx.P95Ms), tx.ProjectName)
		}
		b.WriteString("\n")
	}

	base := strings.TrimRight(r.PublicURL, "/")
	fmt.Fprintf(&b, "---\nManage notification settings: %s/settings/profile\n", base)
	return b.String()
}

// renderBaseLayout is the shared email shell - duplicated here to avoid a
// circular import with the alerts package.
func renderBaseLayout(title string, body template.HTML, logoSrc string) (string, error) {
	tmpl := template.Must(template.New("base").Parse(
		`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
  body,table,td,a{-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%}
  table,td{mso-table-lspace:0pt;mso-table-rspace:0pt;border-collapse:collapse}
  img{border:0;height:auto;line-height:100%;outline:none;text-decoration:none}
</style>
</head>
<body style="margin:0;padding:0;background-color:#f5f6f8;">
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="background-color:#f5f6f8;">
<tr><td align="center" style="padding:32px 16px;">
  <table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="max-width:600px;margin:0 auto;">
    <tr><td style="background-color:#ffffff;border-radius:12px;border:1px solid #e5e7eb;padding:32px 28px;">
      <div style="text-align:center;margin-bottom:28px;">
        <img src="{{.LogoSrc}}" width="160" alt="Tindra" style="border:0;display:inline-block;width:160px;height:auto;max-width:100%;">
      </div>
      {{.Body}}
    </td></tr>
    <tr><td style="padding-top:24px;text-align:center;font-size:12px;color:#9ca3af;line-height:1.6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
      <a href="https://tindra.sh" style="color:#9ca3af;text-decoration:none;">tindra.sh</a>
    </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct {
		Title   string
		Body    template.HTML
		LogoSrc string
	}{title, body, logoSrc}); err != nil {
		return "", fmt.Errorf("render base layout: %w", err)
	}
	return buf.String(), nil
}
