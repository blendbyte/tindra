package digest

import (
	"strings"
	"testing"
	"time"
)

func TestFormatCount(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{999999, "1000.0k"},
		{1000000, "1.0m"},
		{1500000, "1.5m"},
	}
	for _, c := range cases {
		got := formatCount(c.n)
		if got != c.want {
			t.Errorf("formatCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatMs(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{1, "1ms"},
		{500, "500ms"},
		{999, "999ms"},
		{1000, "1s"},
		{1500, "1.5s"},
		{2000, "2s"},
		{10000, "10s"},
	}
	for _, c := range cases {
		got := formatMs(c.ms)
		if got != c.want {
			t.Errorf("formatMs(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestTxDelta(t *testing.T) {
	cases := []struct {
		cur, prev    int64
		wantLabel    string
		wantNonEmpty bool
	}{
		{100, 0, "", false},
		{100, 100, "", false},
		{104, 100, "", false},
		{96, 100, "", false},
		{120, 100, "↑20%", true},
		{80, 100, "↓20%", true},
		{106, 100, "↑6%", true},
		{94, 100, "↓6%", true},
	}
	for _, c := range cases {
		label, color := txDelta(c.cur, c.prev)
		if c.wantNonEmpty {
			if label != c.wantLabel {
				t.Errorf("txDelta(%d,%d) label = %q, want %q", c.cur, c.prev, label, c.wantLabel)
			}
			if color == "" {
				t.Errorf("txDelta(%d,%d) expected non-empty color", c.cur, c.prev)
			}
		} else {
			if label != "" || color != "" {
				t.Errorf("txDelta(%d,%d) = (%q,%q), want (\"\",\"\")", c.cur, c.prev, label, color)
			}
		}
	}
}

func TestTxDelta_positiveIsRed_negativeIsGreen(t *testing.T) {
	_, redColor := txDelta(200, 100)
	if redColor != "#ef4444" {
		t.Errorf("increase color: got %q, want #ef4444", redColor)
	}
	_, greenColor := txDelta(50, 100)
	if greenColor != "#22c55e" {
		t.Errorf("decrease color: got %q, want #22c55e", greenColor)
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

func TestRenderBaseLayout_returnsValidHTML(t *testing.T) {
	html, err := renderBaseLayout("Test Title", "<p>Hello</p>", "https://example.com/logo.png")
	if err != nil {
		t.Fatalf("renderBaseLayout returned error: %v", err)
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("expected DOCTYPE declaration")
	}
	if !strings.Contains(html, "Test Title") {
		t.Error("expected title in output")
	}
	if !strings.Contains(html, "<p>Hello</p>") {
		t.Error("expected body content in output")
	}
	if !strings.Contains(html, "https://example.com/logo.png") {
		t.Error("expected logo src in output")
	}
}

func TestRenderBaseLayout_containsTindraLink(t *testing.T) {
	html, err := renderBaseLayout("T", "<p>body</p>", "logo.png")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "tindra.sh") {
		t.Error("expected tindra.sh link in footer")
	}
}
