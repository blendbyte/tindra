package alerts

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

//go:embed assets/tindra-logo.png
var LogoPNG []byte

// baseLayoutTmpl is the shared wrapper used by all transactional emails.
// It accepts a Title and pre-rendered Body (template.HTML - caller is responsible for escaping).
var baseLayoutTmpl = template.Must(template.New("base").Parse(
	`<!DOCTYPE html>
<html lang="en" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
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
<body style="margin:0;padding:0;background-color:#f5f6f8;word-spacing:normal;">
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="background-color:#f5f6f8;">
<tr><td align="center" style="padding:20px 10px;">

  <!--[if (gte mso 9)|(IE)]>
  <table width="680" align="center" cellpadding="0" cellspacing="0" role="presentation"><tr><td>
  <![endif]-->
  <table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="max-width:680px;margin:0 auto;">

    <!-- Card (logo + body on white) -->
    <tr><td style="background-color:#ffffff;border-radius:12px;padding:24px 20px;">
      <div style="text-align:center;margin-bottom:28px;">
        <a href="{{.PublicURL}}" style="border:0;text-decoration:none;"><img src="{{.LogoSrc}}" width="160" height="45" alt="Tindra" style="border:0;display:inline-block;width:160px;height:auto;max-width:100%;"></a>
      </div>
      {{.Body}}
    </td></tr>

    <!-- Footer -->
    <tr><td style="padding-top:24px;text-align:center;font-size:12px;color:#9ca3af;line-height:1.6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
      <a href="https://tindra.sh" style="color:#9ca3af;text-decoration:none;">tindra.sh</a>
      &nbsp;&middot;&nbsp;
      <a href="https://github.com/blendbyte/tindra" style="color:#9ca3af;text-decoration:none;">GitHub</a>
    </td></tr>

  </table>
  <!--[if (gte mso 9)|(IE)]>
  </td></tr></table>
  <![endif]-->

</td></tr>
</table>
</body>
</html>`))

type baseEmailData struct {
	Title     string
	Body      template.HTML
	LogoSrc   string
	PublicURL string
}

// renderBase wraps pre-rendered body HTML in the shared Tindra email layout.
// body must already be HTML-safe (use template.HTML to mark it as trusted).
func renderBase(title string, body template.HTML, logoSrc, publicURL string) (string, error) {
	var buf bytes.Buffer
	if err := baseLayoutTmpl.Execute(&buf, baseEmailData{
		Title:     title,
		Body:      body,
		LogoSrc:   logoSrc,
		PublicURL: publicURL,
	}); err != nil {
		return "", fmt.Errorf("render base layout: %w", err)
	}
	return buf.String(), nil
}

// ---- Invite email ----

var inviteBodyTmpl = template.Must(template.New("invite-body").Parse(
	`<h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#111827;letter-spacing:-0.3px;line-height:1.3;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">You&#39;ve been invited</h1>
<p style="margin:0 0 28px;font-size:15px;line-height:1.65;color:#6b7280;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  You&#39;ve been invited to join <strong style="color:#111827;font-weight:600;">Tindra</strong>.
  Click the button below to set up your account.
</p>

<table cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <td style="border-radius:8px;background-color:#6366f1;mso-padding-alt:0;">
      <!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="{{.InviteURL}}" style="height:44px;v-text-anchor:middle;width:200px;" arcsize="18%" stroke="f" fillcolor="#6366f1"><w:anchorlock/><center style="color:#ffffff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:14px;font-weight:600;">Accept invitation &rarr;</center></v:roundrect><![endif]-->
      <!--[if !mso]><!--><a href="{{.InviteURL}}" style="display:inline-block;padding:13px 28px;font-size:14px;font-weight:600;color:#ffffff;text-decoration:none;letter-spacing:-0.1px;line-height:1;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Accept invitation &rarr;</a><!--<![endif]-->
    </td>
  </tr>
</table>

<p style="margin:0 0 4px;font-size:12px;color:#9ca3af;line-height:1.5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Or copy this link into your browser:</p>
<p style="margin:0;font-size:12px;line-height:1.5;word-break:break-all;font-family:'Courier New',Courier,monospace;">
  <a href="{{.InviteURL}}" style="color:#6366f1;text-decoration:none;">{{.InviteURL}}</a>
</p>

<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin:28px 0;">
  <tr><td style="border-top:1px solid #f3f4f6;font-size:0;">&nbsp;</td></tr>
</table>

<p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  This invitation expires in <strong style="color:#9ca3af;">7 days</strong>.
  If you weren&#39;t expecting this, you can safely ignore this email.
</p>`))

// ---- Password reset email ----

var passwordResetBodyTmpl = template.Must(template.New("password-reset-body").Parse(
	`<h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#111827;letter-spacing:-0.3px;line-height:1.3;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Reset your password</h1>
<p style="margin:0 0 28px;font-size:15px;line-height:1.65;color:#6b7280;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  An administrator requested a password reset for your account.
  Click the button below to set a new password.
</p>

<table cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <td style="border-radius:8px;background-color:#6366f1;mso-padding-alt:0;">
      <!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="{{.ResetURL}}" style="height:44px;v-text-anchor:middle;width:200px;" arcsize="18%" stroke="f" fillcolor="#6366f1"><w:anchorlock/><center style="color:#ffffff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:14px;font-weight:600;">Reset password &rarr;</center></v:roundrect><![endif]-->
      <!--[if !mso]><!--><a href="{{.ResetURL}}" style="display:inline-block;padding:13px 28px;font-size:14px;font-weight:600;color:#ffffff;text-decoration:none;letter-spacing:-0.1px;line-height:1;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Reset password &rarr;</a><!--<![endif]-->
    </td>
  </tr>
</table>

<p style="margin:0 0 4px;font-size:12px;color:#9ca3af;line-height:1.5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Or copy this link into your browser:</p>
<p style="margin:0;font-size:12px;line-height:1.5;word-break:break-all;font-family:'Courier New',Courier,monospace;">
  <a href="{{.ResetURL}}" style="color:#6366f1;text-decoration:none;">{{.ResetURL}}</a>
</p>

<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin:28px 0;">
  <tr><td style="border-top:1px solid #f3f4f6;font-size:0;">&nbsp;</td></tr>
</table>

<p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  This link expires in <strong style="color:#9ca3af;">24 hours</strong>.
  If you weren&#39;t expecting this, you can safely ignore this email.
</p>`))

// RenderPasswordResetEmail returns (html, text) for a password reset email.
func RenderPasswordResetEmail(resetURL, publicURL string) (string, string, error) {
	logoSrc := strings.TrimRight(publicURL, "/") + "/assets/email-logo.png"
	var bodyBuf bytes.Buffer
	if err := passwordResetBodyTmpl.Execute(&bodyBuf, struct{ ResetURL string }{resetURL}); err != nil {
		return "", "", fmt.Errorf("render password reset body: %w", err)
	}
	html, err := renderBase("Reset your Tindra password", template.HTML(bodyBuf.String()), logoSrc, strings.TrimRight(publicURL, "/"))
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

type alertIssueData struct {
	Title         string
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

type alertEmailData struct {
	RuleName       string
	FiredAt        string
	TriggerLine    string
	ViewURL        string
	ViewLabel      string
	Issues         []alertIssueData
	Monitors       []alertMonitorData
	UptimeMonitors []alertUptimeMonitorData
	Logs           []alertLogData
	MoreCount      int
	MoreLabel      string
}

var alertBodyTmpl = template.Must(template.New("alert-body").Parse(
	`<h1 style="margin:0 0 8px;font-size:20px;font-weight:700;color:#111827;letter-spacing:-0.3px;line-height:1.3;word-break:break-all;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">Alert: {{.RuleName}}</h1>
<p style="margin:0 0 20px;font-size:15px;line-height:1.65;color:#6b7280;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  {{.TriggerLine}}
</p>

{{if .Issues}}
{{range .Issues}}
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:8px;">
  <tr>
    <td style="background-color:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:12px 14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;word-break:break-all;">
      <p style="margin:0 0 5px;font-size:11px;line-height:1;">
        <strong style="color:{{.LevelColor}};text-transform:uppercase;letter-spacing:0.5px;">{{.Level}}</strong>{{if .Environment}}&nbsp;&bull;&nbsp;<span style="color:#6b7280;">{{.Environment}}</span>{{end}}{{if .ProjectName}}&nbsp;&bull;&nbsp;<span style="color:#6b7280;">{{.ProjectName}}</span>{{end}}
      </p>
      <a href="{{.URL}}" style="font-size:13px;font-weight:600;color:#111827;text-decoration:none;line-height:1.4;word-break:break-all;">{{.Title}}</a>
      {{if .OccurredAt}}<p style="margin:5px 0 0;font-size:11px;color:#9ca3af;">{{.OccurredAt}}</p>{{end}}
      {{if .RequestURL}}<p style="margin:7px 0 0;font-size:11px;font-family:'Courier New',Courier,monospace;color:#6b7280;word-break:break-all;">{{if .RequestMethod}}<span style="padding:1px 5px;background-color:#e5e7eb;border-radius:3px;color:#374151;font-weight:600;letter-spacing:0.3px;">{{.RequestMethod}}</span> {{end}}{{.RequestURL}}</p>{{end}}
      {{if .Message}}<p style="margin:7px 0 0;font-size:12px;color:#374151;line-height:1.5;word-break:break-all;">{{.Message}}</p>{{end}}
      {{if .StackFrames}}<div style="margin-top:7px;padding:7px 9px;background-color:#f3f4f6;border-radius:4px;font-family:'Courier New',Courier,monospace;font-size:11px;color:#6b7280;line-height:1.6;">{{range .StackFrames}}<div style="word-break:break-all;">{{.}}</div>{{end}}</div>{{end}}
    </td>
  </tr>
</table>
{{end}}
{{end}}
{{if .Monitors}}
{{range .Monitors}}
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:8px;">
  <tr>
    <td style="background-color:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:12px 14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;word-break:break-all;">
      <p style="margin:0 0 5px;font-size:11px;line-height:1;">
        <strong style="color:{{.StateColor}};text-transform:uppercase;letter-spacing:0.5px;">{{.State}}</strong>&nbsp;&bull;&nbsp;<span style="color:#6b7280;font-family:'Courier New',Courier,monospace;">{{.Schedule}}</span>
      </p>
      <p style="margin:0 0 6px;font-size:13px;font-weight:600;color:#111827;line-height:1.45;">{{.Name}}</p>
      {{if .NextAt}}<p style="margin:0 0 2px;font-size:12px;color:#9ca3af;">Expected at {{.NextAt}}</p>{{end}}
      {{if .LastErrAt}}<p style="margin:0 0 2px;font-size:12px;color:#9ca3af;">Last error at {{.LastErrAt}}</p>{{end}}
      {{if .LastOkAt}}<p style="margin:0;font-size:12px;color:#9ca3af;">Last OK at {{.LastOkAt}}</p>{{end}}
    </td>
  </tr>
</table>
{{end}}
{{end}}
{{if .UptimeMonitors}}
{{range .UptimeMonitors}}
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:8px;">
  <tr>
    <td style="background-color:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:12px 14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;word-break:break-all;">
      <p style="margin:0 0 5px;font-size:11px;line-height:1;">
        <strong style="color:{{.StateColor}};text-transform:uppercase;letter-spacing:0.5px;">{{.State}}</strong>{{if .Received}}&nbsp;&bull;&nbsp;<span style="color:#6b7280;">expected {{.Expected}}, got {{.Received}}</span>{{end}}{{if .ResponseMs}}&nbsp;&bull;&nbsp;<span style="color:#9ca3af;">{{.ResponseMs}}</span>{{end}}
      </p>
      {{if .MonitorURL}}<a href="{{.MonitorURL}}" style="font-size:13px;font-weight:600;color:#111827;text-decoration:none;line-height:1.4;">{{.Name}}</a>{{else}}<p style="margin:0;font-size:13px;font-weight:600;color:#111827;line-height:1.4;">{{.Name}}</p>{{end}}
      <p style="margin:4px 0 0;font-size:11px;font-family:'Courier New',Courier,monospace;color:#6b7280;">{{.URL}}</p>
      {{if .DownSince}}<p style="margin:5px 0 0;font-size:11px;color:#9ca3af;">Down since {{.DownSince}}</p>{{end}}
      {{if .Downtime}}<p style="margin:5px 0 0;font-size:11px;color:#9ca3af;">Was down for {{.Downtime}}</p>{{end}}
      {{if .History}}<p style="margin:7px 0 0;line-height:1;font-size:0;">{{range .History}}<span style="display:inline-block;width:8px;height:8px;border-radius:50%;margin:0 1px;background:{{if eq . "up"}}#22c55e{{else}}#ef4444{{end}};"></span>{{end}}</p>{{end}}
    </td>
  </tr>
</table>
{{end}}
{{end}}
{{if .Logs}}
{{range .Logs}}
<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:8px;">
  <tr>
    <td style="background-color:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:12px 14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;word-break:break-all;">
      <p style="margin:0 0 5px;font-size:11px;line-height:1;">
        <strong style="color:{{.LevelColor}};text-transform:uppercase;letter-spacing:0.5px;">{{.Level}}</strong>{{if .Environment}}&nbsp;&bull;&nbsp;<span style="color:#6b7280;">{{.Environment}}</span>{{end}}{{if .Time}}&nbsp;&bull;&nbsp;<span style="color:#9ca3af;">{{.Time}}</span>{{end}}
      </p>
      <p style="margin:0;font-size:13px;color:#111827;line-height:1.45;font-family:'Courier New',Courier,monospace;">{{.Body}}</p>
    </td>
  </tr>
</table>
{{end}}
{{end}}
{{if .MoreCount}}<p style="margin:0 0 20px;font-size:12px;color:#9ca3af;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">and {{.MoreCount}} more {{.MoreLabel}}</p>{{end}}

<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <td style="background-color:#f9fafb;border-radius:8px;border:1px solid #e5e7eb;padding:16px 20px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
      <p style="margin:0 0 3px;font-size:11px;color:#9ca3af;text-transform:uppercase;letter-spacing:0.6px;font-weight:600;">Fired at</p>
      <p style="margin:0;font-size:14px;color:#111827;font-weight:500;">{{.FiredAt}}</p>
    </td>
  </tr>
</table>

{{if .ViewURL}}
<table cellpadding="0" cellspacing="0" role="presentation" style="margin-bottom:28px;">
  <tr>
    <td style="border-radius:8px;background-color:#6366f1;mso-padding-alt:0;">
      <!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="{{.ViewURL}}" style="height:44px;v-text-anchor:middle;width:180px;" arcsize="18%" stroke="f" fillcolor="#6366f1"><w:anchorlock/><center style="color:#ffffff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:14px;font-weight:600;">{{.ViewLabel}} &rarr;</center></v:roundrect><![endif]-->
      <!--[if !mso]><!--><a href="{{.ViewURL}}" style="display:inline-block;padding:13px 28px;font-size:14px;font-weight:600;color:#ffffff;text-decoration:none;letter-spacing:-0.1px;line-height:1;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">{{.ViewLabel}} &rarr;</a><!--<![endif]-->
    </td>
  </tr>
</table>
{{end}}

<table width="100%" cellpadding="0" cellspacing="0" role="presentation" style="margin:0 0 16px;">
  <tr><td style="border-top:1px solid #f3f4f6;font-size:0;">&nbsp;</td></tr>
</table>

<p style="margin:0;font-size:12px;color:#9ca3af;line-height:1.6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;">
  You&#39;re receiving this because an alert rule in your Tindra instance fired.
  Manage alert rules in <strong style="color:#9ca3af;">Settings &rsaquo; Alerts</strong>.
</p>`))

func alertLevelColor(level string) string {
	switch level {
	case "fatal", "error":
		return "#ef4444"
	case "warning":
		return "#f59e0b"
	case "info":
		return "#3b82f6"
	default:
		return "#9ca3af"
	}
}

// alertTriggerLine returns a human-readable sentence describing why this alert fired.
func alertTriggerLine(p AlertPayload) string {
	if _, isTest := p.Details["test"]; isTest {
		return "This is a test alert. No action required."
	}
	switch p.Trigger {
	case "new_issue":
		count, _ := p.Details["new_issue_count"].(int)
		if count == 1 {
			return "1 new issue was created since the last alert."
		}
		return fmt.Sprintf("%d new issues were created since the last alert.", count)
	case "regressed":
		count, _ := p.Details["regressed_count"].(int)
		if count == 1 {
			return "1 previously resolved issue regressed since the last alert."
		}
		return fmt.Sprintf("%d previously resolved issues regressed since the last alert.", count)
	case "new_or_regressed":
		n, _ := p.Details["new_issue_count"].(int)
		r, _ := p.Details["regressed_count"].(int)
		switch {
		case n > 0 && r > 0:
			return fmt.Sprintf("%d new issue(s) and %d regression(s) since the last alert.", n, r)
		case n > 0:
			if n == 1 {
				return "1 new issue was created since the last alert."
			}
			return fmt.Sprintf("%d new issues were created since the last alert.", n)
		default:
			if r == 1 {
				return "1 previously resolved issue regressed since the last alert."
			}
			return fmt.Sprintf("%d previously resolved issues regressed since the last alert.", r)
		}
	case "event_count":
		count, _ := p.Details["event_count"].(int)
		threshold, _ := p.Details["threshold"].(int)
		windowMins, _ := p.Details["window_mins"].(int)
		return fmt.Sprintf("%d events in the last %d minutes (threshold: %d).", count, windowMins, threshold)
	case "log_count":
		count, _ := p.Details["log_count"].(int)
		threshold, _ := p.Details["threshold"].(int)
		windowMins, _ := p.Details["window_mins"].(int)
		query := logCountQueryLabel(p)
		if query != "" {
			return fmt.Sprintf("%d logs matching %s in the last %d minutes (threshold: %d).", count, query, windowMins, threshold)
		}
		return fmt.Sprintf("%d logs in the last %d minutes (threshold: %d).", count, windowMins, threshold)
	case "cron_missed":
		count, _ := p.Details["missed_count"].(int)
		if count == 1 {
			return "1 cron monitor missed its expected check-in."
		}
		return fmt.Sprintf("%d cron monitors missed their expected check-in.", count)
	case "cron_error":
		count, _ := p.Details["error_count"].(int)
		if count == 1 {
			return "1 cron monitor reported an error."
		}
		return fmt.Sprintf("%d cron monitors reported errors.", count)
	case "uptime_down":
		count, _ := p.Details["down_count"].(int)
		if count == 1 {
			return "1 uptime monitor is down."
		}
		return fmt.Sprintf("%d uptime monitors are down.", count)
	case "uptime_recovered":
		count, _ := p.Details["recovered_count"].(int)
		if count == 1 {
			return "1 uptime monitor has recovered."
		}
		return fmt.Sprintf("%d uptime monitors have recovered.", count)
	case "issue_auto_resolved":
		count, _ := p.Details["resolved_count"].(int)
		if count == 1 {
			return "1 issue was automatically resolved."
		}
		return fmt.Sprintf("%d issues were automatically resolved.", count)
	default:
		return fmt.Sprintf("Alert rule %q fired.", p.RuleName)
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
		cards = append(cards, alertIssueData{
			Title:         iss.Title,
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

func formatDowntime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return "less than a minute"
}

func buildUptimeMonitorCards(monitors []*storage.UptimeMonitor, trigger, base string) []alertUptimeMonitorData {
	cards := make([]alertUptimeMonitorData, 0, len(monitors))
	for _, m := range monitors {
		card := alertUptimeMonitorData{
			Name:     m.Name,
			URL:      m.URL,
			Expected: m.ExpectedCodes,
		}
		if base != "" {
			card.MonitorURL = base + "/monitors"
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
			card.StateColor = "#16a34a"
			if m.WentDownAt != nil && m.LastOkAt != nil {
				card.Downtime = formatDowntime(m.LastOkAt.Sub(*m.WentDownAt))
			}
		} else {
			card.State = "Down"
			card.StateColor = "#ef4444"
			if m.WentDownAt != nil {
				card.DownSince = m.WentDownAt.UTC().Format("2 Jan 2006 15:04 UTC")
			} else if m.LastOkAt != nil {
				card.DownSince = m.LastOkAt.UTC().Format("2 Jan 2006 15:04 UTC") + " (approx)"
			}
		}
		cards = append(cards, card)
	}
	return cards
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
	switch ids := p.Details["project_ids"].(type) {
	case []string:
		for _, id := range ids {
			if id != "" {
				q.Add("project_id", id)
			}
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
	logoSrc := base + "/assets/email-logo.png"

	isCron := payload.Trigger == "cron_missed" || payload.Trigger == "cron_error"
	isUptime := payload.Trigger == "uptime_down" || payload.Trigger == "uptime_recovered"
	isLogs := payload.Trigger == "log_count"

	viewURL := ""
	viewLabel := "View issues"
	if base != "" {
		switch {
		case isCron || isUptime:
			viewURL = base + "/monitors"
			viewLabel = "View monitors"
		case isLogs:
			viewURL = logsViewURL(base, payload)
			viewLabel = "View logs"
		default:
			viewURL = base + "/issues"
		}
	}

	issueCards := buildIssueCards(payload.Issues, payload.ProjectName, payload.ProjectNames, base)
	uptimeCards := buildUptimeMonitorCards(payload.UptimeMonitors, payload.Trigger, base)
	logCards := buildLogCards(payload.Logs)

	var monitorCards []alertMonitorData
	for _, m := range payload.Monitors {
		card := alertMonitorData{
			Name:     m.Name,
			Schedule: m.Schedule,
			State:    m.State,
		}
		switch m.State {
		case "missed":
			card.StateColor = "#ef4444"
		case "error":
			card.StateColor = "#ef4444"
		case "in_progress":
			card.StateColor = "#f59e0b"
		default:
			card.StateColor = "#9ca3af"
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
		if n, ok := payload.Details["new_issue_count"].(int); ok {
			moreCount = n - len(issueCards)
		}
	case "regressed":
		if r, ok := payload.Details["regressed_count"].(int); ok {
			moreCount = r - len(issueCards)
		}
	case "new_or_regressed":
		n, _ := payload.Details["new_issue_count"].(int)
		r, _ := payload.Details["regressed_count"].(int)
		moreCount = (n + r) - len(issueCards)
	case "cron_missed":
		if n, ok := payload.Details["missed_count"].(int); ok {
			moreCount = n - len(monitorCards)
		}
	case "cron_error":
		if n, ok := payload.Details["error_count"].(int); ok {
			moreCount = n - len(monitorCards)
		}
	case "uptime_down":
		if n, ok := payload.Details["down_count"].(int); ok {
			moreCount = n - len(uptimeCards)
		}
	case "uptime_recovered":
		if n, ok := payload.Details["recovered_count"].(int); ok {
			moreCount = n - len(uptimeCards)
		}
	case "log_count":
		if n, ok := payload.Details["log_count"].(int); ok {
			moreCount = n - len(logCards)
		}
	}
	if moreCount < 0 {
		moreCount = 0
	}

	data := alertEmailData{
		RuleName:       payload.RuleName,
		FiredAt:        payload.FiredAt.UTC().Format("Mon, 2 Jan 2006 at 15:04 UTC"),
		TriggerLine:    alertTriggerLine(payload),
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
	if err := alertBodyTmpl.Execute(&bodyBuf, data); err != nil {
		return "", "", fmt.Errorf("render alert body: %w", err)
	}
	html, err := renderBase("[Tindra] "+payload.RuleName, template.HTML(bodyBuf.String()), logoSrc, base)
	if err != nil {
		return "", "", err
	}

	var tb strings.Builder
	fmt.Fprintf(&tb, "Alert: %s\n\n%s\n\n", payload.RuleName, data.TriggerLine)

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
	logoSrc := strings.TrimRight(publicURL, "/") + "/assets/email-logo.png"
	var bodyBuf bytes.Buffer
	if err := inviteBodyTmpl.Execute(&bodyBuf, struct{ InviteURL string }{inviteURL}); err != nil {
		return "", "", fmt.Errorf("render invite body: %w", err)
	}
	html, err := renderBase("You've been invited to Tindra", template.HTML(bodyBuf.String()), logoSrc, strings.TrimRight(publicURL, "/"))
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
