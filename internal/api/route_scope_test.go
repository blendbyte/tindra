package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// This is the regression guard for project scope. A user who only sees their teams'
// projects must never get data from another team's project, from ANY route. The test
// walks every route the router registers, so a new endpoint that has not been
// classified in routeClasses below fails the build until someone decides how it keeps
// restricted users out. Classifying it is the point: pick the kind that says what the
// route returns, and the test then checks that behaviour for restricted sessions.

type routeKind int

const (
	// kPublic is reachable without a session and returns no project data.
	kPublic routeKind = iota
	// kIngest is authenticated by a DSN key or a monitor id, not by a session.
	kIngest
	// kMCP is the JSON-RPC endpoint, covered by project_access_mcp_test.go.
	kMCP
	// kSelf returns the caller's own account, devices or settings, not project data.
	kSelf
	// kAdmin needs manage_users or manage_projects, which a restricted user cannot hold:
	// the answer must be 403.
	kAdmin
	// kList returns project data filtered by project_id: a restricted user must get none
	// of another team's, whether or not they ask for that project.
	kList
	// kProject addresses a project by slug or id (or names it in the body): another
	// team's project must be refused.
	kProject
	// kObject addresses an object of a project by id: another team's must be refused.
	kObject
)

type routeCase struct {
	kind routeKind
	// body is the JSON request body for a write. {name} placeholders are filled in.
	body string
	// allow2xx is for a route that answers 200 but must neither change nor return
	// another team's data (a bulk update that matches no visible issue).
	allow2xx bool
}

var routeClasses = map[string]routeCase{
	"GET /healthz":                                                   {kind: kPublic},
	"GET /metrics":                                                   {kind: kPublic},
	"GET /assets/email-logo.png":                                     {kind: kPublic},
	"OPTIONS /api/{projectID}/envelope/":                             {kind: kIngest},
	"POST /api/{projectID}/envelope/":                                {kind: kIngest},
	"POST /api/auth/login":                                           {kind: kPublic},
	"POST /api/auth/logout":                                          {kind: kPublic},
	"POST /api/auth/mfa/verify":                                      {kind: kPublic},
	"GET /api/auth/invite/{token}":                                   {kind: kPublic},
	"POST /api/auth/invite/{token}/accept":                           {kind: kPublic},
	"GET /api/auth/password-reset/{token}":                           {kind: kPublic},
	"POST /api/auth/password-reset/{token}":                          {kind: kPublic},
	"POST /api/push/rotate":                                          {kind: kPublic},
	"GET /api/push/config":                                           {kind: kPublic},
	"GET /api/config":                                                {kind: kPublic},
	"GET /api/auth/providers":                                        {kind: kPublic},
	"GET /api/auth/{provider}/redirect":                              {kind: kPublic},
	"GET /api/auth/{provider}/callback":                              {kind: kPublic},
	"GET /api/stats":                                                 {kind: kPublic},
	"GET /api/settings":                                              {kind: kSelf},
	"GET /api/me":                                                    {kind: kSelf},
	"PATCH /api/me":                                                  {kind: kSelf},
	"GET /api/users":                                                 {kind: kSelf},
	"GET /api/projects":                                              {kind: kList},
	"GET /api/projects/metadata":                                     {kind: kList},
	"GET /api/environments":                                          {kind: kList},
	"GET /api/projects/stats":                                        {kind: kList},
	"GET /api/projects/performance":                                  {kind: kList},
	"GET /api/setup-status":                                          {kind: kList},
	"POST /api/projects":                                             {kind: kAdmin},
	"GET /api/projects/{projectID}/quota":                            {kind: kProject},
	"PATCH /api/projects/{projectID}":                                {kind: kAdmin},
	"PATCH /api/projects/{projectID}/privacy":                        {kind: kAdmin},
	"DELETE /api/projects/{projectID}":                               {kind: kAdmin},
	"GET /api/projects/teams":                                        {kind: kAdmin},
	"GET /api/projects/{projectID}/teams":                            {kind: kAdmin},
	"PUT /api/projects/{projectID}/teams":                            {kind: kAdmin},
	"GET /api/app-users":                                             {kind: kList},
	"GET /api/issues":                                                {kind: kList},
	"GET /api/issues/overview":                                       {kind: kList},
	"GET /api/issues/attention":                                      {kind: kList},
	"GET /api/issues/export":                                         {kind: kList},
	"GET /api/issues/{issueID}":                                      {kind: kObject},
	"GET /api/issues/{issueID}/events/histogram":                     {kind: kObject},
	"GET /api/issues/{issueID}/events":                               {kind: kObject},
	"GET /api/issues/{issueID}/events/latest":                        {kind: kObject},
	"GET /api/issues/{issueID}/trace":                                {kind: kObject},
	"GET /api/issues/{issueID}/tags":                                 {kind: kObject},
	"GET /api/issues/{issueID}/history":                              {kind: kObject},
	"GET /api/issues/{issueID}/perf-events":                          {kind: kObject},
	"GET /api/issues/{issueID}/comments":                             {kind: kObject},
	"GET /api/transactions":                                          {kind: kList},
	"GET /api/transactions/summaries":                                {kind: kList},
	"GET /api/transactions/timeseries":                               {kind: kList},
	"GET /api/transactions/counts":                                   {kind: kList},
	"GET /api/transactions/{txID}":                                   {kind: kObject},
	"GET /api/transactions/{txID}/spans":                             {kind: kObject},
	"GET /api/transactions/{txID}/errors":                            {kind: kObject},
	"GET /api/transactions/{txID}/flamegraph":                        {kind: kObject},
	"GET /api/spans/db":                                              {kind: kList},
	"GET /api/spans/db/timeseries":                                   {kind: kList},
	"GET /api/spans/cache":                                           {kind: kList},
	"GET /api/spans/cache/timeseries":                                {kind: kList},
	"GET /api/spans/jobs":                                            {kind: kList},
	"GET /api/spans/jobs/timeseries":                                 {kind: kList},
	"GET /api/spans/samples":                                         {kind: kList},
	"GET /api/vitals":                                                {kind: kList},
	"GET /api/vitals/pages":                                          {kind: kList},
	"GET /api/tokens":                                                {kind: kList},
	"POST /api/tokens":                                               {kind: kAdmin},
	"PATCH /api/tokens/{tokenID}":                                    {kind: kAdmin},
	"DELETE /api/tokens/{tokenID}":                                   {kind: kAdmin},
	"GET /api/logs/count":                                            {kind: kList},
	"GET /api/logs":                                                  {kind: kList},
	"GET /api/releases":                                              {kind: kList},
	"GET /api/releases/metadata":                                     {kind: kList},
	"GET /api/releases/health":                                       {kind: kList},
	"GET /api/releases/{releaseID}":                                  {kind: kObject},
	"GET /api/releases/{releaseID}/issues":                           {kind: kObject},
	"GET /api/releases/{releaseID}/transactions":                     {kind: kObject},
	"GET /api/audit":                                                 {kind: kAdmin},
	"GET /api/instance/health":                                       {kind: kAdmin},
	"GET /api/instance/ingestion":                                    {kind: kAdmin},
	"GET /api/projects/{projectSlug}/issues":                         {kind: kProject},
	"GET /api/projects/{projectSlug}/issues/{issueID}":               {kind: kProject},
	"GET /api/projects/{projectSlug}/issues/{issueID}/fingerprints":  {kind: kProject},
	"GET /api/projects/{projectSlug}/issues/{issueID}/events/latest": {kind: kProject},
	"GET /api/projects/{projectSlug}/transactions":                   {kind: kProject},
	"GET /api/projects/{projectSlug}/transactions/stats":             {kind: kProject},
	"GET /api/projects/{projectSlug}/transactions/{txID}":            {kind: kProject},
	"GET /api/projects/{projectSlug}/sourcemaps":                     {kind: kProject},
	"GET /api/projects/{projectSlug}/setup-status":                   {kind: kProject},
	"POST /api/projects/{projectSlug}/setup-checks":                  {kind: kProject},
	"GET /api/projects/{projectSlug}/setup-sourcemaps":               {kind: kProject},
	"POST /api/issues/{issueID}/comments":                            {kind: kObject, body: `{"body":"x"}`},
	"PUT /api/comments/{commentID}":                                  {kind: kObject, body: `{"body":"x"}`},
	"DELETE /api/comments/{commentID}":                               {kind: kObject},
	"PATCH /api/issues/bulk":                                         {kind: kObject, body: `{"ids":["{issueID}"],"status":"resolved"}`, allow2xx: true},
	"PATCH /api/issues/{issueID}":                                    {kind: kObject, body: `{"status":"resolved"}`},
	"PATCH /api/projects/{projectSlug}/issues/{issueID}":             {kind: kProject, body: `{"status":"resolved"}`},
	"POST /api/projects/{projectSlug}/issues/merge":                  {kind: kProject, body: `{"target_id":"{issueID}","source_ids":["{issueID}"]}`},
	"POST /api/projects/{projectSlug}/issues/{issueID}/unmerge":      {kind: kProject},
	"POST /api/projects/{projectSlug}/sourcemaps":                    {kind: kProject},
	"DELETE /api/projects/{projectSlug}/sourcemaps/{smID}":           {kind: kProject},
	"POST /api/invites":                                              {kind: kAdmin},
	"GET /api/invites":                                               {kind: kAdmin},
	"DELETE /api/invites/{id}":                                       {kind: kAdmin},
	"DELETE /api/users/{userID}":                                     {kind: kAdmin},
	"PUT /api/users/{userID}/permissions":                            {kind: kAdmin},
	"PUT /api/users/{userID}/access":                                 {kind: kAdmin},
	"DELETE /api/users/{userID}/mfa":                                 {kind: kAdmin},
	"PUT /api/users/{userID}/password":                               {kind: kAdmin},
	"POST /api/users/{userID}/password-reset":                        {kind: kAdmin},
	"GET /api/teams":                                                 {kind: kAdmin},
	"POST /api/teams":                                                {kind: kAdmin},
	"PATCH /api/teams/{teamID}":                                      {kind: kAdmin},
	"DELETE /api/teams/{teamID}":                                     {kind: kAdmin},
	"PUT /api/teams/{teamID}/members":                                {kind: kAdmin},
	"POST /mcp":                                                      {kind: kMCP},
	"PATCH /api/me/password":                                         {kind: kSelf},
	"POST /api/auth/mfa/setup":                                       {kind: kSelf},
	"POST /api/auth/mfa/confirm":                                     {kind: kSelf},
	"DELETE /api/auth/mfa":                                           {kind: kSelf},
	"GET /api/push/subscriptions":                                    {kind: kSelf},
	"POST /api/push/subscriptions":                                   {kind: kSelf},
	"POST /api/push/subscriptions/unsubscribe":                       {kind: kSelf},
	"DELETE /api/push/subscriptions/{subscriptionID}":                {kind: kSelf},
	"POST /api/push/subscriptions/{subscriptionID}/test":             {kind: kSelf},
	"GET /api/alert-rules":                                           {kind: kList},
	"GET /api/alert-rules/{ruleID}":                                  {kind: kObject},
	"GET /api/alert-rules/{ruleID}/firings":                          {kind: kObject},
	"POST /api/alert-rules":                                          {kind: kProject, body: `{"name":"x","trigger":"new_issue","channel":"email","email_to":"a@example.com","project_ids":["{projectID}"]}`},
	"PATCH /api/alert-rules/{ruleID}":                                {kind: kObject, body: `{"enabled":false}`},
	"DELETE /api/alert-rules/{ruleID}":                               {kind: kObject},
	"POST /api/alert-rules/{ruleID}/test":                            {kind: kObject},
	"GET /api/cron/{monitorID}":                                      {kind: kIngest},
	"POST /api/cron/{monitorID}":                                     {kind: kIngest},
	"POST /api/cron/{monitorID}/checkins/":                           {kind: kIngest},
	"PUT /api/cron/{monitorID}/checkins/{checkinID}/":                {kind: kIngest},
	"POST /api/cron/{monitorID}/starting":                            {kind: kIngest},
	"POST /api/cron/{monitorID}/finished":                            {kind: kIngest},
	"POST /api/cron/{monitorID}/failed":                              {kind: kIngest},
	"GET /api/monitors":                                              {kind: kList},
	"GET /api/monitors/{monitorID}":                                  {kind: kObject},
	"GET /api/monitors/{monitorID}/checkins":                         {kind: kObject},
	"POST /api/monitors":                                             {kind: kAdmin},
	"PATCH /api/monitors/{monitorID}":                                {kind: kAdmin},
	"DELETE /api/monitors/{monitorID}":                               {kind: kAdmin},
	"GET /api/uptime-monitors":                                       {kind: kList},
	"GET /api/uptime-monitors/{monitorID}":                           {kind: kObject},
	"GET /api/uptime-monitors/{monitorID}/checks":                    {kind: kObject},
	"GET /api/uptime-monitors/{monitorID}/stats":                     {kind: kObject},
	"POST /api/uptime-monitors":                                      {kind: kAdmin},
	"PATCH /api/uptime-monitors/{monitorID}":                         {kind: kAdmin},
	"DELETE /api/uptime-monitors/{monitorID}":                        {kind: kAdmin},
	"GET /.well-known/*":                                             {kind: kPublic},
	"GET /mcp":                                                       {kind: kPublic},
	"DELETE /mcp":                                                    {kind: kPublic},
}

// refused are the statuses that mean "not yours" or "not valid"; none carries data.
var refused = []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
	http.StatusMethodNotAllowed, http.StatusConflict, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity}

func newScopeRouter() *api.Handle {
	return api.NewRouter(api.Config{Pool: testPool, Events: ingest.NewBuffer(1)})
}

func registeredRoutes(t *testing.T, h *api.Handle) []string {
	t.Helper()
	mux, ok := h.Handler.(chi.Routes)
	require.True(t, ok, "the router must stay walkable with chi.Walk")
	var routes []string
	require.NoError(t, chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	}))
	sort.Strings(routes)
	return routes
}

func TestRouteScope_everyRouteIsClassified(t *testing.T) {
	registered := registeredRoutes(t, newScopeRouter())
	known := make(map[string]bool, len(registered))
	var unclassified, stale []string
	for _, route := range registered {
		known[route] = true
		if _, ok := routeClasses[route]; !ok {
			unclassified = append(unclassified, route)
		}
	}
	for route := range routeClasses {
		if !known[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(stale)
	require.Empty(t, unclassified, "these routes are not classified in routeClasses (route_scope_test.go). "+
		"Add each one with the kind that says how a user who only sees their teams' projects is kept out of other teams' data")
	require.Empty(t, stale, "these entries of routeClasses match no registered route; remove them")
}

// hiddenProject is a project whose team has no restricted test user in it, with one
// object of every kind that routes address by id, all carrying marker strings.
type hiddenProject struct {
	project *storage.Project
	issue   *storage.Issue
	params  map[string]string
	markers []string
}

func seedHiddenProject(t *testing.T, member *storage.User) *hiddenProject {
	t.Helper()
	ctx := context.Background()
	mark := "SECRET-" + uuid.NewString()
	p, issue := projectInTeams(t, newTeam(t, member.ID))
	h := &hiddenProject{project: p, issue: issue, params: map[string]string{
		"projectSlug": p.Slug, "projectID": p.ID, "issueID": issue.ID,
	}}
	h.markers = []string{p.ID, p.Slug, issue.ID, issue.Title}
	add := func(param, id string, markers ...string) {
		require.NotEmpty(t, id, param)
		if param != "" {
			h.params[param] = id
		}
		h.markers = append(h.markers, id)
		h.markers = append(h.markers, markers...)
	}

	var txID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, trace_id)
		VALUES ($1, $2, 'http.server', 'ok', 120, NOW(), NOW(), $3) RETURNING id`,
		p.ID, "/"+mark+"/tx", "trace-"+mark).Scan(&txID))
	add("txID", txID, "/"+mark+"/tx")

	var releaseID string
	require.NoError(t, testPool.QueryRow(ctx,
		`INSERT INTO releases (project_id, version) VALUES ($1, $2) RETURNING id`, p.ID, "rel-"+mark).Scan(&releaseID))
	add("releaseID", releaseID, "rel-"+mark)

	tok, _, err := storage.CreateAPIToken(ctx, testPool, p.ID, "tok-"+mark, false)
	require.NoError(t, err)
	add("tokenID", tok.ID, "tok-"+mark)

	comment, err := storage.CreateComment(ctx, testPool, issue.ID, testUser.ID, "comment "+mark)
	require.NoError(t, err)
	add("commentID", comment.ID, "comment "+mark)

	sm, err := storage.UpsertSourcemap(ctx, testPool, &storage.Sourcemap{ProjectID: p.ID, Release: "rel-" + mark, URL: "~/" + mark + ".js.map", ContentHash: "hash", SizeBytes: 1})
	require.NoError(t, err)
	add("smID", sm.ID, mark+".js.map")

	rule := alertRuleFor(t, mark, p.ID)
	add("ruleID", rule.ID, rule.Name)

	cron, err := storage.CreateCronMonitor(ctx, testPool, &storage.CronMonitor{ProjectID: p.ID, Name: "cron " + mark, Schedule: "* * * * *", GracePeriodSecs: 60})
	require.NoError(t, err)
	add("cronID", cron.ID, "cron "+mark)

	up, err := storage.CreateUptimeMonitor(ctx, testPool, &storage.UptimeMonitor{ProjectID: p.ID, Name: "up " + mark, URL: "https://example.com/" + mark,
		Method: "GET", IntervalSecs: 60, TimeoutSecs: 10, ExpectedCodes: "200"})
	require.NoError(t, err)
	add("uptimeID", up.ID, "up "+mark, "example.com/"+mark)

	_, err = testPool.Exec(ctx, `INSERT INTO logs (project_id, timestamp, received_at, level, body, attributes)
		VALUES ($1, NOW(), NOW(), 'error', $2, '{}')`, p.ID, "log "+mark)
	require.NoError(t, err)
	h.markers = append(h.markers, "log "+mark)

	_, err = testPool.Exec(ctx, `INSERT INTO app_users (project_id, identity, user_id, email, last_seen)
		VALUES ($1, $2, $2, $3, $4)`, p.ID, "user-"+mark, mark+"@example.com", time.Now())
	require.NoError(t, err)
	h.markers = append(h.markers, "user-"+mark, mark+"@example.com")
	return h
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// fill replaces {name} placeholders. A name the hidden project does not provide
// (user, team, invite or device ids) gets a random id, which is only legitimate on
// routes that are not about project data. Cron and uptime monitors share a name.
func (h *hiddenProject) fill(route, text string) string {
	return placeholder.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		if name == "monitorID" {
			if strings.Contains(route, "/uptime-monitors") {
				return h.params["uptimeID"]
			}
			return h.params["cronID"]
		}
		if v, ok := h.params[name]; ok {
			return v
		}
		return uuid.NewString()
	})
}

func (h *hiddenProject) call(handler http.Handler, cookie *http.Cookie, route string, rc routeCase, filter bool) *httptest.ResponseRecorder {
	method, pattern, _ := strings.Cut(route, " ")
	path := h.fill(route, pattern)
	if rc.kind == kList && filter {
		path += "?project_id=" + h.project.ID
	}
	body := h.fill(route, rc.body)
	if body == "" && method != http.MethodGet && method != http.MethodDelete {
		body = "{}"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// exercised says whether a kind is called with a restricted session.
func exercised(k routeKind) bool {
	return k == kAdmin || k == kList || k == kProject || k == kObject
}

func requireNoData(t *testing.T, h *hiddenProject, route string, rc routeCase, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	for _, marker := range h.markers {
		require.NotContains(t, body, marker, "%s (status %d) returned data of a project the user has no access to", route, rec.Code)
	}
	switch rc.kind {
	case kAdmin:
		require.Equal(t, http.StatusForbidden, rec.Code, "%s needs manage_users or manage_projects, which a restricted user cannot hold", route)
	case kList:
		require.Contains(t, []int{http.StatusOK, http.StatusBadRequest}, rec.Code, route)
	default:
		if rc.allow2xx && rec.Code < 300 {
			return
		}
		require.Contains(t, refused, rec.Code, "%s must refuse a project the user has no access to", route)
	}
}

func TestRouteScope_restrictedSessionsGetNoDataFromOtherTeams(t *testing.T) {
	handler := newScopeRouter()
	insider, insiderCookie := restrictedSession(t) // in the hidden project's team: the positive control
	hidden := seedHiddenProject(t, insider)

	_, noTeam := restrictedSession(t)
	otherUser, inAnotherTeam := restrictedSession(t)
	restrictedProject(t, otherUser.ID) // a visible project, so the allowed list is not empty
	_, canTriage := restrictedWith(t, storage.UserPermissions{ManageIssues: true, ManageAlerts: true})
	sessions := map[string]*http.Cookie{"no team": noTeam, "another team": inAnotherTeam, "manage_issues and manage_alerts": canTriage}

	routes := make([]string, 0, len(routeClasses))
	for route := range routeClasses {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	for _, route := range routes {
		rc := routeClasses[route]
		if !exercised(rc.kind) {
			continue
		}
		for name, cookie := range sessions {
			t.Run(route+" as "+name, func(t *testing.T) {
				requireNoData(t, hidden, route, rc, hidden.call(handler, cookie, route, rc, true))
				if rc.kind == kList {
					requireNoData(t, hidden, route, rc, hidden.call(handler, cookie, route, rc, false))
				}
			})
		}
	}

	// The same hidden objects are visible to the team that owns them, so the checks above
	// are not passing because the fixtures are unreachable.
	t.Run("positive control: the owning team sees them", func(t *testing.T) {
		require.Contains(t, projectIDsIn(t, accessGet(t, insiderCookie, "/api/projects")), hidden.project.ID)
		for _, path := range []string{
			"/api/issues/" + hidden.issue.ID,
			"/api/projects/" + hidden.project.Slug + "/issues",
			"/api/transactions/" + hidden.params["txID"],
			"/api/releases/" + hidden.params["releaseID"],
			"/api/alert-rules/" + hidden.params["ruleID"],
			"/api/monitors/" + hidden.params["cronID"],
			"/api/uptime-monitors/" + hidden.params["uptimeID"],
		} {
			require.Equal(t, http.StatusOK, accessGet(t, insiderCookie, path).Code, path)
		}
		require.Contains(t, accessGet(t, insiderCookie, "/api/issues").Body.String(), hidden.issue.ID)
	})
}

func TestRouteScope_kindsAreWhatTheyClaim(t *testing.T) {
	// A kAdmin route must really be gated by an administration permission, not just look
	// like it: a user who sees every project and holds none of them is refused too.
	handler := newScopeRouter()
	insider, _ := restrictedSession(t)
	hidden := seedHiddenProject(t, insider)
	_, plain := fullAccessWith(t, storage.UserPermissions{})
	for route, rc := range routeClasses {
		if rc.kind != kAdmin {
			continue
		}
		rec := hidden.call(handler, plain, route, rc, true)
		require.Equal(t, http.StatusForbidden, rec.Code, fmt.Sprintf("%s is classified kAdmin but a user without the permission got %d", route, rec.Code))
	}
	require.True(t, slices.Contains(refused, http.StatusForbidden))
}
