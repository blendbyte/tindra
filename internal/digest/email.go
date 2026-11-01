package digest

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/emailstyle"
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

// formatCount and formatMs print counts and durations like the app does.
func formatCount(n int64) string { return emailstyle.FormatCount(n) }

func formatMs(ms int64) string { return emailstyle.FormatDuration(float64(ms)) }

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

// minDeltaSamples is how many requests both weeks need before a p50 change is
// shown, the same evidence the performance page asks for, so a "↑157%" from a
// handful of requests never appears.
const minDeltaSamples = 20

// txDelta returns a formatted delta label and colour for a p50 change, in the
// app's terms: slower is amber, faster is quiet. It returns ("", "") without
// enough requests in either week or for a change under 5%.
func txDelta(cur, prev, curCount, prevCount int64) (string, string) {
	if prev == 0 || curCount < minDeltaSamples || prevCount < minDeltaSamples {
		return "", ""
	}
	pct := float64(cur-prev) / float64(prev) * 100
	if pct > -5 && pct < 5 {
		return "", ""
	}
	if pct > 0 {
		return fmt.Sprintf("↑%.0f%%", pct), emailstyle.Warning
	}
	return fmt.Sprintf("↓%.0f%%", -pct), emailstyle.Text3
}

// issuesVerdict is the sentence under the heading: what is new, what came
// back, and how much is still open, with new in violet and regressions in
// amber like the dashboard.
func issuesVerdict(s IssuesSummary) template.HTML {
	esc := template.HTMLEscapeString
	colored := func(color, text string) string {
		return `<span style="color:` + color + `;font-weight:600;">` + esc(text) + `</span>`
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return "1\u00a0" + one
		}
		return fmt.Sprintf("%d\u00a0%s", n, many)
	}
	var lead string
	switch {
	case s.New > 0 && s.Regressed > 0:
		lead = colored(emailstyle.Accent, plural(s.New, "new issue", "new issues")) + " and " + colored(emailstyle.Warning, plural(s.Regressed, "regression", "regressions")) + " this week."
	case s.New > 0:
		lead = colored(emailstyle.Accent, plural(s.New, "new issue", "new issues")) + " this week."
	case s.Regressed > 0:
		lead = colored(emailstyle.Warning, plural(s.Regressed, "regression", "regressions")) + " this week."
	default:
		lead = "No new or regressed issues this week."
	}
	// "21 more are ongoing" after news, "21 are ongoing" without any.
	more := ""
	if s.New > 0 || s.Regressed > 0 {
		more = "more "
	}
	switch {
	case s.Ongoing == 1:
		lead += " 1 " + more + "is ongoing."
	case s.Ongoing > 1:
		lead += fmt.Sprintf(" %d %sare ongoing.", s.Ongoing, more)
	}
	return template.HTML(lead)
}

const (
	sectionTitle = `margin:0 0 8px;font-size:14px;font-weight:600;color:__TEXT1__;__SANS__`
	thStyle      = `padding:0 0 8px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:__TEXT3__;border-bottom:1px solid __HAIRLINE__;__SANS__`
	tdStyle      = `padding:10px 0;border-bottom:1px solid __HAIRLINE__;vertical-align:top;`
	numStyle     = `padding:10px 0 10px 16px;border-bottom:1px solid __HAIRLINE__;vertical-align:top;text-align:right;white-space:nowrap;font-size:13px;color:__TEXT2__;__MONO__`
	subStyle     = `margin:3px 0 0;font-size:12px;color:__TEXT3__;__SANS__`
)

var digestBodyTmpl = template.Must(template.New("digest-body").Funcs(template.FuncMap{
	"formatCount": formatCount,
	"formatMs":    formatMs,
	"splitTitle": func(title string) []string {
		typ, msg := emailstyle.SplitTitle(title)
		return []string{typ, msg}
	},
	"txDeltaHTML": func(tx TopTransaction) template.HTML {
		label, color := txDelta(tx.P50Ms, tx.P50PrevMs, tx.Count, tx.P50PrevCount)
		if label == "" {
			return ""
		}
		return template.HTML(fmt.Sprintf(` <span style="color:%s;font-size:11px;">%s</span>`, color, label))
	},
}).Parse(emailstyle.Expand(strings.NewReplacer(
	"__SECTION__", sectionTitle, "__TH__", thStyle, "__TD__", tdStyle, "__NUM__", numStyle, "__SUBLINE__", subStyle,
).Replace(`
<h1 style="margin:0 0 4px;font-size:22px;font-weight:600;color:__TEXT1__;letter-spacing:-0.2px;line-height:1.3;__SANS__">Weekly update{{if .UserName}} for {{.UserName}}{{end}}</h1>
<p style="margin:0 0 16px;font-size:13px;color:__TEXT3__;__SANS__">{{.DateRange}}</p>
<p style="margin:0 0 24px;font-size:16px;line-height:1.5;color:__TEXT1__;__SANS__">{{.Verdict}}</p>

<!-- Errors and transactions, each with a bar per day -->
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;border-top:1px solid __HAIRLINE__;">
  <tr>
    {{range $i, $c := .Charts}}
    {{if $i}}<td width="8%">&nbsp;</td>{{end}}
    <td width="46%" style="padding-top:16px;vertical-align:top;">
      <p style="margin:0 0 2px;font-size:12px;color:__TEXT3__;__SANS__">{{$c.Label}}</p>
      <p style="margin:0 0 12px;font-size:26px;font-weight:600;color:__TEXT1__;line-height:1.1;__SANS__">{{formatCount $c.Total}}</p>
      <table cellpadding="0" cellspacing="0" role="presentation" style="width:100%;">
        <tr style="vertical-align:bottom;">
          {{range $c.Bars}}
          <td style="width:14%;vertical-align:bottom;padding:0 1px;">
            <div style="width:100%;height:{{.}}px;background-color:__ACCENT__;border-radius:2px 2px 0 0;"></div>
          </td>
          {{end}}
        </tr>
        <tr>
          {{range $.DayLabels}}
          <td style="width:14%;text-align:center;padding-top:4px;font-size:10px;color:__TEXT3__;__SANS__">{{.}}</td>
          {{end}}
        </tr>
      </table>
    </td>
    {{end}}
  </tr>
</table>

{{if .Projects}}
<p style="__SECTION__">By project</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <th align="left" style="__TH__">Project</th>
    <th align="right" style="__TH__">Errors</th>
    <th align="right" style="__TH__">Transactions</th>
  </tr>
  {{range .Projects}}
  <tr>
    <td style="__TD__font-size:13px;color:__TEXT1__;__SANS__">{{.ProjectName}}</td>
    <td style="__NUM__">{{formatCount .Errors}}</td>
    <td style="__NUM__">{{formatCount .Transactions}}</td>
  </tr>
  {{end}}
</table>
{{end}}

{{if .TopIssues}}
<p style="__SECTION__">Issues with the most errors</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <th align="left" style="__TH__">Issue</th>
    <th align="right" style="__TH__">Errors</th>
  </tr>
  {{range .TopIssues}}{{$t := splitTitle .Title}}
  <tr>
    <td style="__TD__">
      <a href="{{$.IssueURL .IssueID}}" style="display:block;font-size:13px;line-height:1.45;color:__TEXT1__;text-decoration:none;word-break:break-word;__SANS__">{{with index $t 0}}<span style="color:__ACCENT__;__MONO__">{{.}}</span>: {{end}}{{index $t 1}}</a>
      <p style="__SUBLINE__">{{.ProjectName}}</p>
    </td>
    <td style="__NUM__">{{formatCount .Count}}</td>
  </tr>
  {{end}}
</table>
{{end}}

{{if .TopTx}}
<p style="__SECTION__">Most frequent transactions</p>
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <th align="left" style="__TH__">Transaction</th>
    <th align="right" style="__TH__">Count</th>
    <th align="right" style="__TH__">P50</th>
    <th align="right" style="__TH__">P95</th>
  </tr>
  {{range .TopTx}}
  <tr>
    <td style="__TD__">
      <span style="font-size:13px;color:__TEXT1__;word-break:break-all;__MONO__">{{.Transaction}}</span>
      <p style="__SUBLINE__">{{.ProjectName}}</p>
    </td>
    <td style="__NUM__">{{formatCount .Count}}</td>
    <td style="__NUM__">{{formatMs .P50Ms}}{{txDeltaHTML .}}</td>
    <td style="__NUM__">{{formatMs .P95Ms}}</td>
  </tr>
  {{end}}
</table>
{{end}}

<p style="margin:0;padding-top:16px;border-top:1px solid __HAIRLINE__;font-size:12px;color:__TEXT3__;line-height:1.6;__SANS__">
  You&#39;re receiving this because weekly digests are enabled for your account.
  <a href="{{.SettingsURL}}" style="color:__TEXT3__;">Manage notification settings</a>.
</p>
`))))

type digestChart struct {
	Label string
	Total int64
	Bars  []int
}

type digestTemplateData struct {
	UserName    string
	DateRange   string
	Verdict     template.HTML
	Charts      []digestChart
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

	dailyErrors := fillDailyStats(r.DailyErrors, r.From, r.To)
	dailyTx := fillDailyStats(r.DailyTx, r.From, r.To)

	data := &digestTemplateData{
		UserName:  r.UserName,
		DateRange: r.dateRange(),
		Verdict:   issuesVerdict(r.Issues),
		Charts: []digestChart{
			{Label: "Errors", Total: r.TotalErrors, Bars: barHeights(dailyErrors, 40)},
			{Label: "Transactions", Total: r.TotalTx, Bars: barHeights(dailyTx, 40)},
		},
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

	html, err = emailstyle.Layout(r.subject(), template.HTML(bodyBuf.String()), base)
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
