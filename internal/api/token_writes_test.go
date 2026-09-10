package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func tokenWriteRequest(h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWritableTokenIssueOperations(t *testing.T) {
	h := issuesHandler()
	_, token, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "issue automation", true)
	require.NoError(t, err)
	fp := uuid.NewString()
	a := seedIssueFull(t, fp+"-a", "Token A")
	b := seedIssueFull(t, fp+"-b", "Token B")
	base := "/api/projects/" + testProject.Slug + "/issues"
	rec := tokenWriteRequest(h, token, "PATCH", base+"/"+a.ID, `{"status":"resolved"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated, err := storage.GetIssue(t.Context(), testPool, a.ID)
	require.NoError(t, err)
	require.Equal(t, "resolved", updated.Status)
	rec = tokenWriteRequest(h, token, "POST", base+"/merge", fmt.Sprintf(`{"issue_ids":[%q,%q]}`, a.ID, b.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	removed, err := storage.GetIssue(t.Context(), testPool, b.ID)
	require.NoError(t, err)
	require.Nil(t, removed)
	rec = tokenWriteRequest(h, token, "POST", base+"/"+a.ID+"/unmerge", fmt.Sprintf(`{"fingerprints":[%q]}`, fp+"-b"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var result struct {
		Issues []*storage.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(t, result.Issues, 1)
	require.Equal(t, testProject.ID, result.Issues[0].ProjectID)
}

func TestWritableTokenSourcemapOperations(t *testing.T) {
	h, _ := smHandler(t)
	_, token, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "release automation", true)
	require.NoError(t, err)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("release", uuid.NewString()))
	require.NoError(t, mw.WriteField("url", "~/app.js"))
	file, err := mw.CreateFormFile("file", "app.js.map")
	require.NoError(t, err)
	_, err = file.Write([]byte(`{"version":3,"sources":["src/app.js"],"mappings":"AAAA"}`))
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	base := "/api/projects/" + testProject.Slug + "/sourcemaps"
	req := httptest.NewRequest("POST", base, &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var sm storage.Sourcemap
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sm))
	require.Equal(t, testProject.ID, sm.ProjectID)
	rec = tokenWriteRequest(h, token, "DELETE", base+"/"+sm.ID, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec = tokenWriteRequest(h, token, "DELETE", base+"/"+sm.ID, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProjectWritesRejectInsufficientAccess(t *testing.T) {
	h, _ := smHandler(t)
	other, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Other token scope")
	require.NoError(t, err)
	_, readOnly, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "read only", false)
	require.NoError(t, err)
	_, wrongProject, err := storage.CreateAPIToken(t.Context(), testPool, other.ID, "other project", true)
	require.NoError(t, err)
	_, writable, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "write", true)
	require.NoError(t, err)
	limitedUser := makeReadOnlyUser(t, uuid.NewString()+"@limited-write.example.com")
	for _, route := range []struct{ method, path string }{
		{"PATCH", "/issues/" + uuid.NewString()},
		{"POST", "/issues/merge"},
		{"POST", "/issues/" + uuid.NewString() + "/unmerge"},
		{"POST", "/sourcemaps"},
		{"DELETE", "/sourcemaps/" + uuid.NewString()},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			path := "/api/projects/" + testProject.Slug + route.path
			for _, token := range []string{readOnly, wrongProject} {
				rec := tokenWriteRequest(h, token, route.method, path, `{}`)
				require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			}
			// A privileged cookie must not upgrade a read-only bearer token.
			for _, token := range []string{"", readOnly} {
				req := httptest.NewRequest(route.method, path, strings.NewReader(`{}`))
				if token == "" {
					req.AddCookie(limitedUser)
				} else {
					req.Header.Set("Authorization", "Bearer "+token)
					req.AddCookie(authCookie())
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				require.Equal(t, http.StatusForbidden, rec.Code)
			}
			rec := tokenWriteRequest(h, writable, route.method, "/api/projects/nonexistent-token-project"+route.path, `{}`)
			require.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestWritableTokenCannotTargetForeignResources(t *testing.T) {
	h, store := smHandler(t)
	other, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Foreign resources")
	require.NoError(t, err)
	foreign, _, _, err := storage.UpsertIssue(t.Context(), testPool, other.ID, uuid.NewString(), "Foreign", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	local := seedIssue(t, uuid.NewString(), "Local")
	sm, err := store.Upload(t.Context(), other.ID, uuid.NewString(), "~/foreign.js", strings.NewReader(`{"version":3,"sources":[],"mappings":""}`))
	require.NoError(t, err)
	_, token, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "scoped writer", true)
	require.NoError(t, err)
	base := "/api/projects/" + testProject.Slug
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/issues/" + foreign.ID, `{"status":"resolved"}`},
		{"POST", "/issues/merge", fmt.Sprintf(`{"issue_ids":[%q,%q]}`, local.ID, foreign.ID)},
		{"POST", "/issues/" + foreign.ID + "/unmerge", `{"fingerprints":["foreign"]}`},
		{"DELETE", "/sourcemaps/" + sm.ID, ""},
	} {
		rec := tokenWriteRequest(h, token, tc.method, base+tc.path, tc.body)
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	}
	unchanged, err := storage.GetIssue(t.Context(), testPool, foreign.ID)
	require.NoError(t, err)
	require.Equal(t, foreign.Status, unchanged.Status)
	maps, err := storage.ListSourcemaps(t.Context(), testPool, other.ID, "")
	require.NoError(t, err)
	require.Len(t, maps, 1)
}

func TestWritableTokenCannotAdministerInstance(t *testing.T) {
	h := tokenHandler()
	_, token, err := storage.CreateAPIToken(t.Context(), testPool, testProject.ID, "not an administrator", true)
	require.NoError(t, err)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/projects"},
		{"PATCH", "/api/projects/" + testProject.ID},
		{"DELETE", "/api/projects/" + testProject.ID},
		{"POST", "/api/invites"},
		{"POST", "/api/alert-rules"},
		{"PATCH", "/api/issues/bulk"},
		{"PATCH", "/api/issues/" + uuid.NewString()},
		{"POST", "/api/tokens"},
	} {
		rec := tokenWriteRequest(h, token, tc.method, tc.path, `{}`)
		require.Equal(t, http.StatusForbidden, rec.Code, tc.path)
	}
	rec := tokenWriteRequest(h, token, "POST", "/api/projects/"+testProject.Slug+"/tokens", `{"name":"new"}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
