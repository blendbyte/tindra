package storage_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestTokenTouchCoalescesDatabaseWrites(t *testing.T) {
	p := setupProjectForTokens(t)
	ctx := context.Background()
	tok, plain, err := storage.CreateAPIToken(ctx, testPool, p.ID, "touch", false)
	require.NoError(t, err)
	hash := storage.HashAPIToken(plain)
	got, err := storage.GetAPITokenByHash(ctx, testPool, hash)
	require.NoError(t, err)
	require.True(t, got.TouchDue)
	_, err = testPool.Exec(ctx, `CREATE TABLE token_touch_writes(n int); INSERT INTO token_touch_writes VALUES (0);
 CREATE FUNCTION count_token_touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE token_touch_writes SET n=n+1; RETURN NEW; END $$;
 CREATE TRIGGER count_token_touch AFTER UPDATE OF last_used_at ON api_tokens FOR EACH ROW EXECUTE FUNCTION count_token_touch()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := testPool.Exec(ctx, "DROP TRIGGER count_token_touch ON api_tokens; DROP FUNCTION count_token_touch(); DROP TABLE token_touch_writes")
		require.NoError(t, e)
	})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); storage.TouchAPIToken(ctx, testPool, tok.ID) }()
	}
	wg.Wait()
	var writes int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT n FROM token_touch_writes").Scan(&writes))
	require.Equal(t, 1, writes)
	got, err = storage.GetAPITokenByHash(ctx, testPool, hash)
	require.NoError(t, err)
	require.False(t, got.TouchDue)
	require.NotNil(t, got.LastUsedAt)
	_, err = testPool.Exec(ctx, "UPDATE api_tokens SET last_used_at=NOW()-interval '61 seconds' WHERE id=$1", tok.ID)
	require.NoError(t, err)
	got, err = storage.GetAPITokenByHash(ctx, testPool, hash)
	require.NoError(t, err)
	require.True(t, got.TouchDue)
	storage.TouchAPIToken(ctx, testPool, tok.ID)
	got, err = storage.GetAPITokenByHash(ctx, testPool, hash)
	require.NoError(t, err)
	require.False(t, got.TouchDue)
	_, err = storage.DeleteAPITokenByID(ctx, testPool, tok.ID)
	require.NoError(t, err)
	got, err = storage.GetAPITokenByHash(ctx, testPool, hash)
	require.NoError(t, err)
	require.Nil(t, got)
}
