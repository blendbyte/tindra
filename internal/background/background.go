// Package background runs Tindra's long-lived background jobs.
//
// Every job is supervised: a panic is logged and the job restarts after a
// pause instead of taking the process (and its buffered ingest data) down, and
// Start returns a wait function so shutdown can let jobs finish before the
// database pool closes.
//
// Jobs marked Singleton run on one instance at a time. Instances compete for a
// PostgreSQL advisory lock held on a dedicated connection; the holder runs the
// singleton jobs, the others stand by and take over if it stops. This keeps
// alerts, digests and uptime probes from being sent or run twice when several
// replicas run, including briefly during a rolling deploy.
package background

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is one background loop. Run should return only when ctx ends.
type Job struct {
	Name      string
	Run       func(ctx context.Context)
	Singleton bool // run on one instance only
}

// Tunables, variables so tests can shorten them.
var (
	// leaderRetry is how often a standby instance tries to take over, and how
	// often the leader checks that its lock connection is still alive.
	leaderRetry = 15 * time.Second
	// restartDelay is the pause before a panicked job runs again.
	restartDelay = 30 * time.Second
)

// leaderLockKey shares the (1953066596, n) advisory-lock namespace ("tind")
// used by storage.LockIssueMembership, with its own second key.
const leaderLockKey1, leaderLockKey2 = 1953066596, 2

// Start launches jobs and returns a function that blocks until all of them
// have stopped. Cancel ctx to stop them.
func Start(ctx context.Context, pool *pgxpool.Pool, jobs []Job) (wait func()) {
	var wg sync.WaitGroup
	var singletons []Job
	for _, job := range jobs {
		if job.Singleton {
			singletons = append(singletons, job)
			continue
		}
		wg.Go(func() { supervise(ctx, job) })
	}
	if len(singletons) > 0 {
		wg.Go(func() {
			runAsLeader(ctx, pool.Config().ConnConfig, func(leaderCtx context.Context) {
				var leaderWG sync.WaitGroup
				for _, job := range singletons {
					leaderWG.Go(func() { supervise(leaderCtx, job) })
				}
				leaderWG.Wait()
			})
		})
	}
	return wg.Wait
}

// supervise runs job until ctx ends, restarting it after a panic. A job that
// returns on its own (for example, disabled by configuration) is not restarted.
func supervise(ctx context.Context, job Job) {
	log := slog.With("job", job.Name)
	for ctx.Err() == nil {
		if !runRecovered(ctx, job, log) {
			return
		}
		timer := time.NewTimer(restartDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			log.Info("restarting background job after panic")
		}
	}
}

// runRecovered runs the job once and reports whether it panicked.
func runRecovered(ctx context.Context, job Job, log *slog.Logger) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			log.Error("background job panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	job.Run(ctx)
	return false
}

// runAsLeader repeatedly tries to become leader and runs lead while it is.
// Leadership is a session advisory lock on a dedicated connection, outside the
// pool so it never takes a slot from request handling. Losing the connection
// ends leadership: lead's context is cancelled and waited for before retrying.
func runAsLeader(ctx context.Context, connConfig *pgx.ConnConfig, lead func(context.Context)) {
	for ctx.Err() == nil {
		conn, acquired := tryAcquireLeadership(ctx, connConfig)
		if acquired {
			slog.Info("background: this instance runs the singleton jobs")
			leaderCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				lead(leaderCtx)
			}()
			watchLeadership(leaderCtx, conn, cancel)
			<-done
			cancel()
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Close(closeCtx) // closing the session releases the lock
			closeCancel()
			if ctx.Err() == nil {
				slog.Warn("background: lost leadership, retrying")
			}
		} else if conn != nil {
			_ = conn.Close(context.Background())
		}
		timer := time.NewTimer(leaderRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// tryAcquireLeadership opens a connection and tries the leader lock once.
func tryAcquireLeadership(ctx context.Context, connConfig *pgx.ConnConfig) (*pgx.Conn, bool) {
	conn, err := pgx.ConnectConfig(ctx, connConfig.Copy())
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("background: connect for leader lock", "err", err)
		}
		return nil, false
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, leaderLockKey1, leaderLockKey2).Scan(&acquired); err != nil {
		if ctx.Err() == nil {
			slog.Warn("background: try leader lock", "err", err)
		}
		return conn, false
	}
	return conn, acquired
}

// watchLeadership blocks until leaderCtx ends or the lock connection fails,
// in which case it cancels leadership so jobs stop before another instance
// takes over.
func watchLeadership(leaderCtx context.Context, conn *pgx.Conn, cancel context.CancelFunc) {
	ticker := time.NewTicker(leaderRetry)
	defer ticker.Stop()
	for {
		select {
		case <-leaderCtx.Done():
			return
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(leaderCtx, leaderRetry)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil && leaderCtx.Err() == nil {
				slog.Error("background: leader lock connection lost", "err", err)
				cancel()
				return
			}
		}
	}
}

// Every runs fn immediately and then at each interval until ctx ends. Running
// at start matters for long intervals: a daily job driven only by a ticker
// never runs on a process restarted more often than once a day.
func Every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
