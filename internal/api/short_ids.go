package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/storage"
)

// resolveIssueRef turns an issue reference into an issue ID. It accepts the
// UUID or a short ID such as BACKEND-42, which follows merges. ok is false
// when neither names an issue.
func (ro *router) resolveIssueRef(ctx context.Context, ref string) (id string, ok bool, err error) {
	if _, err := uuid.Parse(ref); err == nil {
		return ref, true, nil
	}
	id, err = storage.ResolveShortID(ctx, ro.pool, ref)
	if err != nil {
		return "", false, err
	}
	return id, id != "", nil
}
