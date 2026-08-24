package main

import (
	"fmt"
	"math/rand"
	"time"
)

// Profiles are only worth looking at when they hang off a transaction, so this
// section emits both sides of the link rather than profiles on their own.
//
// Both wire formats are covered because they behave nothing alike:
//
//	v1 "profile"        one profile per transaction, shipped in the same
//	                    envelope, timed relative to the profile start. This is
//	                    what the PHP and Laravel SDKs send.
//	v2 "profile_chunk"  continuous profiling. The profiler runs across many
//	                    transactions and flushes fixed windows on its own, so a
//	                    chunk here deliberately spans several transactions and
//	                    each one renders its own slice of the same samples.

// profileFrame is one frame in a seeded stack.
type profileFrame struct {
	function string
	module   string
	filename string
	absPath  string
	lineno   int
	inApp    bool
}

// profileLeaf is a branch below the shared prefix, taking weight/total of the
// samples. Uneven weights are the point: a flat profile shows nothing.
type profileLeaf struct {
	weight int
	frames []profileFrame
}

// profileTemplate is a profiled transaction: a stack prefix every sample shares
// plus the branches that take turns underneath it.
type profileTemplate struct {
	txName     string
	op         string
	platform   string
	durationMs [2]int
	root       []profileFrame
	leaves     []profileLeaf
}

// sampleIntervalNs is the default sampling period in both the PHP and Python
// SDKs, 101 Hz. Seeding at the real rate keeps the sample counts and the
// millisecond figures in the UI honest.
const sampleIntervalNs = int64(9_900_990)

var laravelProfiles = []profileTemplate{
	{
		txName:     "GET /api/orders",
		op:         "http.server",
		platform:   "php",
		durationMs: [2]int{280, 620},
		root: []profileFrame{
			{function: "/var/www/app/public/index.php", filename: "public/index.php", absPath: "/var/www/app/public/index.php", lineno: 17},
			{function: `Illuminate\Foundation\Http\Kernel::handle`, module: `Illuminate\Foundation\Http\Kernel`, filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", lineno: 141},
			{function: `Illuminate\Routing\Router::dispatch`, module: `Illuminate\Routing\Router`, filename: "vendor/laravel/framework/src/Illuminate/Routing/Router.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Routing/Router.php", lineno: 714},
			{function: `App\Http\Controllers\OrderController::index`, module: `App\Http\Controllers\OrderController`, filename: "app/Http/Controllers/OrderController.php", absPath: "/var/www/app/app/Http/Controllers/OrderController.php", lineno: 42},
		},
		leaves: []profileLeaf{
			{weight: 46, frames: []profileFrame{
				{function: `Illuminate\Database\Eloquent\Builder::get`, module: `Illuminate\Database\Eloquent\Builder`, filename: "vendor/laravel/framework/src/Illuminate/Database/Eloquent/Builder.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Eloquent/Builder.php", lineno: 690},
				{function: `Illuminate\Database\Connection::select`, module: `Illuminate\Database\Connection`, filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Connection.php", lineno: 405},
				{function: "PDOStatement::execute", module: "PDOStatement", filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Connection.php", lineno: 413},
			}},
			{weight: 22, frames: []profileFrame{
				{function: `App\Http\Resources\OrderResource::toArray`, module: `App\Http\Resources\OrderResource`, filename: "app/Http/Resources/OrderResource.php", absPath: "/var/www/app/app/Http/Resources/OrderResource.php", lineno: 28},
				{function: `App\Support\Money::format`, module: `App\Support\Money`, filename: "app/Support/Money.php", absPath: "/var/www/app/app/Support/Money.php", lineno: 88},
			}},
			{weight: 12, frames: []profileFrame{
				{function: `Illuminate\Cache\Repository::remember`, module: `Illuminate\Cache\Repository`, filename: "vendor/laravel/framework/src/Illuminate/Cache/Repository.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Cache/Repository.php", lineno: 388},
				{function: `Redis::get`, module: "Redis", filename: "vendor/predis/predis/src/Client.php", absPath: "/var/www/app/vendor/predis/predis/src/Client.php", lineno: 331},
			}},
			{weight: 8, frames: []profileFrame{
				{function: `Illuminate\View\Engines\CompilerEngine::get`, module: `Illuminate\View\Engines\CompilerEngine`, filename: "vendor/laravel/framework/src/Illuminate/View/Engines/CompilerEngine.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/View/Engines/CompilerEngine.php", lineno: 58},
			}},
			// No leaf at all: the controller itself was on CPU.
			{weight: 6},
		},
	},
	{
		txName:     "POST /api/checkout",
		op:         "http.server",
		platform:   "php",
		durationMs: [2]int{420, 980},
		root: []profileFrame{
			{function: "/var/www/app/public/index.php", filename: "public/index.php", absPath: "/var/www/app/public/index.php", lineno: 17},
			{function: `Illuminate\Foundation\Http\Kernel::handle`, module: `Illuminate\Foundation\Http\Kernel`, filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", lineno: 141},
			{function: `App\Http\Controllers\CheckoutController::store`, module: `App\Http\Controllers\CheckoutController`, filename: "app/Http/Controllers/CheckoutController.php", absPath: "/var/www/app/app/Http/Controllers/CheckoutController.php", lineno: 71},
		},
		leaves: []profileLeaf{
			{weight: 52, frames: []profileFrame{
				{function: `App\Services\PaymentGateway::charge`, module: `App\Services\PaymentGateway`, filename: "app/Services/PaymentGateway.php", absPath: "/var/www/app/app/Services/PaymentGateway.php", lineno: 114},
				{function: `GuzzleHttp\Client::request`, module: `GuzzleHttp\Client`, filename: "vendor/guzzlehttp/guzzle/src/Client.php", absPath: "/var/www/app/vendor/guzzlehttp/guzzle/src/Client.php", lineno: 187},
				{function: "curl_exec", filename: "vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php", absPath: "/var/www/app/vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php", lineno: 48},
			}},
			{weight: 24, frames: []profileFrame{
				{function: `App\Services\InventoryService::reserve`, module: `App\Services\InventoryService`, filename: "app/Services/InventoryService.php", absPath: "/var/www/app/app/Services/InventoryService.php", lineno: 63},
				{function: "PDOStatement::execute", module: "PDOStatement", filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Database/Connection.php", lineno: 413},
			}},
			{weight: 14, frames: []profileFrame{
				{function: `Illuminate\Validation\Validator::passes`, module: `Illuminate\Validation\Validator`, filename: "vendor/laravel/framework/src/Illuminate/Validation/Validator.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Validation/Validator.php", lineno: 476},
			}},
			{weight: 10},
		},
	},
}

var pythonProfiles = []profileTemplate{
	{
		txName:     "GET /api/reports",
		op:         "http.server",
		platform:   "python",
		durationMs: [2]int{350, 900},
		root: []profileFrame{
			{function: "run", module: "gunicorn.workers.sync", filename: "gunicorn/workers/sync.py", absPath: "/usr/lib/python3.12/site-packages/gunicorn/workers/sync.py", lineno: 128},
			{function: "__call__", module: "django.core.handlers.wsgi", filename: "django/core/handlers/wsgi.py", absPath: "/usr/lib/python3.12/site-packages/django/core/handlers/wsgi.py", lineno: 124},
			{function: "report_list", module: "reports.views", filename: "reports/views.py", absPath: "/srv/app/reports/views.py", lineno: 57, inApp: true},
		},
		leaves: []profileLeaf{
			{weight: 44, frames: []profileFrame{
				{function: "aggregate_totals", module: "reports.services", filename: "reports/services.py", absPath: "/srv/app/reports/services.py", lineno: 91, inApp: true},
				{function: "execute_sql", module: "django.db.models.sql.compiler", filename: "django/db/models/sql/compiler.py", absPath: "/usr/lib/python3.12/site-packages/django/db/models/sql/compiler.py", lineno: 1562},
			}},
			{weight: 28, frames: []profileFrame{
				{function: "serialize_report", module: "reports.serializers", filename: "reports/serializers.py", absPath: "/srv/app/reports/serializers.py", lineno: 31, inApp: true},
				{function: "iterencode", module: "json.encoder", filename: "json/encoder.py", absPath: "/usr/lib/python3.12/json/encoder.py", lineno: 257},
			}},
			{weight: 18, frames: []profileFrame{
				{function: "render_chart", module: "reports.charts", filename: "reports/charts.py", absPath: "/srv/app/reports/charts.py", lineno: 144, inApp: true},
			}},
			{weight: 10},
		},
	},
	{
		txName:     "celery.process_export",
		op:         "queue.task",
		platform:   "python",
		durationMs: [2]int{900, 2400},
		root: []profileFrame{
			{function: "_process_task", module: "celery.worker.worker", filename: "celery/worker/worker.py", absPath: "/usr/lib/python3.12/site-packages/celery/worker/worker.py", lineno: 208},
			{function: "process_export", module: "exports.tasks", filename: "exports/tasks.py", absPath: "/srv/app/exports/tasks.py", lineno: 24, inApp: true},
		},
		leaves: []profileLeaf{
			{weight: 50, frames: []profileFrame{
				{function: "write_rows", module: "exports.writer", filename: "exports/writer.py", absPath: "/srv/app/exports/writer.py", lineno: 88, inApp: true},
				{function: "writerow", module: "csv", filename: "csv.py", absPath: "/usr/lib/python3.12/csv.py", lineno: 154},
			}},
			{weight: 30, frames: []profileFrame{
				{function: "fetch_batch", module: "exports.source", filename: "exports/source.py", absPath: "/srv/app/exports/source.py", lineno: 42, inApp: true},
				{function: "execute_sql", module: "django.db.models.sql.compiler", filename: "django/db/models/sql/compiler.py", absPath: "/usr/lib/python3.12/site-packages/django/db/models/sql/compiler.py", lineno: 1562},
			}},
			{weight: 20, frames: []profileFrame{
				{function: "upload_fileobj", module: "boto3.s3.inject", filename: "boto3/s3/inject.py", absPath: "/usr/lib/python3.12/site-packages/boto3/s3/inject.py", lineno: 610},
			}},
		},
	},
}

// stackBuilder deduplicates frames and stacks the way an SDK does, so the
// seeded payload has the same shape and compression profile as a real one.
type stackBuilder struct {
	frames    []map[string]any
	stacks    [][]int
	frameKeys map[string]int
	stackKeys map[string]int
	platform  string
}

func newStackBuilder(platform string) *stackBuilder {
	return &stackBuilder{
		frameKeys: map[string]int{},
		stackKeys: map[string]int{},
		platform:  platform,
	}
}

func (b *stackBuilder) frameIndex(f profileFrame) int {
	key := fmt.Sprintf("%s|%s|%d", f.function, f.absPath, f.lineno)
	if i, ok := b.frameKeys[key]; ok {
		return i
	}
	out := map[string]any{
		"function": f.function,
		"filename": f.filename,
		"abs_path": f.absPath,
		"lineno":   f.lineno,
	}
	if f.module != "" {
		out["module"] = f.module
	} else if b.platform == "php" {
		// The PHP SDK sends an explicit null when a frame has no class.
		out["module"] = nil
	}
	// Only the Python SDK sets in_app; PHP omits the key entirely, and the UI
	// relies on that difference to decide what to mute.
	if b.platform == "python" {
		out["in_app"] = f.inApp
	}
	b.frames = append(b.frames, out)
	i := len(b.frames) - 1
	b.frameKeys[key] = i
	return i
}

// stackIndex registers a stack, stored leaf first as every SDK sends it.
func (b *stackBuilder) stackIndex(frames []profileFrame) int {
	ids := make([]int, 0, len(frames))
	for i := len(frames) - 1; i >= 0; i-- {
		ids = append(ids, b.frameIndex(frames[i]))
	}
	key := fmt.Sprint(ids)
	if i, ok := b.stackKeys[key]; ok {
		return i
	}
	b.stacks = append(b.stacks, ids)
	i := len(b.stacks) - 1
	b.stackKeys[key] = i
	return i
}

// pickLeaf chooses a branch by weight.
func pickLeaf(leaves []profileLeaf) profileLeaf {
	total := 0
	for _, l := range leaves {
		total += l.weight
	}
	if total <= 0 {
		return leaves[0]
	}
	n := rand.Intn(total) //nolint:gosec - seed data, not security
	for _, l := range leaves {
		n -= l.weight
		if n < 0 {
			return l
		}
	}
	return leaves[len(leaves)-1]
}

// buildV1Profile returns a transaction-based profile shaped like the PHP SDK's
// output, and the transaction event it belongs to. They travel together in one
// envelope, which is what the SDK does and what the ingest path has to handle.
func buildV1Profile(tmpl profileTemplate, ts time.Time, releases, envs []string) (tx, profile map[string]any) {
	durMs := jitterMs(tmpl.durationMs[0], tmpl.durationMs[1])
	end := ts.Add(time.Duration(durMs * float64(time.Millisecond)))

	eventID := newEventID()
	traceID := newEventID()[:32]
	spanID := newEventID()[:16]
	release := randomChoice(releases)
	env := randomChoice(envs)

	b := newStackBuilder("php")
	var samples []map[string]any
	for elapsed := int64(0); elapsed < int64(durMs)*1e6; elapsed += sampleIntervalNs {
		leaf := pickLeaf(tmpl.leaves)
		stack := append(append([]profileFrame{}, tmpl.root...), leaf.frames...)
		samples = append(samples, map[string]any{
			"stack_id":  b.stackIndex(stack),
			"thread_id": "0",
			// The PHP SDK sends this as a JSON number; the Python one sends the
			// same field as a string. Seeding both keeps the parser honest.
			"elapsed_since_start_ns": elapsed,
		})
	}

	tx = map[string]any{
		"event_id":        eventID,
		"transaction":     tmpl.txName,
		"start_timestamp": ts.Format(time.RFC3339Nano),
		"timestamp":       end.Format(time.RFC3339Nano),
		"platform":        "php",
		"environment":     env,
		"release":         release,
		"contexts": map[string]any{
			"trace": map[string]any{
				"trace_id": traceID,
				"span_id":  spanID,
				"op":       tmpl.op,
				"status":   "ok",
			},
		},
		"spans": []any{},
	}

	profile = map[string]any{
		"event_id":    newEventID(),
		"version":     "1",
		"platform":    "php",
		"environment": env,
		"release":     release,
		"timestamp":   ts.Format("2006-01-02T15:04:05.000Z07:00"),
		"device":      map[string]any{"architecture": "x86_64"},
		"os":          map[string]any{"name": "Linux", "version": "6.8.0-45-generic", "build_number": ""},
		"runtime":     map[string]any{"name": "php", "sapi": "fpm-fcgi", "version": "8.3.12"},
		// Singular, as the PHP SDK sends it. Python sends a `transactions` array.
		"transaction": map[string]any{
			"id":               eventID,
			"name":             tmpl.txName,
			"trace_id":         traceID,
			"active_thread_id": "0",
		},
		"profile": map[string]any{
			"frames":  b.frames,
			"stacks":  b.stacks,
			"samples": samples,
		},
	}
	return tx, profile
}

// buildV2Session returns one continuous-profiling chunk plus the transactions
// that ran inside it.
//
// The chunk deliberately covers more wall clock than any single transaction,
// because that is the situation continuous profiling actually produces: the
// profiler runs on its own schedule and each transaction is a slice of the
// same sample stream.
func buildV2Session(tmpl profileTemplate, start time.Time, txCount int, releases, envs []string) (chunk map[string]any, txs []map[string]any) {
	profilerID := newEventID()
	threadID := fmt.Sprintf("%d", 8412331008+rand.Intn(4096)) //nolint:gosec - seed data
	release := randomChoice(releases)
	env := randomChoice(envs)

	// A 30s window, well inside the 66s ceiling the format allows.
	const windowSecs = 30
	b := newStackBuilder("python")
	var samples []map[string]any
	for elapsed := int64(0); elapsed < windowSecs*int64(time.Second); elapsed += sampleIntervalNs {
		leaf := pickLeaf(tmpl.leaves)
		stack := append(append([]profileFrame{}, tmpl.root...), leaf.frames...)
		at := start.Add(time.Duration(elapsed))
		samples = append(samples, map[string]any{
			"stack_id":  b.stackIndex(stack),
			"thread_id": threadID,
			// Absolute Unix seconds, unlike v1's relative nanoseconds.
			"timestamp": float64(at.UnixNano()) / 1e9,
		})
	}

	chunk = map[string]any{
		"chunk_id":    newEventID(),
		"profiler_id": profilerID,
		"version":     "2",
		"platform":    "python",
		"environment": env,
		"release":     release,
		"client_sdk":  map[string]any{"name": "sentry.python.django", "version": "2.41.0"},
		// No top-level timestamp: the Python SDK omits it, so the server has to
		// derive the chunk bounds from the samples.
		"profile": map[string]any{
			"frames":          b.frames,
			"stacks":          b.stacks,
			"samples":         samples,
			"thread_metadata": map[string]any{threadID: map[string]any{"name": "MainThread"}},
		},
	}

	// Space the transactions through the window, each a slice of the samples.
	for i := 0; i < txCount; i++ {
		durMs := jitterMs(tmpl.durationMs[0], tmpl.durationMs[1])
		frac := float64(i+1) / float64(txCount+1)
		offset := time.Duration(frac * windowSecs * float64(time.Second))
		txStart := start.Add(offset)
		txEnd := txStart.Add(time.Duration(durMs * float64(time.Millisecond)))

		txs = append(txs, map[string]any{
			"event_id":        newEventID(),
			"transaction":     tmpl.txName,
			"start_timestamp": txStart.Format(time.RFC3339Nano),
			"timestamp":       txEnd.Format(time.RFC3339Nano),
			"platform":        "python",
			"environment":     env,
			"release":         release,
			"contexts": map[string]any{
				"trace": map[string]any{
					"trace_id": newEventID()[:32],
					"span_id":  newEventID()[:16],
					"op":       tmpl.op,
					"status":   "ok",
					// This is how the server picks one thread's samples out of
					// a chunk that may hold several.
					"data": map[string]any{"thread.id": threadID, "thread.name": "MainThread"},
				},
				// And this is the only link from a transaction to its chunks.
				"profile": map[string]any{"profiler_id": profilerID},
			},
			"spans": []any{},
		})
	}
	return chunk, txs
}

// profileAge keeps seeded profiles inside the default PROFILE_RETENTION_DAYS
// of 7. Spreading them over a week like transactions would have the retention
// worker delete half of them on its first pass.
func profileAge() time.Duration {
	return time.Duration(rand.Intn(3*24*60)) * time.Minute //nolint:gosec - seed data
}
