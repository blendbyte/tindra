package alerts

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/emailstyle"
	"github.com/blendbyte/tindra/internal/storage"
)

//go:embed assets/tindra-logo.png
var LogoPNG []byte

// renderBase wraps pre-rendered body HTML in the shared Tindra email layout.
// body must already be HTML-safe (use template.HTML to mark it as trusted).
func renderBase(title string, body template.HTML, publicURL string) (string, error) {
	return emailstyle.Layout(title, body, publicURL)
}

// button renders the primary call to action in the app's accent colour, with
// a VML fallback so Outlook draws it as a button too.
const buttonTmpl = `<table cellpadding="0" cellspacing="0" role="presentation" style="margin:0 0 28px;">
  <tr>
    <td style="border-radius:8px;background-color:__ACCENT__;mso-padding-alt:0;">
      <!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="{{.Href}}" style="height:42px;v-text-anchor:middle;width:210px;" arcsize="18%" stroke="f" fillcolor="__ACCENT__"><w:anchorlock/><center style="color:#ffffff;font-family:Arial,sans-serif;font-size:14px;font-weight:600;">{{.Label}} &rarr;</center></v:roundrect><![endif]-->
      <!--[if !mso]><!--><a href="{{.Href}}" style="display:inline-block;padding:13px 24px;font-size:14px;font-weight:600;color:#ffffff;text-decoration:none;line-height:1;__SANS__">{{.Label}} &rarr;</a><!--<![endif]-->
    </td>
  </tr>
</table>`

// linkBlock is the call to action for invite and reset emails: the button,
// the link to copy, and a note on when it expires.
const linkBlockTmpl = `<h1 style="margin:0 0 8px;font-size:22px;font-weight:600;color:__TEXT1__;letter-spacing:-0.2px;line-height:1.3;__SANS__">{{.Heading}}</h1>
<p style="margin:0 0 24px;font-size:15px;line-height:1.6;color:__TEXT2__;__SANS__">{{.Lead}}</p>
{{template "button" .}}
<p style="margin:0 0 4px;font-size:12px;color:__TEXT3__;line-height:1.5;__SANS__">Or copy this link into your browser:</p>
<p style="margin:0 0 24px;font-size:12px;line-height:1.5;word-break:break-all;__MONO__"><a href="{{.Href}}" style="color:__ACCENT__;text-decoration:none;">{{.Href}}</a></p>
<p style="margin:0;padding-top:16px;border-top:1px solid __HAIRLINE__;font-size:12px;color:__TEXT3__;line-height:1.6;__SANS__">{{.Note}}</p>`

var linkEmailTmpl = template.Must(template.Must(template.New("link").Parse(emailstyle.Expand(linkBlockTmpl))).New("button").Parse(emailstyle.Expand(buttonTmpl)))

type linkEmailData struct {
	Heading string
	Lead    template.HTML
	Href    string
	Label   string
	Note    template.HTML
}

func renderLinkBody(d linkEmailData) (template.HTML, error) {
	var buf bytes.Buffer
	if err := linkEmailTmpl.ExecuteTemplate(&buf, "link", d); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// RenderPasswordResetEmail returns (html, text) for a password reset email.
func RenderPasswordResetEmail(resetURL, publicURL string) (string, string, error) {
	body, err := renderLinkBody(linkEmailData{
		Heading: "Reset your password",
		Lead:    "An administrator requested a password reset for your account. Click the button below to set a new password.",
		Href:    resetURL,
		Label:   "Reset password",
		Note:    "This link expires in <strong style=\"color:" + emailstyle.Text2 + ";font-weight:600;\">24 hours</strong>. If you weren&#39;t expecting this, you can safely ignore this email.",
	})
	if err != nil {
		return "", "", fmt.Errorf("render password reset body: %w", err)
	}
	html, err := renderBase("Reset your Tindra password", body, publicURL)
	if err != nil {
		return "", "", err
	}
	text := fmt.Sprintf(
		"An administrator requested a password reset for your Tindra account.\n\n"+
			"Click the link below to set a new password:\n\n"+
			"%s\n\n"+
			"This link expires in 24 hours.\n"+
			"If you did not expect this, you can safely ignore this email.",
		resetURL,
	)
	return html, text, nil
}

// ---- Alert email ----

// An alert email reads like the app: it leads with what happened, coloured by
// what kind of news it is (red for something broken, amber for a regression,
// violet for something new, green for a recovery), then lists what fired as
// rows, then links to the page that shows it.

type alertIssueData struct {
	Title         string // the full title, for the plain-text part
	TitleType     string
	TitleText     string
	Level         string
	LevelColor    string
	Environment   string
	ProjectName   string
	OccurredAt    string
	RequestURL    string
	RequestMethod string
	Message       string
	URL           string
	StackFrames   []string
}

type alertMonitorData struct {
	Name       string
	Schedule   string
	State      string
	StateColor string
	NextAt     string
	LastErrAt  string
	LastOkAt   string
}

type alertUptimeMonitorData struct {
	Name       string
	URL        string
	MonitorURL string
	State      string
	StateColor string
	For        string // how long it has been down: "3h 2m"
	Expected   string
	Received   string
	ResponseMs string
	DownSince  string
	Downtime   string
	History    []string // "up" or "down", oldest first
}

type alertLogData struct {
	Level       string
	LevelColor  string
	Body        string
	Time        string
	Environment string
}

// headPart is one run of the headline; Color is empty for plain text.
type headPart struct {
	Text  string
	Color string
}

type alertEmailData struct {
	Head           []headPart
	RuleName       string
	FiredAt        string
	ViewURL        string
	ViewLabel      string
	Issues         []alertIssueData
	Monitors       []alertMonitorData
	UptimeMonitors []alertUptimeMonitorData
	Logs           []alertLogData
	MoreCount      int
	MoreLabel      string
}

// Row building blocks shared by every kind of alert row.
const (
	rowStyle  = `padding:14px 0;border-bottom:1px solid __HAIRLINE__;word-break:break-word;__SANS__`
	metaStyle = `margin:0 0 4px;font-size:12px;line-height:1.5;color:__TEXT3__;`
	dotStyle  = `display:inline-block;width:7px;height:7px;border-radius:50%;margin-right:6px;vertical-align:1px;`
	nameStyle = `margin:0;font-size:15px;font-weight:500;line-height:1.45;color:__TEXT1__;text-decoration:none;`
	subStyle  = `margin:4px 0 0;font-size:12px;line-height:1.5;color:__TEXT3__;`
)

var alertBodyTmpl = template.Must(template.Must(template.New("alert-body").Parse(emailstyle.Expand(strings.NewReplacer(
	"__ROW__", rowStyle, "__META__", metaStyle, "__DOT__", dotStyle, "__NAME__", nameStyle, "__SUB__", subStyle,
).Replace(`<h1 style="margin:0 0 6px;font-size:22px;font-weight:600;color:__TEXT1__;letter-spacing:-0.2px;line-height:1.3;__SANS__">{{range .Head}}{{if .Color}}<span style="color:{{.Color}};">{{.Text}}</span>{{else}}{{.Text}}{{end}}{{end}}</h1>
<p style="margin:0 0 20px;font-size:13px;line-height:1.5;color:__TEXT3__;__SANS__">Alert rule <strong style="color:__TEXT2__;font-weight:600;">{{.RuleName}}</strong> fired {{.FiredAt}}</p>

{{if or .Issues .Monitors .UptimeMonitors .Logs}}
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="border-top:1px solid __HAIRLINE__;margin:0 0 {{if .MoreCount}}12{{else}}24{{end}}px;">
{{range .Issues}}
  <tr><td style="__ROW__">
    <p style="__META__"><span style="__DOT__background:{{.LevelColor}};"></span><strong style="color:__TEXT2__;font-weight:600;">{{.Level}}</strong>{{if .Environment}} &middot; {{.Environment}}{{end}}{{if .ProjectName}} &middot; {{.ProjectName}}{{end}}{{if .OccurredAt}} &middot; {{.OccurredAt}}{{end}}</p>
    <a href="{{.URL}}" style="__NAME__">{{if .TitleType}}<span style="color:__ACCENT__;font-size:14px;__MONO__">{{.TitleType}}</span>: {{end}}{{.TitleText}}</a>
    {{if .RequestURL}}<p style="__SUB__color:__TEXT2__;word-break:break-all;__MONO__">{{if .RequestMethod}}<strong style="color:__TEXT1__;font-weight:600;">{{.RequestMethod}}</strong> {{end}}{{.RequestURL}}</p>{{end}}
    {{if .Message}}<p style="margin:6px 0 0;font-size:13px;line-height:1.5;color:__TEXT2__;">{{.Message}}</p>{{end}}
    {{if .StackFrames}}<div style="margin-top:8px;padding:8px 10px;background-color:__SURFACE__;border-radius:6px;font-size:12px;line-height:1.6;color:__TEXT2__;__MONO__">{{range .StackFrames}}<div style="word-break:break-all;">{{.}}</div>{{end}}</div>{{end}}
  </td></tr>
{{end}}
{{range .Monitors}}
  <tr><td style="__ROW__">
    <p style="__META__"><span style="__DOT__background:{{.StateColor}};"></span><strong style="color:{{.StateColor}};font-weight:600;">{{.State}}</strong> &middot; <span style="__MONO__">{{.Schedule}}</span></p>
    <p style="__NAME__">{{.Name}}</p>
    {{if or .NextAt .LastErrAt .LastOkAt}}<p style="__SUB__">{{if .LastErrAt}}Last error at {{.LastErrAt}}{{end}}{{if and .LastErrAt .LastOkAt}} &middot; {{end}}{{if .LastOkAt}}Last OK at {{.LastOkAt}}{{end}}{{if and .NextAt (or .LastErrAt .LastOkAt)}} &middot; {{end}}{{if .NextAt}}Expected at {{.NextAt}}{{end}}</p>{{end}}
  </td></tr>
{{end}}
{{range .UptimeMonitors}}
  <tr><td style="__ROW__">
    <p style="__META__"><span style="__DOT__background:{{.StateColor}};"></span><strong style="color:{{.StateColor}};font-weight:600;">{{.State}}{{if .For}} for {{.For}}{{end}}</strong>{{if .Received}} &middot; expected {{.Expected}}, got {{.Received}}{{end}}{{if .ResponseMs}} &middot; {{.ResponseMs}}{{end}}</p>
    {{if .MonitorURL}}<a href="{{.MonitorURL}}" style="__NAME__">{{.Name}}</a>{{else}}<p style="__NAME__">{{.Name}}</p>{{end}}
    <p style="__SUB__color:__TEXT2__;word-break:break-all;__MONO__">{{.URL}}</p>
    {{if .DownSince}}<p style="__SUB__">Down since {{.DownSince}}</p>{{end}}
    {{if .Downtime}}<p style="__SUB__">Was down for {{.Downtime}}</p>{{end}}
    {{if .History}}<p style="margin:8px 0 0;line-height:1;font-size:0;">{{range .History}}<span style="display:inline-block;width:8px;height:8px;border-radius:2px;margin:0 2px 0 0;background:{{if eq . "up"}}__PASSTICK__{{else}}__DANGER__{{end}};"></span>{{end}}</p>{{end}}
  </td></tr>
{{end}}
{{range .Logs}}
  <tr><td style="__ROW__">
    <p style="__META__"><span style="__DOT__background:{{.LevelColor}};"></span><strong style="color:__TEXT2__;font-weight:600;">{{.Level}}</strong>{{if .Environment}} &middot; {{.Environment}}{{end}}{{if .Time}} &middot; {{.Time}}{{end}}</p>
    <p style="margin:0;font-size:13px;line-height:1.5;color:__TEXT1__;__MONO__">{{.Body}}</p>
  </td></tr>
{{end}}
</table>
{{end}}
{{if .MoreCount}}<p style="margin:0 0 24px;font-size:13px;color:__TEXT3__;__SANS__">and {{.MoreCount}} more {{.MoreLabel}}</p>{{end}}

{{if .ViewURL}}{{template "button" .}}{{end}}

<p style="margin:0;padding-top:16px;border-top:1px solid __HAIRLINE__;font-size:12px;color:__TEXT3__;line-height:1.6;__SANS__">
  You&#39;re receiving this because an alert rule in your Tindra instance fired.
  Manage alert rules in <strong style="color:__TEXT2__;font-weight:600;">Settings &rsaquo; Alerts</strong>.
</p>`)))).New("button").Parse(emailstyle.Expand(strings.ReplaceAll(strings.ReplaceAll(buttonTmpl, "{{.Href}}", "{{.ViewURL}}"), "{{.Label}}", "{{.ViewLabel}}"))))

// alertLevelColor is the level dot's colour, as in the app's .leveldot.
func alertLevelColor(level string) string {
	switch level {
	case "fatal", "error":
		return emailstyle.Danger
	case "warning":
		return emailstyle.Warning
	case "info":
		return emailstyle.Info
	default:
		return emailstyle.Text3
	}
}

// alertTriggerLine returns a human-readable sentence describing why this alert fired.
func alertTriggerLine(p AlertPayload) string {
	var b strings.Builder
	for _, part := range alertHeadline(p) {
		b.WriteString(part.Text)
	}
	return b.String()
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// alertHeadline is the trigger sentence in parts, with the part that carries
// the news in its state colour.
func alertHeadline(p AlertPayload) []headPart {
	plain := func(s string) []headPart { return []headPart{{Text: s}} }
	lead := func(color, text, rest string) []headPart {
		parts := []headPart{{Text: text, Color: color}}
		if rest != "" {
			parts = append(parts, headPart{Text: rest})
		}
		return parts
	}
	newIssues := func(n int) []headPart {
		verb := " were created since the last alert."
		if n == 1 {
			verb = " was created since the last alert."
		}
		return lead(emailstyle.Accent, countOf(n, "new issue", "new issues"), verb)
	}
	regressed := func(n int) []headPart {
		return lead(emailstyle.Warning, countOf(n, "previously resolved issue", "previously resolved issues")+" regressed", " since the last alert.")
	}
	if _, isTest := p.Details["test"]; isTest {
		return plain("This is a test alert. No action required.")
	}
	switch p.Trigger {
	case "new_issue":
		n, _ := detailInt(p.Details, "new_issue_count")
		return newIssues(n)
	case "regressed":
		r, _ := detailInt(p.Details, "regressed_count")
		return regressed(r)
	case "new_or_regressed":
		n, _ := detailInt(p.Details, "new_issue_count")
		r, _ := detailInt(p.Details, "regressed_count")
		switch {
		case n > 0 && r > 0:
			return []headPart{
				{Text: countOf(n, "new issue", "new issues"), Color: emailstyle.Accent},
				{Text: " and "},
				{Text: countOf(r, "regression", "regressions"), Color: emailstyle.Warning},
				{Text: " since the last alert."},
			}
		case n > 0:
			return newIssues(n)
		default:
			return regressed(r)
		}
	case "event_count":
		count, _ := detailInt(p.Details, "event_count")
		threshold, _ := detailInt(p.Details, "threshold")
		windowMins, _ := detailInt(p.Details, "window_mins")
		return lead(emailstyle.Danger, fmt.Sprintf("%d events in the last %d minutes", count, windowMins), fmt.Sprintf(" (threshold: %d).", threshold))
	case "log_count":
		count, _ := detailInt(p.Details, "log_count")
		threshold, _ := detailInt(p.Details, "threshold")
		windowMins, _ := detailInt(p.Details, "window_mins")
		// The count leads in red; the filter follows in plain text so a long
		// query does not turn the whole headline red.
		rest := fmt.Sprintf(" (threshold: %d).", threshold)
		if query := logCountQueryLabel(p); query != "" {
			rest = " matched " + query + rest
		}
		return lead(emailstyle.Danger, fmt.Sprintf("%d logs in the last %d minutes", count, windowMins), rest)
	case "cron_missed":
		n, _ := detailInt(p.Details, "missed_count")
		if n == 1 {
			return lead(emailstyle.Danger, "1 cron monitor missed its expected check-in.", "")
		}
		return lead(emailstyle.Danger, fmt.Sprintf("%d cron monitors missed their expected check-in.", n), "")
	case "cron_error":
		n, _ := detailInt(p.Details, "error_count")
		if n == 1 {
			return lead(emailstyle.Danger, "1 cron monitor reported an error.", "")
		}
		return lead(emailstyle.Danger, fmt.Sprintf("%d cron monitors reported errors.", n), "")
	case "uptime_down":
		n, _ := detailInt(p.Details, "down_count")
		if n == 1 {
			return lead(emailstyle.Danger, "1 uptime monitor is down.", "")
		}
		return lead(emailstyle.Danger, fmt.Sprintf("%d uptime monitors are down.", n), "")
	case "uptime_recovered":
		n, _ := detailInt(p.Details, "recovered_count")
		if n == 1 {
			return lead(emailstyle.Success, "1 uptime monitor has recovered.", "")
		}
		return lead(emailstyle.Success, fmt.Sprintf("%d uptime monitors have recovered.", n), "")
	case "issue_auto_resolved":
		n, _ := detailInt(p.Details, "resolved_count")
		if n == 1 {
			return plain("1 issue was automatically resolved.")
		}
		return plain(fmt.Sprintf("%d issues were automatically resolved.", n))
	default:
		return plain(fmt.Sprintf("Alert rule %q fired.", p.RuleName))
	}
}

func buildIssueCards(issues []*storage.Issue, projectName string, projectNames map[string]string, base string) []alertIssueData {
	cards := make([]alertIssueData, 0, len(issues))
	for _, iss := range issues {
		env := ""
		if iss.Environment != nil {
			env = *iss.Environment
		}
		occurredAt := ""
		if iss.AlertOccurredAt != nil {
			occurredAt = iss.AlertOccurredAt.UTC().Format("2 Jan 2006, 15:04:05 UTC")
		}
		name := projectName
		if n := projectNames[iss.ProjectID]; n != "" {
			name = n
		}
		typ, text := emailstyle.SplitTitle(iss.Title)
		cards = append(cards, alertIssueData{
			Title:         iss.Title,
			TitleType:     typ,
			TitleText:     text,
			Level:         iss.Level,
			LevelColor:    alertLevelColor(iss.Level),
			Environment:   env,
			ProjectName:   name,
			OccurredAt:    occurredAt,
			RequestURL:    iss.AlertReqURL,
			RequestMethod: iss.AlertReqMethod,
			Message:       iss.AlertMessage,
			URL:           base + "/issues/" + iss.ID,
			StackFrames:   iss.TopFrames,
		})
	}
	return cards
}

func buildUptimeMonitorCards(monitors []*storage.UptimeMonitor, trigger, base string, firedAt time.Time) []alertUptimeMonitorData {
	cards := make([]alertUptimeMonitorData, 0, len(monitors))
	for _, m := range monitors {
		card := alertUptimeMonitorData{
			Name:     m.Name,
			URL:      m.URL,
			Expected: m.ExpectedCodes,
		}
		if base != "" {
			card.MonitorURL = monitorsViewURL(base, "uptime", []string{m.ProjectID})
		}
		if m.LastStatusCode != nil {
			card.Received = fmt.Sprintf("HTTP %d", *m.LastStatusCode)
		} else if m.LastError != nil && *m.LastError != "" {
			card.Received = *m.LastError
		}
		if m.LastResponseMs != nil {
			card.ResponseMs = fmt.Sprintf("%dms", *m.LastResponseMs)
		}
		for _, c := range m.RecentChecks {
			card.History = append(card.History, c.Status)
		}
		if trigger == "uptime_recovered" {
			card.State = "Recovered"
			card.StateColor = emailstyle.Success
			if m.WentDownAt != nil && m.LastOkAt != nil {
				card.Downtime = emailstyle.FormatSpan(m.LastOkAt.Sub(*m.WentDownAt))
			}
		} else {
			card.State = "Down"
			card.StateColor = emailstyle.Danger
			// Like the monitors page: the outage counts from when it went
			// down, or from the last good check when that is all we know.
			if m.WentDownAt != nil {
				card.DownSince = m.WentDownAt.UTC().Format("2 Jan 2006 15:04 UTC")
				card.For = emailstyle.FormatSpan(firedAt.Sub(*m.WentDownAt))
			} else if m.LastOkAt != nil {
				card.DownSince = m.LastOkAt.UTC().Format("2 Jan 2006 15:04 UTC") + " (approx)"
				card.For = emailstyle.FormatSpan(firedAt.Sub(*m.LastOkAt))
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// cronStateLabel and cronStateColor name a cron monitor's state the way the
// monitors page does: failures in red, a running job in plain text.
func cronStateLabel(state string) string {
	switch state {
	case "ok":
		return "OK"
	case "missed":
		return "Missed"
	case "error":
		return "Error"
	case "in_progress":
		return "Running"
	default:
		return "Unknown"
	}
}

func cronStateColor(state string) string {
	switch state {
	case "ok":
		return emailstyle.Success
	case "missed", "error":
		return emailstyle.Danger
	case "in_progress":
		return emailstyle.Text2
	default:
		return emailstyle.Text3
	}
}

func moreLabel(trigger string, count int) string {
	switch trigger {
	case "cron_missed", "cron_error", "uptime_down", "uptime_recovered":
		if count == 1 {
			return "monitor"
		}
		return "monitors"
	case "regressed":
		if count == 1 {
			return "regressed issue"
		}
		return "regressed issues"
	case "log_count":
		if count == 1 {
			return "log"
		}
		return "logs"
	case "new_or_regressed":
		// The rest can be new or regressed, so the label names neither.
		if count == 1 {
			return "issue"
		}
		return "issues"
	default:
		if count == 1 {
			return "new issue"
		}
		return "new issues"
	}
}

func logCountQueryLabel(p AlertPayload) string {
	var parts []string
	if v, ok := p.Details["filter_environment"].(string); ok && v != "" {
		parts = append(parts, v)
	}
	if v, ok := p.Details["filter_level"].(string); ok && v != "" {
		if v == "fatal" {
			parts = append(parts, "fatal")
		} else {
			parts = append(parts, v+"+")
		}
	}
	if v, ok := p.Details["filter_search"].(string); ok && v != "" {
		parts = append(parts, `"`+v+`"`)
	}
	return strings.Join(parts, " · ")
}

// monitorsViewURL links to the uptime or cron monitor page; /monitors alone
// opens the cron page. The monitors' projects travel with the link, so they
// are listed even when the reader has other projects selected in the app.
func monitorsViewURL(base, kind string, projectIDs []string) string {
	q := url.Values{}
	for _, id := range projectIDs {
		if id != "" && !slices.Contains(q["project_id"], id) {
			q.Add("project_id", id)
		}
	}
	if len(q) == 0 {
		return base + "/monitors/" + kind
	}
	return base + "/monitors/" + kind + "?" + q.Encode()
}

func uptimeProjectIDs(monitors []*storage.UptimeMonitor) []string {
	ids := make([]string, 0, len(monitors))
	for _, m := range monitors {
		ids = append(ids, m.ProjectID)
	}
	return ids
}

func cronProjectIDs(monitors []*storage.CronMonitor) []string {
	ids := make([]string, 0, len(monitors))
	for _, m := range monitors {
		ids = append(ids, m.ProjectID)
	}
	return ids
}

func logsViewURL(base string, p AlertPayload) string {
	q := url.Values{}
	if v, ok := p.Details["filter_level"].(string); ok && v != "" {
		q.Set("min_level", v)
	}
	if v, ok := p.Details["filter_environment"].(string); ok && v != "" {
		q.Set("environment", v)
	}
	if v, ok := p.Details["filter_search"].(string); ok && v != "" {
		q.Set("search", v)
	}
	for _, id := range detailStrings(p.Details, "project_ids") {
		if id != "" {
			q.Add("project_id", id)
		}
	}
	if p.ProjectID != "" && q.Get("project_id") == "" {
		q.Add("project_id", p.ProjectID)
	}
	encoded := q.Encode()
	if encoded == "" {
		return base + "/logs"
	}
	return base + "/logs?" + encoded
}

func buildLogCards(logs []*storage.Log) []alertLogData {
	cards := make([]alertLogData, 0, len(logs))
	for _, l := range logs {
		card := alertLogData{
			Level:      l.Level,
			LevelColor: alertLevelColor(l.Level),
			Body:       truncateLogBody(l.Body, 240),
			Time:       l.Timestamp.UTC().Format("15:04:05 UTC"),
		}
		if l.Environment != nil {
			card.Environment = *l.Environment
		}
		cards = append(cards, card)
	}
	return cards
}

// RenderAlertEmail returns (html, text) for an alert notification email.
func RenderAlertEmail(payload AlertPayload, publicURL string) (string, string, error) {
	base := strings.TrimRight(publicURL, "/")

	isCron := payload.Trigger == "cron_missed" || payload.Trigger == "cron_error"
	isUptime := payload.Trigger == "uptime_down" || payload.Trigger == "uptime_recovered"
	isLogs := payload.Trigger == "log_count"

	viewURL := ""
	viewLabel := "View issues"
	if base != "" {
		switch {
		case isUptime:
			viewURL = monitorsViewURL(base, "uptime", uptimeProjectIDs(payload.UptimeMonitors))
			viewLabel = "View uptime monitors"
		case isCron:
			viewURL = monitorsViewURL(base, "cron", cronProjectIDs(payload.Monitors))
			viewLabel = "View cron monitors"
		case isLogs:
			viewURL = logsViewURL(base, payload)
			viewLabel = "View logs"
		default:
			viewURL = base + "/issues"
		}
	}

	issueCards := buildIssueCards(payload.Issues, payload.ProjectName, payload.ProjectNames, base)
	uptimeCards := buildUptimeMonitorCards(payload.UptimeMonitors, payload.Trigger, base, payload.FiredAt)
	logCards := buildLogCards(payload.Logs)

	var monitorCards []alertMonitorData
	for _, m := range payload.Monitors {
		card := alertMonitorData{
			Name:       m.Name,
			Schedule:   m.Schedule,
			State:      cronStateLabel(m.State),
			StateColor: cronStateColor(m.State),
		}
		if m.NextExpectedAt != nil {
			card.NextAt = m.NextExpectedAt.UTC().Format("2 Jan 2006 15:04 UTC")
		}
		if m.LastCheckinAt != nil && m.LastCheckinStatus != nil && *m.LastCheckinStatus == "error" {
			card.LastErrAt = m.LastCheckinAt.UTC().Format("2 Jan 2006 15:04 UTC")
		}
		if m.LastOkAt != nil {
			card.LastOkAt = m.LastOkAt.UTC().Format("2 Jan 2006 15:04 UTC")
		}
		monitorCards = append(monitorCards, card)
	}

	moreCount := 0
	switch payload.Trigger {
	case "new_issue":
		if n, ok := detailInt(payload.Details, "new_issue_count"); ok {
			moreCount = n - len(issueCards)
		}
	case "regressed":
		if r, ok := detailInt(payload.Details, "regressed_count"); ok {
			moreCount = r - len(issueCards)
		}
	case "new_or_regressed":
		n, _ := detailInt(payload.Details, "new_issue_count")
		r, _ := detailInt(payload.Details, "regressed_count")
		moreCount = (n + r) - len(issueCards)
	case "cron_missed":
		if n, ok := detailInt(payload.Details, "missed_count"); ok {
			moreCount = n - len(monitorCards)
		}
	case "cron_error":
		if n, ok := detailInt(payload.Details, "error_count"); ok {
			moreCount = n - len(monitorCards)
		}
	case "uptime_down":
		if n, ok := detailInt(payload.Details, "down_count"); ok {
			moreCount = n - len(uptimeCards)
		}
	case "uptime_recovered":
		if n, ok := detailInt(payload.Details, "recovered_count"); ok {
			moreCount = n - len(uptimeCards)
		}
	case "log_count":
		if n, ok := detailInt(payload.Details, "log_count"); ok {
			moreCount = n - len(logCards)
		}
	}
	if moreCount < 0 {
		moreCount = 0
	}

	data := alertEmailData{
		Head:           alertHeadline(payload),
		RuleName:       payload.RuleName,
		FiredAt:        payload.FiredAt.UTC().Format("Mon, 2 Jan 2006 at 15:04 UTC"),
		ViewURL:        viewURL,
		ViewLabel:      viewLabel,
		Issues:         issueCards,
		Monitors:       monitorCards,
		UptimeMonitors: uptimeCards,
		Logs:           logCards,
		MoreCount:      moreCount,
		MoreLabel:      moreLabel(payload.Trigger, moreCount),
	}
	var bodyBuf bytes.Buffer
	if err := alertBodyTmpl.ExecuteTemplate(&bodyBuf, "alert-body", data); err != nil {
		return "", "", fmt.Errorf("render alert body: %w", err)
	}
	html, err := renderBase("[Tindra] "+payload.RuleName, template.HTML(bodyBuf.String()), base)
	if err != nil {
		return "", "", err
	}

	var tb strings.Builder
	fmt.Fprintf(&tb, "%s\n\nAlert rule: %s\n\n", alertTriggerLine(payload), payload.RuleName)

	for _, iss := range data.Issues {
		tb.WriteString("----------------------------------------\n")
		meta := iss.Level
		if iss.Environment != "" {
			meta += " · " + iss.Environment
		}
		if iss.ProjectName != "" {
			meta += " · " + iss.ProjectName
		}
		fmt.Fprintf(&tb, "%s\n%s\n", strings.ToUpper(meta), iss.Title)
		if iss.OccurredAt != "" {
			fmt.Fprintf(&tb, "%s\n", iss.OccurredAt)
		}
		if iss.RequestURL != "" {
			if iss.RequestMethod != "" {
				fmt.Fprintf(&tb, "%s %s\n", iss.RequestMethod, iss.RequestURL)
			} else {
				fmt.Fprintf(&tb, "%s\n", iss.RequestURL)
			}
		}
		if iss.Message != "" {
			fmt.Fprintf(&tb, "\n%s\n", iss.Message)
		}
		if len(iss.StackFrames) > 0 {
			tb.WriteString("\n")
			for _, f := range iss.StackFrames {
				fmt.Fprintf(&tb, "  %s\n", f)
			}
		}
		if iss.URL != "" {
			fmt.Fprintf(&tb, "\n%s\n", iss.URL)
		}
		tb.WriteString("\n")
	}

	for _, m := range data.Monitors {
		tb.WriteString("----------------------------------------\n")
		fmt.Fprintf(&tb, "%s  %s\n%s\n", strings.ToUpper(m.State), m.Schedule, m.Name)
		if m.NextAt != "" {
			fmt.Fprintf(&tb, "Expected at %s\n", m.NextAt)
		}
		if m.LastErrAt != "" {
			fmt.Fprintf(&tb, "Last error at %s\n", m.LastErrAt)
		}
		if m.LastOkAt != "" {
			fmt.Fprintf(&tb, "Last OK at %s\n", m.LastOkAt)
		}
		tb.WriteString("\n")
	}

	for _, um := range data.UptimeMonitors {
		tb.WriteString("----------------------------------------\n")
		fmt.Fprintf(&tb, "%s\n%s\n%s\n", strings.ToUpper(um.State), um.Name, um.URL)
		if um.Received != "" {
			fmt.Fprintf(&tb, "Expected: %s  Got: %s\n", um.Expected, um.Received)
		}
		if um.ResponseMs != "" {
			fmt.Fprintf(&tb, "Response: %s\n", um.ResponseMs)
		}
		if um.DownSince != "" {
			fmt.Fprintf(&tb, "Down since %s\n", um.DownSince)
		}
		if um.Downtime != "" {
			fmt.Fprintf(&tb, "Was down for %s\n", um.Downtime)
		}
		if len(um.History) > 0 {
			var dots strings.Builder
			for _, s := range um.History {
				if s == "up" {
					dots.WriteByte('+')
				} else {
					dots.WriteByte('-')
				}
			}
			fmt.Fprintf(&tb, "History (oldest→newest): %s\n", dots.String())
		}
		tb.WriteString("\n")
	}

	for _, l := range data.Logs {
		tb.WriteString("----------------------------------------\n")
		meta := strings.ToUpper(l.Level)
		if l.Environment != "" {
			meta += " · " + l.Environment
		}
		if l.Time != "" {
			meta += " · " + l.Time
		}
		fmt.Fprintf(&tb, "%s\n%s\n\n", meta, l.Body)
	}

	if data.MoreCount > 0 {
		fmt.Fprintf(&tb, "...and %d more %s\n\n", data.MoreCount, data.MoreLabel)
	}

	fmt.Fprintf(&tb, "Fired at: %s\n", data.FiredAt)
	if viewURL != "" {
		fmt.Fprintf(&tb, "%s: %s\n", viewLabel, viewURL)
	}
	tb.WriteString("\n---\nYou're receiving this because an alert rule in your Tindra instance fired.\n" +
		"Manage alert rules in Settings > Alerts.")
	text := tb.String()

	return html, text, nil
}

// RenderInviteEmail returns (html, text) for an invite email.
// publicURL is the base URL of the Tindra instance (e.g. "https://tindra.example.com").
func RenderInviteEmail(inviteURL, publicURL string) (string, string, error) {
	body, err := renderLinkBody(linkEmailData{
		Heading: "You\u2019ve been invited",
		Lead:    "You\u2019ve been invited to join <strong style=\"color:" + emailstyle.Text1 + ";font-weight:600;\">Tindra</strong>. Click the button below to set up your account.",
		Href:    inviteURL,
		Label:   "Accept invitation",
		Note:    "This invitation expires in <strong style=\"color:" + emailstyle.Text2 + ";font-weight:600;\">7 days</strong>. If you weren&#39;t expecting this, you can safely ignore this email.",
	})
	if err != nil {
		return "", "", fmt.Errorf("render invite body: %w", err)
	}
	html, err := renderBase("You've been invited to Tindra", body, publicURL)
	if err != nil {
		return "", "", err
	}
	text := fmt.Sprintf(
		"You've been invited to join Tindra.\n\n"+
			"Click the link below to accept your invitation and set your password:\n\n"+
			"%s\n\n"+
			"This invitation expires in 7 days.\n"+
			"If you did not expect this invitation, you can safely ignore this email.",
		inviteURL,
	)
	return html, text, nil
}
