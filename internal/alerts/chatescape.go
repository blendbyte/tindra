package alerts

import "strings"

// Alert text such as issue titles and log bodies comes from SDK payloads sent
// with a public DSN, so it is untrusted. Escape it before it is placed into
// chat markup, or a crafted title could ping a whole channel or render a
// disguised link inside a trusted alert.

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeSlack neutralises Slack mrkdwn control sequences: mentions such as
// <!channel>, <url|label> links, and HTML entities.
func escapeSlack(s string) string { return slackEscaper.Replace(s) }

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`,
	">", `\>`, "<", `\<`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`,
)

// escapeMarkdown backslash-escapes characters that start formatting, quotes or
// links in Discord and Teams Markdown.
func escapeMarkdown(s string) string { return markdownEscaper.Replace(s) }

// codeSpan makes text safe inside a single-backtick code span, where escapes
// are not processed, by removing the backticks that would close it.
func codeSpan(s string) string { return strings.ReplaceAll(s, "`", "") }

// slackCodeSpan is codeSpan for Slack, which still parses entities in code.
func slackCodeSpan(s string) string { return escapeSlack(codeSpan(s)) }
