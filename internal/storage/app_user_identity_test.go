package storage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

func TestAppUserIdentityConsistency(t *testing.T) {
	p := setupProjectForLogs(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, id, username, email string
		scrub                     ingest.ScrubConfig
		want                      string
	}{
		{name: "whitespace", id: " \t\u0085\u00a0u-1\u3000\n", want: "u-1"},
		{name: "blank ID", id: " \t", username: " alice ", want: "alice"},
		{name: "blocked ID", id: "u-1", username: "alice", scrub: ingest.ScrubConfig{Fields: []string{"user.id"}}, want: "alice"},
		{name: "pattern ID", id: "alice@example.com", username: "alice", scrub: ingest.ScrubConfig{Patterns: []ingest.ScrubPattern{{Name: "email", Builtin: true, Enabled: true}}}, want: "alice"},
		{name: "all filtered", id: "[Filtered]", email: "[Filtered]", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"user": map[string]string{"id": tc.id, "username": tc.username, "email": tc.email}})
			require.NoError(t, err)
			event := ingest.ScrubEvent(raw, tc.scrub)
			attrs, err := json.Marshal(map[string]string{"user.id": tc.id, "user.username": tc.username, "user.email": tc.email})
			require.NoError(t, err)
			log := ingest.BufferedLog{Attributes: attrs}
			ingest.ScrubLog(&log, tc.scrub)
			tx := ingest.BufferedTransaction{UserID: tc.id, UserUsername: tc.username, UserEmail: tc.email}
			ingest.ScrubTransaction(&tx, tc.scrub)
			require.Equal(t, tc.want, tx.UserIdentity)
			require.Equal(t, tc.want, ingest.ParseSentryUserFromPayload(event).Identity)
			var parsedAttrs map[string]any
			require.NoError(t, json.Unmarshal(log.Attributes, &parsedAttrs))
			require.Equal(t, tc.want, ingest.ParseSentryUserFromAttrs(parsedAttrs).Identity)
			var eventIdentity, logIdentity string
			require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,NOW(),$2) RETURNING COALESCE(user_identity,'')`, p.ID, event).Scan(&eventIdentity))
			require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO logs(project_id,timestamp,level,body,attributes) VALUES($1,NOW(),'info','test',$2) RETURNING COALESCE(user_identity,'')`, p.ID, log.Attributes).Scan(&logIdentity))
			require.Equal(t, tc.want, eventIdentity)
			require.Equal(t, tc.want, logIdentity)
		})
	}
}

func TestLongAppUserIdentity(t *testing.T) {
	p := setupProjectForLogs(t)
	ctx := context.Background()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		sum := sha256.Sum256([]byte{byte(i)})
		b.WriteString(hex.EncodeToString(sum[:]))
	}
	identity := b.String()
	attrs, err := json.Marshal(map[string]string{"user.id": identity})
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO logs(project_id,timestamp,level,body,attributes) VALUES($1,NOW(),'info','long identity',$2)`, p.ID, attrs)
	require.NoError(t, err)
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{{ProjectID: p.ID, User: ingest.SentryUser{Identity: identity, ID: identity}, LastSeen: time.Now()}})
	// The same identity must upsert rather than duplicate, despite its length.
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{{ProjectID: p.ID, User: ingest.SentryUser{Identity: identity, ID: identity, Name: "Updated"}, LastSeen: time.Now()}})
	user, err := storage.GetAppUser(ctx, testPool, []string{p.ID}, identity)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, identity, user.Identity)
	require.Equal(t, "Updated", *user.Name)
	logs, _, err := storage.ListLogs(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}, UserIdentity: identity})
	require.NoError(t, err)
	require.Len(t, logs, 1)
}

// Exercise the migration with existing data, including a long ID that used to
// exceed the new indexes' B-tree limit. Rollback restores the test database.
func TestAppUsersMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	p := setupProjectForLogs(t)
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback(ctx)) }()
	_, err = tx.Exec(ctx, `
 DROP TABLE app_users;
 DROP INDEX issues_last_seen;
 ALTER TABLE events DROP COLUMN user_identity;
 ALTER TABLE logs DROP COLUMN user_identity;
 ALTER TABLE transactions DROP COLUMN user_identity, DROP COLUMN user_id,
 DROP COLUMN user_username, DROP COLUMN user_email, DROP COLUMN user_name;
 DROP FUNCTION app_user_identity_hash(TEXT);
 DROP FUNCTION app_user_identity_field(TEXT);
 `)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO logs(project_id,timestamp,level,body,attributes)
 VALUES($1,NOW(),'info','historical whitespace','{"user.id":"  alice  "}'),
 ($1,NOW(),'info','historical scrubbed','{"user.id":"[Filtered]","user.username":" bob "}'),
 ($1,NOW(),'info','historical anonymous','{"user.id":"[Filtered]"}'),
 ($1,NOW(),'info','historical long',jsonb_build_object('user.id',
 (SELECT string_agg(md5(i::text),'') FROM generate_series(1,200) i)));
 `, p.ID)
	require.NoError(t, err)
	sql, err := migrations.FS.ReadFile("0015_app_users.up.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(sql))
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM app_users WHERE project_id=$1 AND (identity IN ('alice','bob') OR length(identity)=6400)`, p.ID).Scan(&count))
	require.Equal(t, 3, count)
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM app_users WHERE project_id=$1 AND identity='[Filtered]'`, p.ID).Scan(&count))
	require.Zero(t, count)
}

func TestUserQueryParametersRemainLiteral(t *testing.T) {
	ctx := context.Background()
	p := setupProjectForLogs(t)
	identity := `user' OR true -- \ % _`
	attrs, err := json.Marshal(map[string]string{"user.id": identity})
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO logs(project_id,timestamp,level,body,attributes)
 VALUES($1,NOW(),'info','target',$2),($1,NOW(),'info','other','{"user.id":"other"}')`, p.ID, attrs)
	require.NoError(t, err)
	logs, _, err := storage.ListLogs(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}, UserIdentity: identity})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "target", logs[0].Body)
}
