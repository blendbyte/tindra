package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/storage"
)

const maxTeamNameLen = 100

// teamName trims a team name and reports whether it is acceptable.
func teamName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	return name, name != "" && len(name) <= maxTeamNameLen
}

// validUUIDs reports whether every id is a UUID, so a malformed one answers 400
// instead of surfacing as a database error.
func validUUIDs(ids []string) bool {
	for _, id := range ids {
		if uuid.Validate(id) != nil {
			return false
		}
	}
	return true
}

// handleListTeams returns every team with its member ids. It is open to the holders
// of manage_users and manage_projects, who pick teams when editing users and
// project teams.
func (ro *router) handleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := storage.ListTeams(r.Context(), ro.pool)
	if err != nil {
		slog.Error("list teams", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, teams)
}

// handleCreateTeam adds an empty team. Requires manage_users.
func (ro *router) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name, ok := teamName(req.Name)
	if !ok {
		http.Error(w, "name is required and must be at most 100 characters", http.StatusBadRequest)
		return
	}
	team, err := storage.CreateTeam(r.Context(), ro.pool, name)
	if errors.Is(err, storage.ErrTeamNameTaken) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("create team", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "team.created",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &team.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": team.Name},
	})
	writeJSONStatus(w, http.StatusCreated, team)
}

// handleRenameTeam renames a team. Requires manage_users.
func (ro *router) handleRenameTeam(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "teamID")
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name, ok := teamName(req.Name)
	if !ok {
		http.Error(w, "name is required and must be at most 100 characters", http.StatusBadRequest)
		return
	}
	team, err := storage.RenameTeam(r.Context(), ro.pool, id, name)
	if errors.Is(err, storage.ErrTeamNameTaken) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("rename team", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if team == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "team.renamed",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": team.Name},
	})
	writeJSON(w, team)
}

// wantsPreview is true for a request that only asks what a change would do to alert
// rules (?dry_run=true). It runs the same transaction as the real change and rolls it
// back, so the admin sees who stops being notified before confirming.
func wantsPreview(r *http.Request) bool {
	return r.URL.Query().Get("dry_run") == "true"
}

// writePreview answers a preview: the impact on alert rules, or 404 when what the
// change targets does not exist. Errors specific to the change are handled by the caller.
func writePreview(w http.ResponseWriter, impact *storage.AlertImpact, err error, what string) {
	if err != nil {
		slog.Error("preview "+what, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if impact == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, impact)
}

// handleDeleteTeam removes a team. Its members lose the access it gave them, and its
// projects are no longer attached to it. With ?dry_run=true it deletes nothing and
// answers with the alert rules affected. Requires manage_users.
func (ro *router) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "teamID")
	if wantsPreview(r) {
		impact, err := storage.PreviewDeleteTeam(r.Context(), ro.pool, id)
		writePreview(w, impact, err, "delete team")
		return
	}
	deleted, err := storage.DeleteTeam(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete team", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "team.deleted",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleSetTeamMembers replaces a team's members. With ?dry_run=true it changes nothing
// and answers with the alert rules affected. Requires manage_users.
func (ro *router) handleSetTeamMembers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "teamID")
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !validUUIDs(req.UserIDs) {
		http.Error(w, "user_ids must be an array of user ids", http.StatusBadRequest)
		return
	}
	if wantsPreview(r) {
		impact, err := storage.PreviewTeamMembers(r.Context(), ro.pool, id, req.UserIDs)
		if errors.Is(err, storage.ErrUnknownReference) {
			http.Error(w, "unknown user", http.StatusBadRequest)
			return
		}
		writePreview(w, impact, err, "team members")
		return
	}
	team, err := storage.SetTeamMembers(r.Context(), ro.pool, id, req.UserIDs)
	if errors.Is(err, storage.ErrUnknownReference) {
		http.Error(w, "unknown user", http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("set team members", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if team == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "team.members_changed",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"members": len(team.MemberIDs)},
	})
	writeJSON(w, team)
}

// handleListProjectTeams returns the teams of every project, so the project list can
// show and filter by team. Requires manage_projects.
func (ro *router) handleListProjectTeams(w http.ResponseWriter, r *http.Request) {
	entries, err := storage.ListProjectTeams(r.Context(), ro.pool)
	if err != nil {
		slog.Error("list project teams", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}

// handleGetProjectTeams returns the teams a project belongs to. Requires manage_projects.
func (ro *router) handleGetProjectTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := storage.GetProjectTeams(r.Context(), ro.pool, chi.URLParam(r, "projectID"))
	if err != nil {
		slog.Error("get project teams", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if teams == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, teams)
}

// handleSetProjectTeams replaces the teams a project belongs to. Members of those
// teams see the project, and users with access to all projects always do, so a
// project can never become unreachable. With ?dry_run=true it changes nothing and
// answers with the alert rules affected. Requires manage_projects.
func (ro *router) handleSetProjectTeams(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "projectID")
	var req storage.ProjectTeams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !validUUIDs(req.TeamIDs) {
		http.Error(w, "team_ids must be an array of team ids", http.StatusBadRequest)
		return
	}
	if wantsPreview(r) {
		impact, err := storage.PreviewProjectTeams(r.Context(), ro.pool, id, req.TeamIDs)
		if errors.Is(err, storage.ErrUnknownReference) {
			http.Error(w, "unknown team", http.StatusBadRequest)
			return
		}
		writePreview(w, impact, err, "project teams")
		return
	}
	teams, err := storage.SetProjectTeams(r.Context(), ro.pool, id, req.TeamIDs)
	if errors.Is(err, storage.ErrUnknownReference) {
		http.Error(w, "unknown team", http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("set project teams", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if teams == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "project.teams_changed",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"teams": len(teams.TeamIDs)},
	})
	writeJSON(w, teams)
}
