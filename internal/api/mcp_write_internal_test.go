package api

import (
	"context"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestMCPCheckWrite(t *testing.T) {
	token := func(projectID string, writable bool) context.Context {
		ctx := context.WithValue(context.Background(), ctxTokenProjID, projectID)
		return context.WithValue(ctx, ctxTokenWritable, writable)
	}
	session := func(p storage.UserPermissions) context.Context {
		return context.WithValue(context.Background(), ctxUserPerms, &p)
	}

	cases := []struct {
		name         string
		ctx          context.Context
		perm         string
		tokenAllowed bool
		wantErr      bool
	}{
		{"writable scoped token, token-allowed op", token("p1", true), "manage_issues", true, false},
		{"read-only token", token("p1", false), "manage_issues", true, true},
		{"writable token, session-only op", token("p1", true), "manage_alerts", false, true},
		{"unscoped writable token", token("", true), "manage_issues", true, true},
		{"session with permission", session(storage.UserPermissions{ManageAlerts: true}), "manage_alerts", false, false},
		{"session without permission", session(storage.UserPermissions{}), "manage_alerts", false, true},
		{"no identity", context.Background(), "manage_issues", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mcpCheckWrite(tc.ctx, tc.perm, tc.tokenAllowed)
			if (err != nil) != tc.wantErr {
				t.Errorf("mcpCheckWrite() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
