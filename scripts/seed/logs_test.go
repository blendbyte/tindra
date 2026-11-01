package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Log lines are read in screenshots, so every placeholder must resolve to a
// value of its own kind: no leftover names, no format verbs, and numbers in
// ranges that make sense for the sentence.
func TestLogBodiesResolveEveryPlaceholder(t *testing.T) {
	for level, templates := range logMessages {
		for _, tmpl := range templates {
			for _, name := range regexp.MustCompile(`\{([a-z_]+)\}`).FindAllStringSubmatch(tmpl, -1) {
				require.NotEqual(t, name[1], logPlaceholder(name[1], seedUsers[0]), "%s template %q has unknown placeholder %s", level, tmpl, name[0])
			}
		}
	}
	for range 500 {
		for level := range logMessages {
			body := logBody(level, randomChoice(seedUsers))
			require.NotRegexp(t, `%[sd%]`, body)
			require.NotRegexp(t, `\{(?:user|email|order|ms|ms_slow|pct|sku|job|api|carrier|table)\}`, body)
			if m := regexp.MustCompile(`disk usage at (\d+)%`).FindStringSubmatch(body); m != nil {
				pct, _ := strconv.Atoi(m[1])
				require.LessOrEqual(t, pct, 100, body)
			}
			if m := regexp.MustCompile(`slow query: (\d+)ms`).FindStringSubmatch(body); m != nil {
				ms, _ := strconv.Atoi(m[1])
				require.Greater(t, ms, 500, "a slow query exceeds its threshold: %s", body)
			}
			require.False(t, strings.Contains(body, "retry ") && regexp.MustCompile(`retry [4-9]/3`).MatchString(body), body)
		}
	}
}

func TestIssueOffsetKeepsBusyIssuesCurrent(t *testing.T) {
	require.Zero(t, issueOffset(5, false))
	require.Zero(t, issueOffset(0, true))
	require.Less(t, issueOffset(1, true), issueOffset(4, true))
	require.LessOrEqual(t, issueOffset(100, true), issueOffsetCap)
}

func TestServerBaseAcceptsURLsAndDSNs(t *testing.T) {
	base, err := serverBase("http://localhost:18080/")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:18080", base)
	base, err = serverBase("https://abc@tindra.example.com/0b0e")
	require.NoError(t, err)
	require.Equal(t, "https://tindra.example.com", base)
	_, err = serverBase("localhost")
	require.Error(t, err)
}

// A log line that names a customer has to belong to that customer: filtering
// the Logs page to Jane Doe showed "failed to send order confirmation to
// tobias.w@example.com" because the message and the user attributes were
// drawn separately.
func TestLogRecordNamesOnlyItsOwnUser(t *testing.T) {
	for range 2000 {
		rec := buildLogRecord("error", time.Now(), []string{"1.0.0"}, []string{"production"})
		body := rec["body"].(string)
		attrs := rec["attributes"].(map[string]any)
		for _, u := range seedUsers {
			if u.email != "" && strings.Contains(body, u.email) {
				require.Equal(t, u.email, attrs["user.email"], body)
			}
		}
	}
}
