package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/emailstyle"
)

// Counts and durations read as they do in the app.
func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1.0k", 1500: "1.5k", 1000000: "1.0M", 1500000: "1.5M"}
	for n, want := range cases {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatMs(t *testing.T) {
	cases := map[int64]string{0: "<1ms", 1: "1ms", 999: "999ms", 1000: "1.00s", 1990: "1.99s", 10000: "10.0s", 127400: "127.4s"}
	for ms, want := range cases {
		if got := formatMs(ms); got != want {
			t.Errorf("formatMs(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestTxDelta(t *testing.T) {
	cases := []struct {
		cur, prev int64
		wantLabel string
	}{
		{100, 0, ""},
		{100, 100, ""},
		{104, 100, ""},
		{96, 100, ""},
		{120, 100, "↑20%"},
		{80, 100, "↓20%"},
		{106, 100, "↑6%"},
		{94, 100, "↓6%"},
	}
	for _, c := range cases {
		label, color := txDelta(c.cur, c.prev, 100, 100)
		if label != c.wantLabel {
			t.Errorf("txDelta(%d,%d) label = %q, want %q", c.cur, c.prev, label, c.wantLabel)
		}
		if (label == "") != (color == "") {
			t.Errorf("txDelta(%d,%d) = (%q,%q): label and colour disagree", c.cur, c.prev, label, color)
		}
	}
}

// Like the performance page: slower is amber, faster is quiet.
func TestTxDelta_slowerIsAmber_fasterIsQuiet(t *testing.T) {
	if _, c := txDelta(200, 100, 100, 100); c != emailstyle.Warning {
		t.Errorf("increase color: got %q, want %q", c, emailstyle.Warning)
	}
	if _, c := txDelta(50, 100, 100, 100); c != emailstyle.Text3 {
		t.Errorf("decrease color: got %q, want %q", c, emailstyle.Text3)
	}
}

// A change needs 20 requests in both weeks, so a handful never shows as "↑157%".
func TestTxDelta_needsEnoughRequestsInBothWeeks(t *testing.T) {
	for _, counts := range [][2]int64{{19, 100}, {100, 19}, {6, 6}} {
		if label, _ := txDelta(500, 100, counts[0], counts[1]); label != "" {
			t.Errorf("txDelta with counts %v = %q, want no delta", counts, label)
		}
	}
	if label, _ := txDelta(500, 100, 20, 20); label != "↑400%" {
		t.Errorf("txDelta at the threshold = %q, want ↑400%%", label)
	}
}

func TestIssuesVerdict(t *testing.T) {
	cases := []struct {
		s    IssuesSummary
		want string
	}{
		{IssuesSummary{New: 6, Regressed: 1, Ongoing: 21}, "6\u00a0new issues</span> and <span style=\"color:" + emailstyle.Warning + ";font-weight:600;\">1\u00a0regression</span> this week. 21 more are ongoing."},
		{IssuesSummary{New: 1}, "1\u00a0new issue</span> this week."},
		{IssuesSummary{Regressed: 2, Ongoing: 1}, "2\u00a0regressions</span> this week. 1 more is ongoing."},
		{IssuesSummary{Ongoing: 21}, "No new or regressed issues this week. 21 are ongoing."},
		{IssuesSummary{}, "No new or regressed issues this week."},
	}
	for _, c := range cases {
		if got := string(issuesVerdict(c.s)); !strings.HasSuffix(got, c.want) {
			t.Errorf("issuesVerdict(%+v) = %q, want suffix %q", c.s, got, c.want)
		}
	}
	if got := string(issuesVerdict(IssuesSummary{New: 2})); !strings.HasPrefix(got, `<span style="color:`+emailstyle.Accent) {
		t.Errorf("new issues should lead in the accent colour, got %q", got)
	}
}

func TestBarHeights_allZero(t *testing.T) {
	stats := []DayStat{
		{Count: 0},
		{Count: 0},
		{Count: 0},
	}
	heights := barHeights(stats, 40)
	for i, h := range heights {
		if h != 0 {
			t.Errorf("heights[%d] = %d, want 0 for all-zero input", i, h)
		}
	}
}

func TestBarHeights_empty(t *testing.T) {
	heights := barHeights(nil, 40)
	if heights != nil {
		t.Errorf("barHeights(nil) = %v, want nil", heights)
	}
}

func TestBarHeights_maxBecomesMaxPx(t *testing.T) {
	stats := []DayStat{
		{Count: 100},
		{Count: 50},
		{Count: 0},
	}
	heights := barHeights(stats, 40)
	if heights[0] != 40 {
		t.Errorf("max value should map to 40px, got %d", heights[0])
	}
	if heights[1] != 20 {
		t.Errorf("half-max value should map to 20px, got %d", heights[1])
	}
	if heights[2] != 0 {
		t.Errorf("zero value should map to 0px, got %d", heights[2])
	}
}

func TestBarHeights_smallNonZeroGetsMinHeight(t *testing.T) {
	stats := []DayStat{
		{Count: 1000},
		{Count: 1},
	}
	heights := barHeights(stats, 40)
	if heights[1] < 2 {
		t.Errorf("small non-zero count should get minimum height of 2, got %d", heights[1])
	}
}

func TestBarHeights_singleValue(t *testing.T) {
	stats := []DayStat{{Count: 42}}
	heights := barHeights(stats, 40)
	if len(heights) != 1 {
		t.Fatalf("expected 1 height, got %d", len(heights))
	}
	if heights[0] != 40 {
		t.Errorf("single value should scale to maxPx=40, got %d", heights[0])
	}
}

func TestBuildDayLabels(t *testing.T) {
	monday := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	labels := buildDayLabels(monday)
	if len(labels) != 7 {
		t.Fatalf("expected 7 labels, got %d", len(labels))
	}
	if labels[0] != "M" {
		t.Errorf("first label: got %q, want %q", labels[0], "M")
	}
	if labels[1] != "T" {
		t.Errorf("second label: got %q, want %q", labels[1], "T")
	}
	if labels[6] != "S" {
		t.Errorf("seventh label (Sunday): got %q, want %q", labels[6], "S")
	}
}

func TestBuildDayLabels_returnsSevenEntries(t *testing.T) {
	from := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	labels := buildDayLabels(from)
	if len(labels) != 7 {
		t.Fatalf("expected 7 labels, got %d", len(labels))
	}
	for i, l := range labels {
		if l == "" {
			t.Errorf("label[%d] is empty", i)
		}
	}
}

func TestFillDailyStats_fillsMissingDays(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)

	raw := []DayStat{
		{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Count: 10},
		{Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), Count: 20},
	}

	filled := fillDailyStats(raw, from, to)
	if len(filled) != 7 {
		t.Fatalf("expected 7 entries, got %d", len(filled))
	}
	if filled[0].Count != 0 {
		t.Errorf("day 0 (Jan 1) should be 0, got %d", filled[0].Count)
	}
	if filled[1].Count != 10 {
		t.Errorf("day 1 (Jan 2) should be 10, got %d", filled[1].Count)
	}
	if filled[4].Count != 20 {
		t.Errorf("day 4 (Jan 5) should be 20, got %d", filled[4].Count)
	}
	if filled[6].Count != 0 {
		t.Errorf("day 6 (Jan 7) should be 0, got %d", filled[6].Count)
	}
}

func TestFillDailyStats_emptyRaw(t *testing.T) {
	from := time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	filled := fillDailyStats(nil, from, to)
	if len(filled) != 7 {
		t.Fatalf("expected 7 entries for empty input, got %d", len(filled))
	}
	for i, s := range filled {
		if s.Count != 0 {
			t.Errorf("filled[%d].Count = %d, want 0", i, s.Count)
		}
	}
}

func TestDigestTemplateData_IssueURL(t *testing.T) {
	d := &digestTemplateData{baseURL: "https://example.com"}
	got := d.IssueURL("abc-123")
	want := "https://example.com/issues/abc-123"
	if got != want {
		t.Errorf("IssueURL = %q, want %q", got, want)
	}
}

func TestDigestTemplateData_IssueURL_trailingSlash(t *testing.T) {
	d := &digestTemplateData{baseURL: "https://example.com/"}
	got := d.IssueURL("xyz-456")
	want := "https://example.com/issues/xyz-456"
	if got != want {
		t.Errorf("IssueURL = %q, want %q", got, want)
	}
}

func TestReport_dateRange(t *testing.T) {
	r := &Report{
		From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
	}
	got := r.dateRange()
	if !strings.Contains(got, "Jan 1") {
		t.Errorf("dateRange should contain 'Jan 1', got %q", got)
	}
	if !strings.Contains(got, "Jan 7") {
		t.Errorf("dateRange should contain 'Jan 7' (day before To), got %q", got)
	}
	if !strings.Contains(got, "2024") {
		t.Errorf("dateRange should contain year 2024, got %q", got)
	}
}

func TestReport_subject_withName(t *testing.T) {
	r := &Report{
		UserName: "Alice",
		From:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
	}
	got := r.subject()
	if !strings.Contains(got, "Alice") {
		t.Errorf("subject should contain username, got %q", got)
	}
	if !strings.Contains(got, "Weekly update") {
		t.Errorf("subject should contain 'Weekly update', got %q", got)
	}
}

func TestReport_subject_noName(t *testing.T) {
	r := &Report{
		UserName: "",
		From:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
	}
	got := r.subject()
	if !strings.Contains(got, "your projects") {
		t.Errorf("subject should use 'your projects' fallback, got %q", got)
	}
}

func sampleReport() *Report {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	return &Report{
		UserName:    "Bob",
		From:        from,
		To:          to,
		TotalErrors: 1500,
		TotalTx:     3200,
		DailyErrors: []DayStat{
			{Date: from.AddDate(0, 0, 0), Count: 100},
			{Date: from.AddDate(0, 0, 1), Count: 200},
			{Date: from.AddDate(0, 0, 2), Count: 300},
			{Date: from.AddDate(0, 0, 3), Count: 400},
			{Date: from.AddDate(0, 0, 4), Count: 500},
		},
		DailyTx: []DayStat{
			{Date: from.AddDate(0, 0, 0), Count: 500},
		},
		Projects: []ProjectStat{
			{ProjectID: "p1", ProjectName: "My App", Errors: 1000, Transactions: 2000},
			{ProjectID: "p2", ProjectName: "Other App", Errors: 500, Transactions: 1200},
		},
		Issues: IssuesSummary{New: 3, Regressed: 1, Ongoing: 5},
		TopIssues: []TopIssue{
			{IssueID: "issue-1", Title: "Unhandled exception in handler", ProjectName: "My App", Count: 800, Status: "open"},
			{IssueID: "issue-2", Title: "Database connection timeout", ProjectName: "Other App", Count: 200, Status: "regressed"},
		},
		TopTx: []TopTransaction{
			{Transaction: "/api/users", ProjectName: "My App", Count: 1500, P50Ms: 45, P95Ms: 120, P50PrevMs: 40},
			{Transaction: "/api/orders", ProjectName: "My App", Count: 500, P50Ms: 1200, P95Ms: 3000, P50PrevMs: 1100},
		},
		PublicURL: "https://tindra.example.com",
	}
}

func TestRenderDigestEmail_returnsNonEmpty(t *testing.T) {
	r := sampleReport()
	subject, html, text, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("RenderDigestEmail returned error: %v", err)
	}
	if subject == "" {
		t.Error("expected non-empty subject")
	}
	if html == "" {
		t.Error("expected non-empty HTML")
	}
	if text == "" {
		t.Error("expected non-empty plaintext")
	}
}

func TestRenderDigestEmail_subjectContainsUsername(t *testing.T) {
	r := sampleReport()
	subject, _, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(subject, "Bob") {
		t.Errorf("subject should contain username 'Bob', got %q", subject)
	}
}

func TestRenderDigestEmail_htmlContainsProjectName(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "My App") {
		t.Error("HTML should contain project name 'My App'")
	}
}

func TestRenderDigestEmail_htmlContainsIssueTitle(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "Unhandled exception in handler") {
		t.Error("HTML should contain issue title")
	}
}

func TestRenderDigestEmail_htmlContainsIssueCounts(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "3") {
		t.Error("HTML should contain new issue count")
	}
}

func TestRenderDigestEmail_htmlContainsTransactionName(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "/api/users") {
		t.Error("HTML should contain transaction name")
	}
}

func TestRenderDigestEmail_htmlContainsSettingsURL(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "/settings/profile") {
		t.Error("HTML should contain settings URL")
	}
}

func TestRenderDigestEmail_htmlIsValidHTMLShell(t *testing.T) {
	r := sampleReport()
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("HTML should start with DOCTYPE declaration")
	}
	if !strings.Contains(html, "</html>") {
		t.Error("HTML should contain closing </html> tag")
	}
}

func TestRenderDigestEmail_plaintextContainsData(t *testing.T) {
	r := sampleReport()
	_, _, text, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(text, "My App") {
		t.Error("plaintext should contain project name")
	}
	if !strings.Contains(text, "Unhandled exception in handler") {
		t.Error("plaintext should contain issue title")
	}
	if !strings.Contains(text, "/api/users") {
		t.Error("plaintext should contain transaction name")
	}
}

func TestRenderDigestEmail_emptyReport(t *testing.T) {
	r := &Report{
		From:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:        time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
		PublicURL: "https://example.com",
	}
	_, html, text, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatalf("render with empty report: %v", err)
	}
	if html == "" {
		t.Error("expected non-empty HTML even for empty report")
	}
	if text == "" {
		t.Error("expected non-empty text even for empty report")
	}
}

func TestBuildDigestPlaintext_containsHeader(t *testing.T) {
	r := sampleReport()
	dateRange := r.dateRange()
	got := buildDigestPlaintext(r, dateRange)
	if !strings.Contains(got, "Weekly update") {
		t.Error("plaintext should contain 'Weekly update'")
	}
	if !strings.Contains(got, dateRange) {
		t.Error("plaintext should contain date range")
	}
}

func TestBuildDigestPlaintext_containsErrorsAndTx(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "Errors:") {
		t.Error("plaintext should contain Errors label")
	}
	if !strings.Contains(got, "Transactions:") {
		t.Error("plaintext should contain Transactions label")
	}
}

func TestBuildDigestPlaintext_containsProjects(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "By project:") {
		t.Error("plaintext should contain 'By project:' section")
	}
	if !strings.Contains(got, "My App") {
		t.Error("plaintext should contain project name 'My App'")
	}
}

func TestBuildDigestPlaintext_containsIssuesSummary(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "Issues:") {
		t.Error("plaintext should contain issues summary")
	}
	if !strings.Contains(got, "3 new") {
		t.Error("plaintext should mention new issue count")
	}
}

func TestBuildDigestPlaintext_containsTopIssues(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "Issues with the most errors:") {
		t.Error("plaintext should list top issues")
	}
	if !strings.Contains(got, "Unhandled exception in handler") {
		t.Error("plaintext should contain issue title")
	}
}

func TestBuildDigestPlaintext_containsTopTx(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "Most frequent transactions:") {
		t.Error("plaintext should list top transactions")
	}
	if !strings.Contains(got, "/api/users") {
		t.Error("plaintext should contain transaction name")
	}
}

func TestBuildDigestPlaintext_containsSettingsURL(t *testing.T) {
	r := sampleReport()
	got := buildDigestPlaintext(r, r.dateRange())
	if !strings.Contains(got, "settings/profile") {
		t.Error("plaintext should contain settings URL")
	}
}

func TestBuildDigestPlaintext_longTitleTruncated(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	longTitle := strings.Repeat("A", 70)
	r := &Report{
		From:      from,
		To:        from.AddDate(0, 0, 7),
		PublicURL: "https://example.com",
		TopIssues: []TopIssue{
			{IssueID: "x", Title: longTitle, ProjectName: "App", Count: 1},
		},
	}
	got := buildDigestPlaintext(r, r.dateRange())
	if strings.Contains(got, longTitle) {
		t.Error("long title should be truncated in plaintext")
	}
	if !strings.Contains(got, "...") {
		t.Error("truncated title should end with '...'")
	}
}

func TestBuildDigestPlaintext_noProjects(t *testing.T) {
	r := &Report{
		From:        time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
		TotalErrors: 5,
		TotalTx:     10,
		PublicURL:   "https://example.com",
	}
	got := buildDigestPlaintext(r, r.dateRange())
	if strings.Contains(got, "By project:") {
		t.Error("plaintext should not include 'By project:' when there are no projects")
	}
}

// The digest reads like the app: issue types in mono, durations formatted the
// same way, and no change shown without enough requests behind it.
func TestRenderDigestEmail_appConventions(t *testing.T) {
	r := sampleReport()
	r.TopIssues = []TopIssue{{IssueID: "i1", Title: "ChunkLoadError: Loading chunk 482 failed.", ProjectName: "Storefront", Count: 133}}
	r.TopTx = []TopTransaction{
		{Transaction: "/checkout", ProjectName: "Storefront", Count: 540, P50Ms: 1990, P95Ms: 127_400, P50PrevMs: 1420, P50PrevCount: 480},
		{Transaction: "settlement.export", ProjectName: "Payments", Count: 98, P50Ms: 5140, P95Ms: 7120, P50PrevMs: 2000, P50PrevCount: 6},
	}
	_, html, _, err := RenderDigestEmail(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		">ChunkLoadError</span>: Loading chunk 482 failed.",
		"1.99s", "127.4s",
		`color:` + emailstyle.Warning + `;font-size:11px;">↑40%`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(html, "↑157%") {
		t.Error("a change from 6 requests is shown")
	}
	if strings.Contains(html, "ZgotmplZ") || strings.Contains(html, "e+02") {
		t.Error("HTML contains a sanitized value or a number in exponent form")
	}
}
