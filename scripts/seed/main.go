// Seed script: delivers realistic issue and transaction data to a Tindra DSN.
//
// Usage:
//
//	go run scripts/seed/main.go [--type=TYPE] [--seed=SECTIONS] [--db=POSTGRES_URL] <DSN>
//	go run scripts/seed/main.go --list
//
// DSN format: http://PUBLIC_KEY@HOST/PROJECT_ID
//
// --seed accepts a comma-separated list of sections, or "all" (default):
//
//	issues        error events
//	transactions  transactions + spans (also feeds queries/caches/jobs/browser views)
//	logs          structured log records
//	monitors      cron monitors (requires --db)
//
// The sub-views queries, caches, jobs, browser are aliases for transactions.
//
// Examples:
//
//	go run scripts/seed/main.go http://abc123@localhost:8080/my-project
//	go run scripts/seed/main.go --seed=issues,logs http://abc123@localhost:8080/my-project
//	go run scripts/seed/main.go --seed=monitors --db=postgres://localhost/tindra http://abc123@localhost:8080/my-project
//	go run scripts/seed/main.go --list
package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DSN parsing ---

type dsn struct {
	publicKey string
	baseURL   string
	projectID string
}

func parseDSN(raw string) (dsn, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return dsn{}, fmt.Errorf("invalid DSN: %w", err)
	}
	if u.User == nil {
		return dsn{}, fmt.Errorf("DSN must contain public key as username: http://PUBLIC_KEY@host/project_id")
	}
	pk := u.User.Username()
	if pk == "" {
		return dsn{}, fmt.Errorf("DSN public key is empty")
	}
	projectID := strings.TrimPrefix(path.Clean(u.Path), "/")
	if projectID == "" || projectID == "." {
		return dsn{}, fmt.Errorf("DSN must contain project ID as URL path")
	}
	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	return dsn{publicKey: pk, baseURL: base, projectID: projectID}, nil
}

func (d dsn) envelopeURL() string {
	return fmt.Sprintf("%s/api/%s/envelope/", d.baseURL, d.projectID)
}

// --- Envelope helpers ---

func newEventID() string {
	b := make([]byte, 16)
	crand.Read(b) //nolint:errcheck
	return fmt.Sprintf("%032x", b)
}

func buildEnvelope(items []envelopeItem) []byte {
	var buf bytes.Buffer
	header := map[string]any{
		"event_id": newEventID(),
		"sent_at":  time.Now().UTC().Format(time.RFC3339),
	}
	enc := json.NewEncoder(&buf)
	enc.Encode(header) //nolint:errcheck
	for _, item := range items {
		payload, _ := json.Marshal(item.payload)
		itemHeader := map[string]any{
			"type":   item.typ,
			"length": len(payload),
		}
		enc.Encode(itemHeader) //nolint:errcheck
		buf.Write(payload)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

type envelopeItem struct {
	typ     string
	payload any
}

func send(target dsn, envelope []byte) (retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target.envelopeURL(), bytes.NewReader(envelope))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth",
		fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s", target.publicKey))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := 60 * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, e := strconv.Atoi(ra); e == nil {
				wait = time.Duration(secs) * time.Second
			}
		}
		return wait, nil
	}
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("server returned %s", resp.Status)
	}
	return 0, nil
}

// --- Common fixtures ---

var defaultEnvironments = []string{"production", "production", "production", "staging"}

type seedUser struct {
	id       string
	username string
	email    string
	name     string
	ip       string
}

var seedUsers = []seedUser{
	{"101", "jdoe", "jane.doe@acme.io", "Jane Doe", "203.0.113.42"},
	{"204", "m_chen", "marcus.chen@startup.dev", "Marcus Chen", "198.51.100.17"},
	{"317", "priya_k", "priya@example.com", "Priya Krishnamurthy", "203.0.113.91"},
	{"482", "tobiasw", "tobias.w@corp.net", "Tobias Weber", "198.51.100.55"},
	{"539", "lfernandez", "l.fernandez@agency.io", "Lucía Fernández", "203.0.113.8"},
	{"601", "anon_user", "", "", "198.51.100.200"},
	{"718", "saraht", "sarah.thornton@bigco.com", "Sarah Thornton", "203.0.113.34"},
	{"825", "devraj_m", "devraj@saas.app", "Devraj Mehta", "198.51.100.77"},
}

func weightedChoice(choices []string, weights []int) string {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := rand.Intn(total) //nolint:gosec
	for i, w := range weights {
		n -= w
		if n < 0 {
			return choices[i]
		}
	}
	return choices[len(choices)-1]
}

func randomChoice[T any](slice []T) T {
	return slice[rand.Intn(len(slice))] //nolint:gosec
}

func jitterMs(min, max int) float64 {
	return float64(min) + rand.Float64()*float64(max-min) //nolint:gosec
}

func randomPast(maxAgo time.Duration) time.Time {
	ago := time.Duration(rand.Int63n(int64(maxAgo))) //nolint:gosec
	return time.Now().UTC().Add(-ago)
}

// trafficBiasedTime returns a random time within maxAgo with the hour weighted
// towards business hours (09–17) so heatmaps show a realistic daily rhythm.
func trafficBiasedTime(maxAgo time.Duration) time.Time {
	t := randomPast(maxAgo)
	h := weightedHour()
	m := rand.Intn(60) //nolint:gosec
	s := rand.Intn(60) //nolint:gosec
	return time.Date(t.Year(), t.Month(), t.Day(), h, m, s, 0, time.UTC)
}

// weightedHour picks an hour 0-23 biased towards working hours.
func weightedHour() int {
	weights := [24]int{
		1, 1, 1, 1, 1, 1, 2, // 00–06 night
		4, 6, // 07–08 morning ramp
		9, 10, 10, 9, 8, 9, 10, 9, 8, // 09–17 business hours
		6, 5, 4, 3, 2, 1, // 18–23 evening wind-down
	}
	total := 0
	for _, w := range weights {
		total += w
	}
	n := rand.Intn(total) //nolint:gosec
	for i, w := range weights {
		n -= w
		if n < 0 {
			return i
		}
	}
	return 12
}

func boolPtr(b bool) *bool { return &b }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// --- Template types ---

type issueTemplate struct {
	excType     string
	excValue    string
	level       string
	platform    string
	transaction string
	fingerprint []string
	stacktrace  []stackFrame
	handled     *bool
	hasUser     bool
}

type stackFrame struct {
	function    string
	module      string
	filename    string
	lineno      int
	contextLine string
	preContext  []string
	postContext []string
}

type txTemplate struct {
	name       string
	op         string
	durationMs [2]int
	platform   string
	spans      []spanNode
}

// spanNode describes one span in a waterfall tree.
// Children run sequentially by default; set concurrent=true on a node to make it
// start in parallel with its immediately preceding sibling instead of after it.
type spanNode struct {
	op          string
	description string
	durationMs  [2]int
	concurrent  bool
	children    []spanNode
}

type projectDef struct {
	name         string
	description  string
	releases     []string
	environments []string
	issues       []issueTemplate
	issueCounts  []int
	txs          []txTemplate
	txCounts     []int
}

// --- Platform helpers ---

func buildTags(platform string) map[string]string {
	tags := map[string]string{
		"server_name": weightedChoice(
			[]string{"web-01", "web-02", "web-03"},
			[]int{50, 30, 20},
		),
	}
	switch platform {
	case "php":
		tags["php.version"] = weightedChoice(
			[]string{"8.2.28", "8.3.19", "8.4.6"},
			[]int{25, 50, 25},
		)
		tags["laravel.version"] = "11.9.2"
		tags["os.name"] = "Linux"
		tags["server_name"] = weightedChoice(
			[]string{"web-01", "web-02", "worker-01"},
			[]int{40, 40, 20},
		)
	case "javascript":
		tags["browser.name"] = weightedChoice(
			[]string{"Chrome", "Firefox", "Safari", "Edge"},
			[]int{62, 18, 14, 6},
		)
		tags["browser.version"] = weightedChoice(
			[]string{"120.0", "121.0", "122.0", "123.0"},
			[]int{10, 25, 40, 25},
		)
		tags["os.name"] = weightedChoice(
			[]string{"macOS", "Windows", "Linux"},
			[]int{45, 38, 17},
		)
		tags["url"] = randomChoice([]string{
			"/dashboard", "/issues", "/settings", "/transactions",
		})
	case "go":
		tags["go.version"] = weightedChoice(
			[]string{"go1.22", "go1.23", "go1.24"},
			[]int{20, 50, 30},
		)
		tags["os.name"] = weightedChoice(
			[]string{"Linux", "macOS"},
			[]int{85, 15},
		)
	case "python":
		tags["python.version"] = weightedChoice(
			[]string{"3.11", "3.12", "3.13"},
			[]int{30, 50, 20},
		)
		tags["os.name"] = "Linux"
	case "ruby":
		tags["ruby.version"] = weightedChoice(
			[]string{"3.2.0", "3.3.0"},
			[]int{40, 60},
		)
		tags["os.name"] = "Linux"
	}
	return tags
}

func buildContexts(platform string) map[string]any {
	osName := weightedChoice([]string{"Linux", "macOS", "Windows"}, []int{60, 25, 15})
	osVersion := map[string]string{
		"Linux":   weightedChoice([]string{"6.1.0", "6.6.0", "6.8.0"}, []int{20, 40, 40}),
		"macOS":   weightedChoice([]string{"14.4.1", "15.0", "15.3.2"}, []int{20, 35, 45}),
		"Windows": weightedChoice([]string{"10.0.19045", "11.0.22631"}, []int{30, 70}),
	}[osName]

	ctx := map[string]any{
		"os": map[string]any{
			"name":    osName,
			"version": osVersion,
		},
	}

	switch platform {
	case "php":
		ctx["os"] = map[string]any{"name": "Linux", "version": "6.6.0"}
		ctx["runtime"] = map[string]any{
			"name":    "php",
			"version": weightedChoice([]string{"8.2.28", "8.3.19", "8.4.6"}, []int{25, 50, 25}),
		}
		ctx["app"] = map[string]any{
			"app_name":       "laravel",
			"app_version":    "11.9.2",
			"app_start_time": time.Now().UTC().Add(-time.Duration(rand.Intn(120)) * time.Second).Format(time.RFC3339),
			"in_background":  false,
		}
	case "go":
		ctx["runtime"] = map[string]any{
			"name":            "go",
			"version":         weightedChoice([]string{"go1.22.5", "go1.23.4", "go1.24.1"}, []int{15, 40, 45}),
			"go_numcpu":       weightedChoice([]string{"2", "4", "8", "16"}, []int{10, 30, 40, 20}),
			"go_maxprocs":     "4",
			"go_numgoroutine": weightedChoice([]string{"18", "34", "52", "91"}, []int{25, 35, 25, 15}),
		}
	case "python":
		ctx["runtime"] = map[string]any{
			"name":    "CPython",
			"version": weightedChoice([]string{"3.11.9", "3.12.3", "3.13.0"}, []int{25, 50, 25}),
			"build":   "main",
		}
	case "javascript":
		browser := weightedChoice([]string{"Chrome", "Firefox", "Safari", "Edge"}, []int{62, 18, 14, 6})
		browserVersion := map[string]string{
			"Chrome":  weightedChoice([]string{"122.0.6261", "123.0.6312", "124.0.6367"}, []int{20, 40, 40}),
			"Firefox": weightedChoice([]string{"123.0", "124.0", "125.0"}, []int{25, 40, 35}),
			"Safari":  weightedChoice([]string{"17.3.1", "17.4", "17.4.1"}, []int{20, 35, 45}),
			"Edge":    weightedChoice([]string{"122.0.2365", "123.0.2420"}, []int{40, 60}),
		}[browser]
		ctx["browser"] = map[string]any{
			"name":    browser,
			"version": browserVersion,
		}
		ctx["runtime"] = map[string]any{
			"name":    "node",
			"version": weightedChoice([]string{"20.11.1", "22.1.0", "22.14.0"}, []int{30, 30, 40}),
		}
	case "ruby":
		ctx["runtime"] = map[string]any{
			"name":    "ruby",
			"version": weightedChoice([]string{"3.2.4", "3.3.0", "3.3.1"}, []int{25, 40, 35}),
			"vm":      "MRI",
		}
	}

	return ctx
}

var modulesByPlatform = map[string]map[string]string{
	"php": {
		"laravel/framework":          "11.9.2",
		"sentry/sentry-laravel":      "4.9.0",
		"guzzlehttp/guzzle":          "7.9.2",
		"predis/predis":              "2.3.0",
		"stripe/stripe-php":          "16.4.0",
		"spatie/laravel-permission":  "6.10.0",
		"spatie/laravel-activitylog": "4.9.0",
		"league/flysystem":           "3.29.1",
		"barryvdh/laravel-debugbar":  "3.14.10",
		"laravel/sanctum":            "4.0.7",
		"doctrine/dbal":              "4.2.1",
		"illuminate/database":        "11.9.2",
		"illuminate/queue":           "11.9.2",
	},
	"go": {
		"github.com/jackc/pgx/v5":              "5.7.2",
		"github.com/go-chi/chi/v5":             "5.2.1",
		"github.com/golang-migrate/migrate/v4": "4.18.2",
		"golang.org/x/crypto":                  "0.37.0",
		"golang.org/x/net":                     "0.39.0",
		"github.com/google/uuid":               "1.6.0",
		"github.com/lmittmann/tint":            "1.0.7",
	},
	"python": {
		"django":          "5.1.4",
		"openai":          "1.58.1",
		"requests":        "2.32.3",
		"psycopg2-binary": "2.9.10",
		"celery":          "5.4.0",
		"redis":           "5.2.1",
		"pydantic":        "2.10.4",
		"uvicorn":         "0.34.0",
		"httpx":           "0.28.1",
		"boto3":           "1.35.90",
		"sentry-sdk":      "2.19.2",
		"fastapi":         "0.115.5",
		"sqlalchemy":      "2.0.36",
	},
	"javascript": {
		"vue":                 "3.5.13",
		"@tanstack/vue-query": "5.62.1",
		"vue-router":          "4.5.0",
		"pinia":               "2.3.0",
		"axios":               "1.7.9",
		"zod":                 "3.24.1",
		"date-fns":            "4.1.0",
		"lucide-vue-next":     "0.474.0",
		"@vueuse/core":        "12.4.0",
		"vite":                "6.0.11",
		"typescript":          "5.7.3",
		"@sentry/vue":         "8.48.0",
	},
	"ruby": {
		"rails":        "7.1.5",
		"activerecord": "7.1.5",
		"pg":           "1.5.9",
		"puma":         "6.5.0",
		"sidekiq":      "7.3.9",
		"devise":       "4.9.4",
		"pundit":       "2.4.0",
		"sentry-ruby":  "5.22.1",
		"sentry-rails": "5.22.1",
	},
}

// =============================================================================
// Laravel / PHP project
// =============================================================================

var laravelReleases = []string{"2.8.0", "2.9.0", "2.9.1", "3.0.0-beta.1"}

var laravelIssues = []issueTemplate{
	{
		excType:     "Illuminate\\Database\\QueryException",
		excValue:    "SQLSTATE[23000]: Integrity constraint violation: 1062 Duplicate entry 'alice@example.com' for key 'users_email_unique' (SQL: insert into `users` (`name`, `email`, `password`, `updated_at`, `created_at`) values (Alice, alice@example.com, $2y$12$hashedvalue, 2024-03-15 09:14:22, 2024-03-15 09:14:22))",
		level:       "error",
		platform:    "php",
		transaction: "POST /api/auth/register",
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{
				function: "runQueryCallback",
				module:   "Illuminate\\Database\\Connection",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php",
				lineno:   813,
			},
			{
				function: "run",
				module:   "Illuminate\\Database\\Connection",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php",
				lineno:   782,
			},
			{
				function: "statement",
				module:   "Illuminate\\Database\\Connection",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Connection.php",
				lineno:   408,
			},
			{
				function: "performInsert",
				module:   "Illuminate\\Database\\Eloquent\\Model",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Eloquent/Model.php",
				lineno:   1012,
			},
			{
				function: "save",
				module:   "Illuminate\\Database\\Eloquent\\Model",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Eloquent/Model.php",
				lineno:   889,
			},
			{
				function:    "create",
				module:      "App\\Services\\UserService",
				filename:    "app/Services/UserService.php",
				lineno:      44,
				preContext:  []string{"    public function create(array $data): User", "    {", "        $user = new User($data);"},
				contextLine: "        $user->save();",
				postContext: []string{"", "        event(new UserRegistered($user));", "        return $user;"},
			},
			{
				function:    "register",
				module:      "App\\Http\\Controllers\\Api\\AuthController",
				filename:    "app/Http/Controllers/Api/AuthController.php",
				lineno:      67,
				preContext:  []string{"    public function register(RegisterRequest $request): JsonResponse", "    {", "        $validated = $request->validated();"},
				contextLine: "        $user = $this->userService->create($validated);",
				postContext: []string{"", "        return response()->json(['user' => new UserResource($user)], 201);", "    }"},
			},
			{
				function: "dispatch",
				module:   "Illuminate\\Routing\\ControllerDispatcher",
				filename: "vendor/laravel/framework/src/Illuminate/Routing/ControllerDispatcher.php",
				lineno:   46,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "Illuminate\\Database\\Eloquent\\ModelNotFoundException",
		excValue:    "No query results for model [App\\Models\\Order] 84712",
		level:       "error",
		platform:    "php",
		transaction: "GET /api/orders/{id}",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function: "findOrFail",
				module:   "Illuminate\\Database\\Eloquent\\Builder",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Eloquent/Builder.php",
				lineno:   654,
			},
			{
				function: "findOrFail",
				module:   "Illuminate\\Database\\Eloquent\\Model",
				filename: "vendor/laravel/framework/src/Illuminate/Database/Eloquent/Model.php",
				lineno:   512,
			},
			{
				function:    "findById",
				module:      "App\\Repositories\\OrderRepository",
				filename:    "app/Repositories/OrderRepository.php",
				lineno:      38,
				preContext:  []string{"    public function findById(int $id): Order", "    {"},
				contextLine: "        return Order::with(['items.product', 'user', 'shipment'])->findOrFail($id);",
				postContext: []string{"    }", ""},
			},
			{
				function:    "get",
				module:      "App\\Services\\OrderService",
				filename:    "app/Services/OrderService.php",
				lineno:      22,
				preContext:  []string{"    public function get(int $id, User $actor): Order", "    {", "        $this->authorize('view', [$id, $actor]);"},
				contextLine: "        return $this->orderRepo->findById($id);",
				postContext: []string{"    }", ""},
			},
			{
				function:    "show",
				module:      "App\\Http\\Controllers\\Api\\OrderController",
				filename:    "app/Http/Controllers/Api/OrderController.php",
				lineno:      44,
				preContext:  []string{"    public function show(int $id): JsonResponse", "    {"},
				contextLine: "        $order = $this->orderService->get($id, $request->user());",
				postContext: []string{"        return new OrderResource($order);", "    }"},
			},
			{
				function: "dispatch",
				module:   "Illuminate\\Routing\\ControllerDispatcher",
				filename: "vendor/laravel/framework/src/Illuminate/Routing/ControllerDispatcher.php",
				lineno:   46,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "Illuminate\\Validation\\ValidationException",
		excValue:    "The given data was invalid. card_number field is required. expiry_month must be between 1 and 12. cvv must be exactly 3 digits.",
		level:       "warning",
		platform:    "php",
		transaction: "POST /api/checkout",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function: "throwValidationException",
				module:   "Illuminate\\Validation\\Validator",
				filename: "vendor/laravel/framework/src/Illuminate/Validation/Validator.php",
				lineno:   373,
			},
			{
				function: "validate",
				module:   "Illuminate\\Validation\\Validator",
				filename: "vendor/laravel/framework/src/Illuminate/Validation/Validator.php",
				lineno:   354,
			},
			{
				function: "validated",
				module:   "Illuminate\\Foundation\\Http\\FormRequest",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/FormRequest.php",
				lineno:   205,
			},
			{
				function:    "store",
				module:      "App\\Http\\Controllers\\Api\\CheckoutController",
				filename:    "app/Http/Controllers/Api/CheckoutController.php",
				lineno:      58,
				preContext:  []string{"    public function store(CheckoutRequest $request): JsonResponse", "    {", "        $data = $request->validated();"},
				contextLine: "        $order = $this->checkoutService->process($data, $request->user());",
				postContext: []string{"", "        return response()->json(['order_id' => $order->id], 201);", "    }"},
			},
			{
				function: "call",
				module:   "Illuminate\\Pipeline\\Pipeline",
				filename: "vendor/laravel/framework/src/Illuminate/Pipeline/Pipeline.php",
				lineno:   180,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "Illuminate\\Auth\\Access\\AuthorizationException",
		excValue:    "This action is unauthorized. Policy [App\\Policies\\ProjectPolicy@delete] denied access for user #482.",
		level:       "error",
		platform:    "php",
		transaction: "DELETE /api/projects/{id}",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function: "denyWithStatus",
				module:   "Illuminate\\Auth\\Access\\Gate",
				filename: "vendor/laravel/framework/src/Illuminate/Auth/Access/Gate.php",
				lineno:   638,
			},
			{
				function: "authorize",
				module:   "Illuminate\\Auth\\Access\\Gate",
				filename: "vendor/laravel/framework/src/Illuminate/Auth/Access/Gate.php",
				lineno:   450,
			},
			{
				function:    "delete",
				module:      "App\\Policies\\ProjectPolicy",
				filename:    "app/Policies/ProjectPolicy.php",
				lineno:      72,
				preContext:  []string{"    public function delete(User $user, Project $project): bool", "    {"},
				contextLine: "        return $user->hasRole('admin') || $user->id === $project->owner_id;",
				postContext: []string{"    }", ""},
			},
			{
				function: "handle",
				module:   "Illuminate\\Auth\\Middleware\\Authorize",
				filename: "vendor/laravel/framework/src/Illuminate/Auth/Middleware/Authorize.php",
				lineno:   44,
			},
			{
				function:    "destroy",
				module:      "App\\Http\\Controllers\\Api\\ProjectController",
				filename:    "app/Http/Controllers/Api/ProjectController.php",
				lineno:      112,
				preContext:  []string{"    public function destroy(Request $request, int $id): JsonResponse", "    {", "        $project = Project::findOrFail($id);"},
				contextLine: "        $this->authorize('delete', $project);",
				postContext: []string{"        $project->delete();", "        return response()->json(null, 204);", "    }"},
			},
			{
				function: "dispatch",
				module:   "Illuminate\\Routing\\ControllerDispatcher",
				filename: "vendor/laravel/framework/src/Illuminate/Routing/ControllerDispatcher.php",
				lineno:   46,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "App\\Exceptions\\PaymentException",
		excValue:    "Stripe charge failed: Your card was declined. Decline code: insufficient_funds. Payment intent: pi_3OqLmz2eZvKYlo2C0abc1234.",
		level:       "error",
		platform:    "php",
		transaction: "POST /api/subscriptions",
		handled:     boolPtr(false),
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function: "create",
				module:   "Stripe\\Exception\\CardException",
				filename: "vendor/stripe/stripe-php/lib/Exception/CardException.php",
				lineno:   18,
			},
			{
				function:    "charge",
				module:      "App\\Services\\Billing\\StripeGateway",
				filename:    "app/Services/Billing/StripeGateway.php",
				lineno:      88,
				preContext:  []string{"    public function charge(string $customerId, int $amountCents, string $currency): PaymentIntent", "    {", "        try {"},
				contextLine: "            return $this->stripe->paymentIntents->create([",
				postContext: []string{"                'amount' => $amountCents,", "                'currency' => $currency,", "                'customer' => $customerId,"},
			},
			{
				function:    "createSubscription",
				module:      "App\\Services\\SubscriptionService",
				filename:    "app/Services/SubscriptionService.php",
				lineno:      134,
				preContext:  []string{"    public function createSubscription(User $user, Plan $plan): Subscription", "    {", "        $intent = $this->billing->charge("},
				contextLine: "            $user->stripe_customer_id, $plan->price_cents, $plan->currency",
				postContext: []string{"        );", "        return Subscription::create([...]);", "    }"},
			},
			{
				function:    "store",
				module:      "App\\Http\\Controllers\\Api\\SubscriptionController",
				filename:    "app/Http/Controllers/Api/SubscriptionController.php",
				lineno:      51,
				preContext:  []string{"    public function store(SubscribeRequest $request): JsonResponse", "    {", "        $plan = Plan::findOrFail($request->plan_id);"},
				contextLine: "        $sub = $this->subscriptionService->createSubscription($request->user(), $plan);",
				postContext: []string{"        return response()->json(new SubscriptionResource($sub), 201);", "    }"},
			},
			{
				function: "handle",
				module:   "App\\Http\\Middleware\\EnsureVerifiedEmail",
				filename: "app/Http/Middleware/EnsureVerifiedEmail.php",
				lineno:   28,
			},
			{
				function: "call",
				module:   "Illuminate\\Pipeline\\Pipeline",
				filename: "vendor/laravel/framework/src/Illuminate/Pipeline/Pipeline.php",
				lineno:   180,
			},
			{
				function: "dispatch",
				module:   "Illuminate\\Routing\\Router",
				filename: "vendor/laravel/framework/src/Illuminate/Routing/Router.php",
				lineno:   398,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "Illuminate\\Queue\\MaxAttemptsExceededException",
		excValue:    "App\\Jobs\\SyncProductCatalog has been attempted too many times or run too long. The job may have previously timed out.",
		level:       "fatal",
		platform:    "php",
		transaction: "queue:work --queue=sync",
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{
				function: "connect",
				module:   "GuzzleHttp\\Client",
				filename: "vendor/guzzlehttp/guzzle/src/Client.php",
				lineno:   341,
			},
			{
				function: "send",
				module:   "GuzzleHttp\\Client",
				filename: "vendor/guzzlehttp/guzzle/src/Client.php",
				lineno:   192,
			},
			{
				function:    "syncProducts",
				module:      "App\\Services\\ExternalApiService",
				filename:    "app/Services/ExternalApiService.php",
				lineno:      63,
				preContext:  []string{"    public function syncProducts(array $productIds): array", "    {", "        $response = $this->client->post('/api/v2/products/sync', ["},
				contextLine: "            'json' => ['ids' => $productIds, 'include' => ['inventory', 'pricing']],",
				postContext: []string{"            'timeout' => 30,", "        ]);", "        return json_decode($response->getBody(), true);"},
			},
			{
				function:    "handle",
				module:      "App\\Jobs\\SyncProductCatalog",
				filename:    "app/Jobs/SyncProductCatalog.php",
				lineno:      58,
				preContext:  []string{"    public function handle(ExternalApiService $api, ProductRepository $repo): void", "    {", "        $chunks = array_chunk($this->productIds, 50);"},
				contextLine: "        foreach ($chunks as $chunk) {",
				postContext: []string{"            $data = $api->syncProducts($chunk);", "            $repo->bulkUpdate($data);", "        }"},
			},
			{
				function: "call",
				module:   "Illuminate\\Queue\\CallQueuedHandler",
				filename: "vendor/laravel/framework/src/Illuminate/Queue/CallQueuedHandler.php",
				lineno:   103,
			},
			{
				function: "process",
				module:   "Illuminate\\Queue\\Worker",
				filename: "vendor/laravel/framework/src/Illuminate/Queue/Worker.php",
				lineno:   228,
			},
			{
				function: "runJob",
				module:   "Illuminate\\Queue\\Worker",
				filename: "vendor/laravel/framework/src/Illuminate/Queue/Worker.php",
				lineno:   184,
			},
			{
				function: "daemon",
				module:   "Illuminate\\Queue\\Worker",
				filename: "vendor/laravel/framework/src/Illuminate/Queue/Worker.php",
				lineno:   112,
			},
		},
	},
	{
		excType:     "Error",
		excValue:    "Call to a member function load() on null - $subscription is null for user #317 (subscription may have been deleted mid-request)",
		level:       "error",
		platform:    "php",
		transaction: "GET /api/users/{id}/billing",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "getBillingDetails",
				module:      "App\\Services\\BillingService",
				filename:    "app/Services/BillingService.php",
				lineno:      44,
				preContext:  []string{"    public function getBillingDetails(User $user): array", "    {", "        $subscription = $user->subscription('default');"},
				contextLine: "        $subscription->load(['plan', 'invoices' => fn($q) => $q->latest()->limit(12)]);",
				postContext: []string{"        return [", "            'plan' => $subscription->plan->name,", "            'status' => $subscription->stripe_status,"},
			},
			{
				function:    "billing",
				module:      "App\\Http\\Controllers\\Api\\UserController",
				filename:    "app/Http/Controllers/Api/UserController.php",
				lineno:      98,
				preContext:  []string{"    public function billing(Request $request, int $id): JsonResponse", "    {", "        $user = User::findOrFail($id);"},
				contextLine: "        $details = $this->billingService->getBillingDetails($user);",
				postContext: []string{"        return response()->json($details);", "    }"},
			},
			{
				function: "handle",
				module:   "App\\Http\\Middleware\\Authenticate",
				filename: "vendor/laravel/framework/src/Illuminate/Auth/Middleware/Authenticate.php",
				lineno:   44,
			},
			{
				function: "call",
				module:   "Illuminate\\Pipeline\\Pipeline",
				filename: "vendor/laravel/framework/src/Illuminate/Pipeline/Pipeline.php",
				lineno:   180,
			},
			{
				function: "dispatch",
				module:   "Illuminate\\Routing\\Router",
				filename: "vendor/laravel/framework/src/Illuminate/Routing/Router.php",
				lineno:   398,
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
	{
		excType:     "Predis\\Connection\\ConnectionException",
		excValue:    "Error while reading line from the server. [tcp://redis-primary:6379]",
		level:       "error",
		platform:    "php",
		transaction: "GET /api/dashboard",
		stacktrace: []stackFrame{
			{
				function: "read",
				module:   "Predis\\Connection\\StreamConnection",
				filename: "vendor/predis/predis/src/Connection/StreamConnection.php",
				lineno:   194,
			},
			{
				function: "readResponse",
				module:   "Predis\\Connection\\StreamConnection",
				filename: "vendor/predis/predis/src/Connection/StreamConnection.php",
				lineno:   225,
			},
			{
				function: "get",
				module:   "Illuminate\\Cache\\RedisStore",
				filename: "vendor/laravel/framework/src/Illuminate/Cache/RedisStore.php",
				lineno:   62,
			},
			{
				function: "get",
				module:   "Illuminate\\Cache\\Repository",
				filename: "vendor/laravel/framework/src/Illuminate/Cache/Repository.php",
				lineno:   101,
			},
			{
				function:    "handle",
				module:      "App\\Http\\Middleware\\CacheResponse",
				filename:    "app/Http/Middleware/CacheResponse.php",
				lineno:      34,
				preContext:  []string{"    public function handle(Request $request, Closure $next, int $ttl = 60): Response", "    {", "        $key = $this->cacheKey($request);"},
				contextLine: "        if ($cached = Cache::get($key)) {",
				postContext: []string{"            return response($cached)->withHeaders(['X-Cache' => 'HIT']);", "        }", "        $response = $next($request);"},
			},
			{
				function: "call",
				module:   "Illuminate\\Pipeline\\Pipeline",
				filename: "vendor/laravel/framework/src/Illuminate/Pipeline/Pipeline.php",
				lineno:   180,
			},
			{
				function:    "index",
				module:      "App\\Http\\Controllers\\Api\\DashboardController",
				filename:    "app/Http/Controllers/Api/DashboardController.php",
				lineno:      28,
				preContext:  []string{"    public function index(Request $request): JsonResponse", "    {"},
				contextLine: "        return response()->json($this->dashboardService->getSummary($request->user()));",
				postContext: []string{"    }"},
			},
			{
				function: "handle",
				module:   "Illuminate\\Foundation\\Http\\Kernel",
				filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php",
				lineno:   147,
			},
		},
	},
}

var laravelTxs = []txTemplate{
	{
		name:       "GET /api/orders",
		op:         "http.server",
		durationMs: [2]int{22, 95},
		platform:   "php",
		spans: []spanNode{
			{op: "cache.get", description: "user_preferences:{user_id}", durationMs: [2]int{1, 5}},
			{op: "db.query", description: "SELECT `orders`.*, `users`.`name`, `users`.`email` FROM `orders` INNER JOIN `users` ON `users`.`id` = `orders`.`user_id` WHERE `orders`.`deleted_at` IS NULL ORDER BY `orders`.`created_at` DESC LIMIT 25 OFFSET 0", durationMs: [2]int{8, 40}},
			{op: "db.query", description: "SELECT `order_items`.*, `products`.`name`, `products`.`sku`, `products`.`image_url` FROM `order_items` INNER JOIN `products` ON `products`.`id` = `order_items`.`product_id` WHERE `order_items`.`order_id` IN (?, ?, ?, ?, ?)", durationMs: [2]int{6, 25}},
			{op: "db.query", description: "SELECT COUNT(*) AS aggregate FROM `orders` WHERE `user_id` = ? AND `deleted_at` IS NULL", durationMs: [2]int{3, 10}},
			{op: "cache.put", description: "orders_list:{user_id} ttl=60", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "POST /api/checkout",
		op:         "http.server",
		durationMs: [2]int{220, 780},
		platform:   "php",
		spans: []spanNode{
			{op: "cache.get", description: "cart:{session_id}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT `id`, `name`, `price`, `stock_qty`, `sku` FROM `products` WHERE `id` IN (?, ?, ?) FOR UPDATE", durationMs: [2]int{5, 20}},
			{op: "db.query", description: "SELECT COUNT(*) FROM `orders` WHERE `user_id` = ? AND `coupon_code` = ? AND `created_at` > ?", durationMs: [2]int{3, 10}},
			{op: "http.client", description: "POST https://api.stripe.com/v1/payment_intents", durationMs: [2]int{85, 360}},
			{op: "db.query", description: "INSERT INTO `orders` (`user_id`, `total`, `status`, `coupon_code`, `stripe_payment_intent_id`, `updated_at`, `created_at`) VALUES (?, ?, ?, ?, ?, ?, ?)", durationMs: [2]int{4, 12}},
			{op: "db.query", description: "INSERT INTO `order_items` (`order_id`, `product_id`, `qty`, `unit_price`, `updated_at`, `created_at`) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)", durationMs: [2]int{6, 20}},
			{op: "db.query", description: "UPDATE `products` SET `stock_qty` = `stock_qty` - ? WHERE `id` IN (?, ?, ?)", durationMs: [2]int{4, 15}},
			{op: "db.query", description: "UPDATE `coupons` SET `used_count` = `used_count` + 1, `last_used_at` = ? WHERE `code` = ?", durationMs: [2]int{2, 8}},
			{op: "queue.dispatch", description: "App\\Jobs\\SendOrderConfirmation", durationMs: [2]int{2, 6}},
			{op: "queue.dispatch", description: "App\\Jobs\\UpdateInventorySearch", durationMs: [2]int{2, 5}},
			{op: "cache.delete", description: "cart:{session_id}", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "GET /api/dashboard",
		op:         "http.server",
		durationMs: [2]int{65, 290},
		platform:   "php",
		spans: []spanNode{
			{op: "cache.get", description: "dashboard:{user_id}:{date} (miss)", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT COALESCE(SUM(`total`), 0) AS revenue FROM `orders` WHERE `created_at` BETWEEN ? AND ? AND `status` = 'completed'", durationMs: [2]int{8, 35}},
			{op: "db.query", description: "SELECT `status`, COUNT(*) AS cnt FROM `orders` WHERE `created_at` > ? AND `deleted_at` IS NULL GROUP BY `status`", durationMs: [2]int{6, 25}},
			{op: "db.query", description: "SELECT `products`.`name`, `products`.`sku`, SUM(`order_items`.`qty`) AS units_sold, SUM(`order_items`.`qty` * `order_items`.`unit_price`) AS revenue FROM `order_items` INNER JOIN `products` ON `products`.`id` = `order_items`.`product_id` GROUP BY `products`.`id` ORDER BY units_sold DESC LIMIT 5", durationMs: [2]int{12, 55}},
			{op: "db.query", description: "SELECT `id`, `name`, `email`, `created_at` FROM `users` WHERE `deleted_at` IS NULL ORDER BY `created_at` DESC LIMIT 10", durationMs: [2]int{4, 18}},
			{op: "db.query", description: "SELECT * FROM `activity_log` WHERE `causer_id` = ? AND `causer_type` = 'App\\\\Models\\\\User' ORDER BY `created_at` DESC LIMIT 20", durationMs: [2]int{5, 22}},
			{op: "cache.put", description: "dashboard:{user_id}:{date} ttl=300", durationMs: [2]int{1, 4}},
		},
	},
	{
		name:       "App\\Jobs\\SendOrderConfirmation",
		op:         "queue.process",
		durationMs: [2]int{190, 720},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `orders`.*, `users`.`name`, `users`.`email`, `users`.`locale` FROM `orders` INNER JOIN `users` ON `users`.`id` = `orders`.`user_id` WHERE `orders`.`id` = ? LIMIT 1", durationMs: [2]int{5, 18}},
			{op: "db.query", description: "SELECT `order_items`.`qty`, `order_items`.`unit_price`, `products`.`name`, `products`.`sku`, `products`.`image_url` FROM `order_items` INNER JOIN `products` ON `products`.`id` = `order_items`.`product_id` WHERE `order_items`.`order_id` = ?", durationMs: [2]int{6, 22}},
			{op: "db.query", description: "SELECT `addresses`.* FROM `addresses` WHERE `user_id` = ? AND `type` = 'shipping' LIMIT 1", durationMs: [2]int{3, 10}},
			{op: "view.render", description: "emails.order-confirmation (Blade compile + render)", durationMs: [2]int{15, 65}},
			{op: "mail.send", description: "Mailgun SMTP - order-confirmation to customer", durationMs: [2]int{80, 420}},
			{op: "db.query", description: "UPDATE `orders` SET `confirmation_sent_at` = ?, `updated_at` = ? WHERE `id` = ?", durationMs: [2]int{3, 10}},
			{op: "db.query", description: "INSERT INTO `notification_log` (`user_id`, `type`, `channel`, `reference_id`, `sent_at`) VALUES (?, ?, ?, ?, ?)", durationMs: [2]int{3, 8}},
		},
	},
	{
		name:       "POST /api/auth/login",
		op:         "http.server",
		durationMs: [2]int{18, 90},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `id`, `email`, `password`, `remember_token`, `email_verified_at`, `two_factor_secret` FROM `users` WHERE `email` = ? LIMIT 1", durationMs: [2]int{4, 18}},
			{op: "auth.verify", description: "bcrypt_verify - compare submitted password against stored hash", durationMs: [2]int{8, 45}},
			{op: "db.query", description: "UPDATE `users` SET `last_login_at` = ?, `last_login_ip` = ?, `updated_at` = ? WHERE `id` = ?", durationMs: [2]int{3, 10}},
			{op: "cache.put", description: "session:{session_id} ttl=7200", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "INSERT INTO `audit_log` (`user_id`, `event`, `ip_address`, `user_agent`, `created_at`) VALUES (?, 'login', ?, ?, ?)", durationMs: [2]int{3, 8}},
		},
	},
	{
		name:       "GET /api/products",
		op:         "http.server",
		durationMs: [2]int{28, 130},
		platform:   "php",
		spans: []spanNode{
			{op: "cache.get", description: "category_tree", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT `products`.*, `categories`.`name` AS category_name, `categories`.`slug` AS category_slug FROM `products` INNER JOIN `categories` ON `categories`.`id` = `products`.`category_id` WHERE `products`.`status` = 'active' AND `categories`.`slug` = ? ORDER BY `products`.`sort_order` ASC LIMIT 48 OFFSET ?", durationMs: [2]int{10, 45}},
			{op: "db.query", description: "SELECT `product_id`, `url` AS image_url, `alt_text` FROM `product_images` WHERE `product_id` IN (?, ?, ?, ?, ?) AND `is_primary` = 1", durationMs: [2]int{5, 20}},
			{op: "db.query", description: "SELECT `product_id`, COUNT(*) AS review_count, ROUND(AVG(`rating`), 1) AS avg_rating FROM `product_reviews` WHERE `product_id` IN (?, ?, ?, ?, ?) AND `approved` = 1 GROUP BY `product_id`", durationMs: [2]int{6, 25}},
			{op: "db.query", description: "SELECT `product_id`, `stock_qty`, `reserved_qty` FROM `inventory` WHERE `product_id` IN (?, ?, ?, ?, ?) AND `warehouse_id` = ?", durationMs: [2]int{4, 15}},
			{op: "cache.put", description: "products:{category}:{page}:{filters_hash} ttl=120", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "PUT /api/users/{id}",
		op:         "http.server",
		durationMs: [2]int{35, 220},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `id`, `name`, `email`, `avatar_url`, `bio`, `settings`, `timezone` FROM `users` WHERE `id` = ? AND `deleted_at` IS NULL LIMIT 1", durationMs: [2]int{3, 12}},
			{op: "http.client", description: "PUT https://storage.googleapis.com/mybucket/avatars/{hash}.jpg (avatar upload)", durationMs: [2]int{25, 160}},
			{op: "db.query", description: "UPDATE `users` SET `name` = ?, `bio` = ?, `avatar_url` = ?, `timezone` = ?, `settings` = ?, `updated_at` = ? WHERE `id` = ?", durationMs: [2]int{4, 15}},
			{op: "cache.delete", description: "user_profile:{id}", durationMs: [2]int{1, 3}},
			{op: "db.query", description: "INSERT INTO `audit_log` (`user_id`, `action`, `old_values`, `new_values`, `ip_address`, `user_agent`, `created_at`) VALUES (?, 'user.updated', ?, ?, ?, ?, ?)", durationMs: [2]int{3, 10}},
		},
	},
	{
		name:       "schedule:send-daily-digest",
		op:         "console.command",
		durationMs: [2]int{550, 3200},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `users`.`id`, `users`.`email`, `users`.`name`, `users`.`preferences` FROM `users` WHERE `users`.`digest_enabled` = 1 AND (`users`.`last_digest_sent_at` IS NULL OR `users`.`last_digest_sent_at` < ?) AND `users`.`deleted_at` IS NULL LIMIT 100", durationMs: [2]int{8, 35}},
			{op: "db.query", description: "SELECT `orders`.`id`, `orders`.`total`, `orders`.`status`, COUNT(`order_items`.`id`) AS item_count FROM `orders` INNER JOIN `order_items` ON `order_items`.`order_id` = `orders`.`id` WHERE `orders`.`user_id` IN (?) AND `orders`.`created_at` > ? GROUP BY `orders`.`id` ORDER BY `orders`.`created_at` DESC", durationMs: [2]int{10, 45}},
			{op: "db.query", description: "SELECT `products`.`id`, `products`.`name`, `products`.`price`, `products`.`image_url` FROM `products` WHERE `products`.`id` IN (SELECT `product_id` FROM `wishlist` WHERE `user_id` IN (?)) AND `products`.`stock_qty` > 0 AND `products`.`status` = 'active'", durationMs: [2]int{8, 30}},
			{op: "view.render", description: "emails.daily-digest (Blade batch render, 100 recipients)", durationMs: [2]int{80, 420}},
			{op: "mail.send", description: "Mailgun batch send - daily-digest (100 emails)", durationMs: [2]int{200, 1600}},
			{op: "db.query", description: "UPDATE `users` SET `last_digest_sent_at` = ?, `updated_at` = ? WHERE `id` IN (?)", durationMs: [2]int{5, 20}},
			{op: "db.query", description: "INSERT INTO `digest_log` (`batch_id`, `user_count`, `campaign`, `sent_at`) VALUES (?, ?, ?, ?)", durationMs: [2]int{3, 8}},
			{op: "cache.delete", description: "digest_batch:{date}", durationMs: [2]int{1, 3}},
		},
	},
	// N+1: loading role for each user in a paginated list
	{
		name:       "GET /admin/users",
		op:         "http.server",
		durationMs: [2]int{110, 340},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `id`, `name`, `email`, `created_at`, `role_id` FROM `users` WHERE `deleted_at` IS NULL ORDER BY `created_at` DESC LIMIT 20", durationMs: [2]int{8, 22}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
			{op: "db.query", description: "SELECT * FROM `roles` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 5}},
		},
	},
	// N+1: resolving tag names for each issue in a project listing
	{
		name:       "GET /api/issues",
		op:         "http.server",
		durationMs: [2]int{90, 260},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `id`, `title`, `status`, `level`, `last_seen` FROM `issues` WHERE `project_id` = ? ORDER BY `last_seen` DESC LIMIT 25", durationMs: [2]int{10, 30}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `key`, `value` FROM `issue_tags` WHERE `issue_id` = ?", durationMs: [2]int{2, 6}},
		},
	},
}

var laravelProject = projectDef{
	name:         "laravel",
	description:  "PHP/Laravel application (Eloquent, Queue, Horizon, Sanctum)",
	releases:     laravelReleases,
	environments: defaultEnvironments,
	issues:       laravelIssues,
	issueCounts:  []int{8, 5, 12, 6, 4, 2, 7, 3},
	txs:          laravelTxs,
	txCounts:     []int{22, 14, 20, 10, 28, 24, 12, 5, 18, 15},
}

// =============================================================================
// Go project
// =============================================================================

var goReleases = []string{"1.4.2", "1.4.3", "1.5.0", "1.5.1-rc.1"}

var goIssues = []issueTemplate{
	{
		excType:     "DatabaseError",
		excValue:    "pq: deadlock detected - two concurrent merge requests acquired locks in opposite order on issues and issue_events",
		level:       "error",
		platform:    "go",
		transaction: "POST /api/issues/{id}/merge",
		stacktrace: []stackFrame{
			{
				function:    "MergeIssues",
				module:      "storage",
				filename:    "internal/storage/issues.go",
				lineno:      311,
				preContext:  []string{"\t_, err = tx.Exec(ctx, updateSQL, targetID, sourceID)", "\tif err != nil {"},
				contextLine: "\t\treturn fmt.Errorf(\"merge issues: %w\", err)",
				postContext: []string{"\t}", "\t}"},
			},
			{
				function:    "handleMerge",
				module:      "api",
				filename:    "internal/api/issues.go",
				lineno:      88,
				preContext:  []string{"\tif err := s.store.MergeIssues(r.Context(), targetID, sourceID); err != nil {"},
				contextLine: "\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)",
				postContext: []string{"\t\treturn", "\t}"},
			},
			{"ServeHTTP", "net/http", "net/http/server.go", 2136, "", nil, nil},
		},
	},
	{
		excType:     "AuthenticationError",
		excValue:    "JWT signature verification failed: token has expired (exp claim: 2024-03-14T08:00:00Z, now: 2024-03-15T09:14:22Z)",
		level:       "warning",
		platform:    "go",
		transaction: "GET /api/me",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "verifyToken",
				module:      "auth",
				filename:    "internal/auth/jwt.go",
				lineno:      55,
				preContext:  []string{"\tparsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, keyFunc)"},
				contextLine: "\tif err != nil {",
				postContext: []string{"\t\treturn nil, fmt.Errorf(\"verify token: %w\", err)", "\t}"},
			},
			{"authMiddleware", "api", "internal/api/middleware.go", 30, "", nil, nil},
			{"ServeHTTP", "chi", "vendor/github.com/go-chi/chi/v5/mux.go", 87, "", nil, nil},
			{"ServeHTTP", "net/http", "net/http/server.go", 2136, "", nil, nil},
		},
	},
	{
		excType:     "TimeoutError",
		excValue:    "context deadline exceeded after 30000ms - pgxpool: no connections available in pool (size=20, in-use=20, idle=0)",
		level:       "error",
		platform:    "go",
		transaction: "GET /api/transactions",
		hasUser:     true,
		stacktrace: []stackFrame{
			{"(*Pool).Acquire", "pgxpool", "vendor/github.com/jackc/pgx/v5/pgxpool/pool.go", 294, "", nil, nil},
			{
				function:    "ListTransactions",
				module:      "storage",
				filename:    "internal/storage/transactions.go",
				lineno:      48,
				preContext:  []string{"\tconn, err := s.pool.Acquire(ctx)"},
				contextLine: "\tif err != nil {",
				postContext: []string{"\t\treturn nil, fmt.Errorf(\"acquire conn: %w\", err)", "\t}"},
			},
			{"handleList", "api", "internal/api/transactions.go", 33, "", nil, nil},
			{"ServeHTTP", "net/http", "net/http/server.go", 2136, "", nil, nil},
		},
	},
	{
		excType:     "panic",
		excValue:    "runtime error: index out of range [5] with length 3 - evaluating alert threshold conditions",
		level:       "fatal",
		platform:    "go",
		transaction: "POST /api/alerts",
		fingerprint: []string{"go-panic", "index-out-of-range", "alerts-engine"},
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{
				function:    "evaluateConditions",
				module:      "alerts",
				filename:    "internal/alerts/engine.go",
				lineno:      142,
				preContext:  []string{"\tfor i, cond := range rule.Conditions {", "\t\tval := metrics[i]"},
				contextLine: "\t\tif thresholds[i] > val {",
				postContext: []string{"\t\t\treturn false", "\t\t}"},
			},
			{"(*Engine).evaluate", "alerts", "internal/alerts/engine.go", 89, "", nil, nil},
			{"(*Engine).Run", "alerts", "internal/alerts/engine.go", 54, "", nil, nil},
			{"(*Server).Start.func3", "cmd", "cmd/tindra/main.go", 88, "", nil, nil},
		},
	},
	{
		excType:     "pgx: ERROR",
		excValue:    `pgx: ERROR: value too long for type character varying(255) - "release" field received 312-byte string from SDK (truncation would lose version metadata)`,
		level:       "error",
		platform:    "go",
		transaction: "POST /api/{publicKey}/envelope/",
		stacktrace: []stackFrame{
			{"(*Conn).exec", "pgx", "vendor/github.com/jackc/pgx/v5/conn.go", 622, "", nil, nil},
			{
				function:    "batchWrite",
				module:      "ingest",
				filename:    "internal/ingest/writer.go",
				lineno:      88,
				preContext:  []string{"\tb := &pgx.Batch{}", "\tfor _, ev := range batch {", "\t\tb.Queue(insertSQL, ev.ID, ev.ProjectID, ev.Release, ev.Environment)"},
				contextLine: "\t\t// ev.Release is not validated/truncated before insert",
				postContext: []string{"\t}", "\tresults := s.pool.SendBatch(ctx, b)"},
			},
			{"(*BatchWriter).flush", "ingest", "internal/ingest/writer.go", 55, "", nil, nil},
			{"(*BatchWriter).Run", "ingest", "internal/ingest/writer.go", 30, "", nil, nil},
		},
	},
	{
		excType:     "strconv.NumError",
		excValue:    `strconv.ParseInt: parsing "abc": invalid syntax - "cursor" query parameter must be a valid int64`,
		level:       "error",
		platform:    "go",
		transaction: "GET /api/issues",
		stacktrace: []stackFrame{
			{
				function:    "parseCursor",
				module:      "api",
				filename:    "internal/api/pagination.go",
				lineno:      28,
				preContext:  []string{`\traw := r.URL.Query().Get("cursor")`},
				contextLine: `\tn, err := strconv.ParseInt(raw, 10, 64)`,
				postContext: []string{`\tif err != nil {`, `\t\treturn 0, fmt.Errorf("invalid cursor: %w", err)`, `\t}`},
			},
			{"handleListIssues", "api", "internal/api/issues.go", 42, "", nil, nil},
			{"ServeHTTP", "chi", "vendor/github.com/go-chi/chi/v5/mux.go", 87, "", nil, nil},
			{"ServeHTTP", "net/http", "net/http/server.go", 2136, "", nil, nil},
		},
	},
}

var goTxs = []txTemplate{
	{
		name:       "GET /api/issues",
		op:         "http.server",
		durationMs: [2]int{18, 95},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, title, status, level, environment, last_seen, times_seen, fingerprint FROM issues WHERE project_id = $1 AND ($2::text IS NULL OR status = $2) ORDER BY last_seen DESC LIMIT 50", durationMs: [2]int{8, 45}},
			{op: "db.query", description: "SELECT COUNT(*) FROM issues WHERE project_id = $1 AND status = 'open'", durationMs: [2]int{3, 12}},
		},
	},
	{
		name:       "GET /api/issues/:id",
		op:         "http.server",
		durationMs: [2]int{22, 110},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT i.*, p.name AS project_name FROM issues i JOIN projects p ON p.id = i.project_id WHERE i.id = $1", durationMs: [2]int{5, 18}},
			{op: "db.query", description: "SELECT e.id, e.timestamp, e.level, e.environment, e.release, e.exception, e.breadcrumbs, e.tags, e.contexts, e.user FROM events e WHERE e.issue_id = $1 ORDER BY e.timestamp DESC LIMIT 10", durationMs: [2]int{10, 55}},
			{op: "db.query", description: "SELECT c.id, c.body, c.created_at, u.name AS author_name FROM comments c JOIN users u ON u.id = c.user_id WHERE c.issue_id = $1 ORDER BY c.created_at ASC", durationMs: [2]int{4, 14}},
		},
	},
	{
		name:       "POST /api/issues/:id/resolve",
		op:         "http.server",
		durationMs: [2]int{12, 40},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "UPDATE issues SET status = 'resolved', resolved_at = $2, resolved_by = $3 WHERE id = $1 AND project_id = $4", durationMs: [2]int{5, 20}},
			{op: "db.query", description: "INSERT INTO issue_activity (issue_id, actor_id, action, created_at) VALUES ($1, $2, 'resolved', NOW())", durationMs: [2]int{3, 10}},
		},
	},
	{
		name:       "GET /api/transactions",
		op:         "http.server",
		durationMs: [2]int{25, 130},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, name, op, duration_ms, status, started_at, environment, release FROM transactions WHERE project_id = $1 AND ($2::text IS NULL OR name = $2) ORDER BY started_at DESC LIMIT 50", durationMs: [2]int{12, 70}},
			{op: "db.query", description: "SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms) AS p50, percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms) AS p75, percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95, percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) AS p99 FROM transactions WHERE project_id = $1 AND name = $2 AND started_at > NOW() - INTERVAL '24 hours'", durationMs: [2]int{6, 22}},
		},
	},
	{
		name:       "POST /api/:projectId/envelope/",
		op:         "http.server",
		durationMs: [2]int{5, 30},
		platform:   "go",
		spans: []spanNode{
			{op: "ingest.parse", description: "parse sentry envelope - extract header + item type", durationMs: [2]int{1, 5}},
			{op: "ingest.validate", description: "validate event fields and project public key", durationMs: [2]int{1, 4}},
			{op: "ingest.buffer", description: "write event to ring buffer (cap=10000)", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "GET /api/releases",
		op:         "http.server",
		durationMs: [2]int{15, 60},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT r.version, r.created_at, COUNT(DISTINCT e.id) AS event_count, COUNT(DISTINCT i.id) AS issue_count FROM releases r LEFT JOIN events e ON e.release = r.version AND e.project_id = r.project_id LEFT JOIN issues i ON i.first_seen_release = r.version WHERE r.project_id = $1 GROUP BY r.id ORDER BY r.created_at DESC LIMIT 25", durationMs: [2]int{10, 45}},
		},
	},
	{
		name:       "POST /api/projects",
		op:         "http.server",
		durationMs: [2]int{30, 80},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "INSERT INTO projects (id, name, slug, public_key, created_by, created_at) VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING id", durationMs: [2]int{8, 30}},
			{op: "db.query", description: "INSERT INTO audit_log (actor_id, action, resource_type, resource_id, metadata, created_at) VALUES ($1, 'project.created', 'project', $2, $3, NOW())", durationMs: [2]int{5, 15}},
		},
	},
	{
		name:       "sourcemap.upload",
		op:         "task",
		durationMs: [2]int{300, 1800},
		platform:   "go",
		spans: []spanNode{
			{op: "file.read", description: "read sourcemap bundle from multipart body (2.4 MB)", durationMs: [2]int{40, 120}},
			{op: "sourcemap.parse", description: "json.Unmarshal source map (sources + mappings)", durationMs: [2]int{80, 400}},
			{op: "sourcemap.validate", description: "validate sources/sourcesContent arrays match", durationMs: [2]int{10, 40}},
			{op: "file.write", description: "write to DATA_DIR/sourcemaps/{project}/{release}/{hash}.map", durationMs: [2]int{30, 100}},
			{op: "db.query", description: "INSERT INTO sourcemaps (id, project_id, release, filename, content_hash, size_bytes, path, uploaded_at) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW()) ON CONFLICT (project_id, release, filename) DO UPDATE SET content_hash = EXCLUDED.content_hash, path = EXCLUDED.path, uploaded_at = EXCLUDED.uploaded_at", durationMs: [2]int{5, 18}},
		},
	},
	{
		name:       "alert.evaluate",
		op:         "task",
		durationMs: [2]int{50, 220},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, name, conditions, threshold, window_seconds, channel FROM alert_rules WHERE project_id = $1 AND enabled = true", durationMs: [2]int{5, 18}},
			{op: "db.query", description: "SELECT COUNT(*) FROM issues WHERE project_id = $1 AND status = 'open' AND last_seen > NOW() - make_interval(secs => $2)", durationMs: [2]int{6, 22}},
			{op: "alert.check", description: "evaluate threshold conditions against current metrics", durationMs: [2]int{2, 10}},
			{op: "http.client", description: "POST https://hooks.slack.com/services/... (alert webhook)", durationMs: [2]int{40, 140}},
			{op: "db.query", description: "INSERT INTO alert_history (rule_id, triggered_at, metric_value, threshold) VALUES ($1, NOW(), $2, $3)", durationMs: [2]int{3, 10}},
		},
	},
	{
		name:       "GET /api/settings/tokens",
		op:         "http.server",
		durationMs: [2]int{10, 35},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, name, prefix, scopes, created_at, last_used_at, expires_at FROM api_tokens WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC", durationMs: [2]int{6, 22}},
		},
	},
	{
		name:       "GET /api/events",
		op:         "http.server",
		durationMs: [2]int{30, 150},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT e.id, e.timestamp, e.level, e.environment, e.release, e.transaction, e.platform, e.tags FROM events e WHERE e.issue_id = $1 AND ($2::timestamptz IS NULL OR e.timestamp < $2) ORDER BY e.timestamp DESC LIMIT 50", durationMs: [2]int{10, 60}},
			{op: "db.query", description: "SELECT e.exception, e.breadcrumbs, e.contexts, e.user, e.modules FROM events e WHERE e.id = $1", durationMs: [2]int{5, 25}},
			{op: "db.query", description: "SELECT COUNT(*) FROM events WHERE issue_id = $1", durationMs: [2]int{3, 12}},
		},
	},
	{
		name:       "ingest.batch_write",
		op:         "task",
		durationMs: [2]int{8, 55},
		platform:   "go",
		spans: []spanNode{
			{op: "ingest.dequeue", description: "drain ring buffer - batch up to 1000 events or 200ms window", durationMs: [2]int{1, 8}},
			{op: "db.query", description: "INSERT INTO events (id, project_id, issue_id, timestamp, level, ...) VALUES ... ON CONFLICT (id) DO NOTHING - batch of 47 events", durationMs: [2]int{5, 35}},
			{op: "db.query", description: "UPDATE issues SET times_seen = times_seen + $1, last_seen = $2 WHERE id = ANY($3)", durationMs: [2]int{3, 15}},
		},
	},
	{
		name:       "POST /api/issues/bulk-resolve",
		op:         "http.server",
		durationMs: [2]int{40, 200},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id FROM issues WHERE id = ANY($1) AND project_id = $2", durationMs: [2]int{5, 20}},
			{op: "db.query", description: "UPDATE issues SET status = 'resolved', resolved_at = NOW(), resolved_by = $1 WHERE id = ANY($2)", durationMs: [2]int{8, 40}},
			{op: "db.query", description: "INSERT INTO issue_activity (issue_id, actor_id, action, created_at) SELECT unnest($1), $2, 'bulk_resolved', NOW()", durationMs: [2]int{5, 20}},
		},
	},
	{
		name:       "retention.worker",
		op:         "task",
		durationMs: [2]int{800, 8000},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT project_id, retention_days FROM projects WHERE retention_days IS NOT NULL", durationMs: [2]int{3, 12}},
			{op: "db.query", description: "DELETE FROM events WHERE project_id = $1 AND timestamp < NOW() - make_interval(days => $2) RETURNING id", durationMs: [2]int{200, 4000}},
			{op: "db.query", description: "DELETE FROM issues WHERE project_id = $1 AND last_seen < NOW() - make_interval(days => $2) AND id NOT IN (SELECT DISTINCT issue_id FROM events WHERE project_id = $1)", durationMs: [2]int{100, 2000}},
			{op: "db.query", description: "UPDATE projects SET last_retention_run = NOW() WHERE id = $1", durationMs: [2]int{3, 10}},
		},
	},
	// N+1: resolving project name for each event in an activity feed
	{
		name:       "GET /api/activity",
		op:         "http.server",
		durationMs: [2]int{80, 220},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, project_id, issue_id, timestamp, level FROM events ORDER BY received_at DESC LIMIT 20", durationMs: [2]int{8, 25}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT id, name, slug FROM projects WHERE id = $1", durationMs: [2]int{2, 6}},
		},
	},
}

var goProject = projectDef{
	name:         "go",
	description:  "Go HTTP API (chi, pgx, stdlib - mirrors Tindra itself)",
	releases:     goReleases,
	environments: defaultEnvironments,
	issues:       goIssues,
	issueCounts:  []int{8, 6, 3, 1, 4, 9},
	txs:          goTxs,
	txCounts:     []int{20, 15, 10, 18, 30, 8, 9, 3, 5, 9, 15, 30, 5, 3, 12},
}

// =============================================================================
// Python project
// =============================================================================

var pythonReleases = []string{"0.9.0", "0.9.1", "1.0.0", "1.0.1"}

var pythonIssues = []issueTemplate{
	{
		excType:     "ValueError",
		excValue:    "invalid literal for int() with base 10: 'undefined' - project_id field received non-numeric string from webhook payload",
		level:       "error",
		platform:    "python",
		transaction: "/ingest/webhook",
		stacktrace: []stackFrame{
			{
				function:    "parse_payload",
				module:      "ingest.parser",
				filename:    "ingest/parser.py",
				lineno:      77,
				preContext:  []string{"def parse_payload(raw: dict) -> Payload:", "    event_id = raw.get('event_id')"},
				contextLine: "    project_id = int(raw.get('project_id'))",
				postContext: []string{"    return Payload(event_id=event_id, project_id=project_id)", ""},
			},
			{
				function:    "handle_webhook",
				module:      "api.views",
				filename:    "api/views.py",
				lineno:      203,
				preContext:  []string{"@csrf_exempt", "def handle_webhook(request):"},
				contextLine: "    payload = parse_payload(json.loads(request.body))",
				postContext: []string{"    process_event.delay(payload)", "    return JsonResponse({'ok': True})"},
			},
			{"dispatch", "django.core.handlers.base", "django/core/handlers/base.py", 115, "", nil, nil},
			{"__call__", "django.core.handlers.wsgi", "django/core/handlers/wsgi.py", 140, "", nil, nil},
		},
	},
	{
		excType:     "RateLimitError",
		excValue:    "upstream API returned 429: rate limit exceeded for /v1/chat/completions (model=gpt-4o, org=org-acme, limit=500 RPM)",
		level:       "error",
		platform:    "python",
		transaction: "/generate",
		hasUser:     true,
		stacktrace: []stackFrame{
			{"_handle_response", "openai._base_client", "openai/_base_client.py", 455, "", nil, nil},
			{"create", "openai.resources.chat.completions", "openai/resources/chat/completions.py", 88, "", nil, nil},
			{
				function:    "generate_summary",
				module:      "app.ai",
				filename:    "app/ai.py",
				lineno:      31,
				preContext:  []string{"def generate_summary(text: str, model: str = 'gpt-4o') -> str:", "    client = openai.OpenAI()"},
				contextLine: "    response = client.chat.completions.create(model=model, messages=[{\"role\": \"user\", \"content\": text}])",
				postContext: []string{"    return response.choices[0].message.content", ""},
			},
			{
				function:    "summarize",
				module:      "api.views",
				filename:    "api/views.py",
				lineno:      88,
				preContext:  []string{"@require_POST", "@login_required"},
				contextLine: "    summary = generate_summary(request.POST['text'])",
				postContext: []string{"    return JsonResponse({'summary': summary})"},
			},
			{"dispatch", "django.views.generic.base", "django/views/generic/base.py", 93, "", nil, nil},
		},
	},
	{
		excType:     "ValidationError",
		excValue:    "email: this field is required; password: ensure this value has at least 8 characters (it has 5); username: a user with that username already exists",
		level:       "warning",
		platform:    "python",
		transaction: "POST /api/auth/register",
		hasUser:     true,
		stacktrace: []stackFrame{
			{"run_validators", "django.db.models.fields", "django/db/models/fields/__init__.py", 1421, "", nil, nil},
			{"full_clean", "django.db.models.base", "django/db/models/base.py", 1301, "", nil, nil},
			{
				function:    "register",
				module:      "api.views",
				filename:    "api/views.py",
				lineno:      58,
				preContext:  []string{"@api_view(['POST'])", "def register(request):"},
				contextLine: "    serializer = UserSerializer(data=request.data)",
				postContext: []string{"    if serializer.is_valid():", "        serializer.save()"},
			},
			{"initial", "rest_framework.views", "rest_framework/views.py", 395, "", nil, nil},
			{"dispatch", "rest_framework.views", "rest_framework/views.py", 507, "", nil, nil},
		},
	},
	{
		excType:     "pydantic_core._pydantic_core.ValidationError",
		excValue:    "3 validation errors for CreateItemRequest\nbody.price\n  Input should be greater than 0 [type=greater_than]\nbody.sku\n  String should match pattern '^[A-Z0-9]{6,12}$' [type=string_pattern_mismatch]\nbody.category_id\n  Input should be a valid UUID [type=uuid_parsing]",
		level:       "warning",
		platform:    "python",
		transaction: "POST /api/v1/items",
		hasUser:     true,
		stacktrace: []stackFrame{
			{"validate_python", "pydantic_core._pydantic_core", "pydantic_core/_pydantic_core.pyi", 12, "", nil, nil},
			{
				function:    "create_item",
				module:      "app.routers.items",
				filename:    "app/routers/items.py",
				lineno:      44,
				preContext:  []string{"@router.post('/items', response_model=ItemResponse, status_code=201)", "async def create_item("},
				contextLine: "    body: CreateItemRequest,",
				postContext: []string{"    db: AsyncSession = Depends(get_db),", "    current_user: User = Depends(get_current_user),", ") -> ItemResponse:"},
			},
			{"run_endpoint_function", "fastapi.routing", "fastapi/routing.py", 229, "", nil, nil},
			{"run_asgi_app", "starlette.routing", "starlette/routing.py", 72, "", nil, nil},
			{"__call__", "starlette.applications", "starlette/applications.py", 122, "", nil, nil},
		},
	},
	{
		excType:     "sqlalchemy.exc.OperationalError",
		excValue:    "(psycopg2.OperationalError) FATAL: remaining connection slots are reserved for non-replication superuser connections - pool_size=20, overflow=10, checked_out=30",
		level:       "error",
		platform:    "python",
		transaction: "GET /api/v1/reports/{id}",
		stacktrace: []stackFrame{
			{"_do_get", "sqlalchemy.pool.impl", "sqlalchemy/pool/impl.py", 308, "", nil, nil},
			{"_checkout", "sqlalchemy.pool.base", "sqlalchemy/pool/base.py", 894, "", nil, nil},
			{"connect", "sqlalchemy.engine.base", "sqlalchemy/engine/base.py", 3277, "", nil, nil},
			{
				function:    "get_report",
				module:      "app.services.reports",
				filename:    "app/services/reports.py",
				lineno:      88,
				preContext:  []string{"async def get_report(report_id: UUID, db: AsyncSession) -> Report:"},
				contextLine: "    result = await db.execute(select(Report).where(Report.id == report_id).options(selectinload(Report.rows)))",
				postContext: []string{"    return result.scalar_one_or_none()"},
			},
			{
				function:    "read_report",
				module:      "app.routers.reports",
				filename:    "app/routers/reports.py",
				lineno:      62,
				preContext:  []string{"@router.get('/reports/{report_id}')"},
				contextLine: "    report = await get_report(report_id, db)",
				postContext: []string{"    if report is None:", "        raise HTTPException(status_code=404, detail='Report not found')"},
			},
			{"run_asgi_app", "starlette.routing", "starlette/routing.py", 72, "", nil, nil},
		},
	},
	{
		excType:     "celery.exceptions.MaxRetriesExceededError",
		excValue:    "Can't retry app.tasks.send_invoice_pdf[c8f3b2d1-4e5a-4b6c-9d8e-1f2a3b4c5d6e] args:('inv_20240315_84712',) - max retries (5) exceeded",
		level:       "fatal",
		platform:    "python",
		transaction: "celery:app.tasks.send_invoice_pdf",
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{"retry", "celery.app.task", "celery/app/task.py", 702, "", nil, nil},
			{
				function:    "send_invoice_pdf",
				module:      "app.tasks",
				filename:    "app/tasks.py",
				lineno:      118,
				preContext:  []string{"@shared_task(bind=True, max_retries=5, default_retry_delay=60)", "def send_invoice_pdf(self, invoice_id: str) -> None:"},
				contextLine: "        raise self.retry(exc=exc, countdown=2 ** self.request.retries * 30)",
				postContext: []string{""},
			},
			{"__protected_call__", "celery.app.trace", "celery/app/trace.py", 439, "", nil, nil},
			{"_fast_trace_task", "celery.app.trace", "celery/app/trace.py", 474, "", nil, nil},
			{"execute", "celery.worker.strategy", "celery/worker/strategy.py", 202, "", nil, nil},
		},
	},
}

var pythonTxs = []txTemplate{
	{
		name:       "GET /api/users",
		op:         "http.server",
		durationMs: [2]int{20, 90},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT \"users\".\"id\", \"users\".\"email\", \"users\".\"name\", \"users\".\"created_at\", \"users\".\"last_login\" FROM \"users\" WHERE \"users\".\"is_active\" = True ORDER BY \"users\".\"created_at\" DESC LIMIT 25 OFFSET 0", durationMs: [2]int{6, 30}},
			{op: "cache.get", description: "user_list:page=1:filter=active", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT COUNT(*) AS \"__count\" FROM \"users\" WHERE \"users\".\"is_active\" = True", durationMs: [2]int{3, 12}},
		},
	},
	{
		name:       "POST /api/v1/items",
		op:         "http.server",
		durationMs: [2]int{25, 110},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT categories.id, categories.name FROM categories WHERE categories.id = $1 FOR SHARE", durationMs: [2]int{4, 15}},
			{op: "db.query", description: "INSERT INTO items (id, name, sku, price, category_id, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW()) RETURNING id, created_at", durationMs: [2]int{5, 20}},
			{op: "cache.delete", description: "items:category:{category_id}:*", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "INSERT INTO audit_events (entity_type, entity_id, action, actor_id, payload, created_at) VALUES ('item', $1, 'created', $2, $3, NOW())", durationMs: [2]int{3, 10}},
		},
	},
	{
		name:       "celery: app.tasks.generate_report",
		op:         "queue.task",
		durationMs: [2]int{800, 5000},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT reports.id, reports.config, reports.date_range_start, reports.date_range_end FROM reports WHERE reports.id = %s", durationMs: [2]int{4, 15}},
			{op: "db.query", description: "SELECT events.*, users.email FROM events JOIN users ON users.id = events.user_id WHERE events.created_at BETWEEN %s AND %s AND events.project_id = %s ORDER BY events.created_at", durationMs: [2]int{200, 2000}},
			{op: "compute", description: "aggregate metrics - group by day, compute p50/p95/p99 latencies", durationMs: [2]int{100, 800}},
			{op: "file.write", description: "render report to PDF (WeasyPrint)", durationMs: [2]int{300, 1500}},
			{op: "http.client", description: "PUT https://s3.amazonaws.com/{bucket}/reports/{report_id}.pdf", durationMs: [2]int{80, 400}},
			{op: "db.query", description: "UPDATE reports SET status = 'complete', file_url = %s, completed_at = NOW() WHERE id = %s", durationMs: [2]int{3, 12}},
			{op: "cache.delete", description: "report:{report_id}", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "POST /ingest/webhook",
		op:         "http.server",
		durationMs: [2]int{8, 40},
		platform:   "python",
		spans: []spanNode{
			{op: "http.verify", description: "verify HMAC-SHA256 webhook signature", durationMs: [2]int{1, 3}},
			{op: "cache.get", description: "idempotency:{webhook_id}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "INSERT INTO webhook_events (id, source, event_type, payload, received_at) VALUES (%s, %s, %s, %s, NOW()) ON CONFLICT (id) DO NOTHING", durationMs: [2]int{3, 12}},
			{op: "queue.dispatch", description: "app.tasks.process_webhook_event", durationMs: [2]int{2, 6}},
			{op: "cache.put", description: "idempotency:{webhook_id} ttl=86400", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "GET /api/v1/search",
		op:         "http.server",
		durationMs: [2]int{30, 180},
		platform:   "python",
		spans: []spanNode{
			{op: "cache.get", description: "search:{query_hash}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT id, name, description, ts_rank(search_vector, query) AS rank FROM items, to_tsquery('english', $1) query WHERE search_vector @@ query ORDER BY rank DESC LIMIT 20", durationMs: [2]int{15, 80}},
			{op: "db.query", description: "SELECT item_id, url FROM item_images WHERE item_id = ANY($1) AND is_primary = true", durationMs: [2]int{5, 20}},
			{op: "cache.put", description: "search:{query_hash} ttl=30", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "celery: app.tasks.process_webhook_event",
		op:         "queue.task",
		durationMs: [2]int{50, 400},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, source, event_type, payload FROM webhook_events WHERE id = %s AND processed_at IS NULL", durationMs: [2]int{3, 12}},
			{op: "http.client", description: "POST https://api.internal/events/ingest (forward to internal pipeline)", durationMs: [2]int{15, 120}},
			{op: "db.query", description: "UPDATE webhook_events SET processed_at = NOW(), status = 'ok' WHERE id = %s", durationMs: [2]int{3, 10}},
		},
	},
}

var pythonProject = projectDef{
	name:         "python",
	description:  "Python application (Django DRF + FastAPI + Celery)",
	releases:     pythonReleases,
	environments: defaultEnvironments,
	issues:       pythonIssues,
	issueCounts:  []int{15, 9, 11, 8, 3, 2},
	txs:          pythonTxs,
	txCounts:     []int{18, 12, 6, 22, 10, 15},
}

// =============================================================================
// JavaScript project
// =============================================================================

var jsReleases = []string{"1.4.2", "1.4.3", "1.5.0", "1.5.1-rc.1"}

var jsIssues = []issueTemplate{
	{
		excType:     "TypeError",
		excValue:    "Cannot read properties of null (reading 'userId') - session expired mid-render before auth guard could redirect",
		level:       "error",
		platform:    "javascript",
		transaction: "/dashboard",
		handled:     boolPtr(false),
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "resolveUser",
				module:      "auth/session",
				filename:    "src/auth/session.ts",
				lineno:      42,
				preContext:  []string{"export function resolveUser(session: Session | null) {", "  if (!session) return null"},
				contextLine: "  return session.user.userId",
				postContext: []string{"}", ""},
			},
			{
				function:    "DashboardView.setup",
				module:      "views/dashboard",
				filename:    "src/views/DashboardView.vue",
				lineno:      18,
				preContext:  []string{"const route = useRoute()", ""},
				contextLine: "const user = resolveUser(useSession())",
				postContext: []string{"const issues = useIssues({ userId: user.userId })", ""},
			},
			{"mount", "runtime-core", "node_modules/@vue/runtime-core/dist/runtime-core.esm-bundler.js", 1234, "", nil, nil},
		},
	},
	{
		excType:     "TypeError",
		excValue:    "Cannot destructure property 'data' of 'undefined' - TanStack Query returned undefined before suspense boundary caught it",
		level:       "error",
		platform:    "javascript",
		transaction: "/issues/:id",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "useIssueDetail",
				module:      "composables/useIssueDetail",
				filename:    "src/composables/useIssueDetail.ts",
				lineno:      24,
				preContext:  []string{"export function useIssueDetail(id: string) {", "  const res = useQuery({ queryKey: ['issue', id], queryFn: () => fetchIssue(id) })"},
				contextLine: "  const { data: issue } = res",
				postContext: []string{"  return { issue, isLoading: res.isLoading }", "}"},
			},
			{"IssueDetailView.setup", "views/IssueDetail", "src/views/IssueDetailView.vue", 11, "", nil, nil},
			{"mount", "runtime-core", "node_modules/@vue/runtime-core/dist/runtime-core.esm-bundler.js", 1234, "", nil, nil},
		},
	},
	{
		excType:     "ECONNREFUSED",
		excValue:    "connect ECONNREFUSED 127.0.0.1:6379 - Redis appears unreachable; background queue worker cannot process jobs",
		level:       "error",
		platform:    "javascript",
		transaction: "worker.processQueue",
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{"createConnectionError", "ioredis/built/utils", "node_modules/ioredis/built/utils/index.js", 141, "", nil, nil},
			{"Socket.<anonymous>", "ioredis/built/connectors", "node_modules/ioredis/built/connectors/StandaloneConnector.js", 44, "", nil, nil},
			{
				function:    "connectToRedis",
				module:      "lib/queue",
				filename:    "src/lib/queue.ts",
				lineno:      18,
				preContext:  []string{"const redis = new IORedis(process.env.REDIS_URL)"},
				contextLine: "redis.on('error', (err) => { throw err })",
				postContext: []string{"export const queue = new Queue('jobs', { connection: redis })", ""},
			},
		},
	},
	{
		excType:     "ChunkLoadError",
		excValue:    "Loading chunk 42 failed. (missing: https://app.example.com/assets/IssueDetail-DHk3js9.js) - CDN cache may have stale references after deploy",
		level:       "error",
		platform:    "javascript",
		transaction: "/issues/:id",
		fingerprint: []string{"chunk-load-error", "IssueDetail"},
		handled:     boolPtr(false),
		stacktrace: []stackFrame{
			{"__webpack_require__.f.j", "webpack/runtime", "webpack/runtime/ensure chunk.js", 22, "", nil, nil},
			{"Promise.then.then", "runtime-core", "node_modules/@vue/runtime-core/dist/runtime-core.esm-bundler.js", 5511, "", nil, nil},
			{
				function:    "loadIssueDetail",
				module:      "router/index",
				filename:    "src/router/index.ts",
				lineno:      44,
				preContext:  []string{"const routes = [", "  {", "    path: '/issues/:id',"},
				contextLine: "    component: () => import('../views/IssueDetailView.vue'),",
				postContext: []string{"    meta: { requiresAuth: true },", "  },", "]"},
			},
		},
	},
}

var jsTxs = []txTemplate{
	{
		name:       "dashboard load",
		op:         "pageload",
		durationMs: [2]int{180, 950},
		platform:   "javascript",
		spans: []spanNode{
			{op: "browser.cache", description: "service worker cache lookup", durationMs: [2]int{2, 8}},
			{op: "http.client", description: "GET /api/issues?status=open&limit=50", durationMs: [2]int{60, 200}},
			{op: "http.client", description: "GET /api/transactions?limit=10", durationMs: [2]int{50, 180}},
			{op: "http.client", description: "GET /api/releases?limit=5", durationMs: [2]int{30, 100}},
			{op: "browser.render", description: "vue component tree hydration", durationMs: [2]int{20, 80}},
			{op: "browser.paint", description: "LCP - issue list first visible", durationMs: [2]int{10, 40}},
		},
	},
	{
		name:       "issue list render",
		op:         "ui.render",
		durationMs: [2]int{8, 45},
		platform:   "javascript",
		spans: []spanNode{
			{op: "vue.render", description: "IssueListView - diff + patch", durationMs: [2]int{3, 15}},
			{op: "vue.render", description: "VirtualScroller (n=500 rows, 20 visible)", durationMs: [2]int{4, 20}},
			{op: "browser.paint", description: "composite - issue rows paint", durationMs: [2]int{2, 8}},
		},
	},
	{
		name:       "issue detail load",
		op:         "navigation",
		durationMs: [2]int{90, 400},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/issues/{id}", durationMs: [2]int{20, 80}},
			{op: "http.client", description: "GET /api/issues/{id}/events?limit=10", durationMs: [2]int{25, 100}},
			{op: "http.client", description: "GET /api/issues/{id}/comments", durationMs: [2]int{10, 40}},
			{op: "vue.render", description: "IssueDetailView - stack trace + breadcrumbs", durationMs: [2]int{15, 60}},
			{op: "browser.paint", description: "LCP - stack trace visible", durationMs: [2]int{5, 20}},
		},
	},
	{
		name:       "command palette open",
		op:         "ui.interaction",
		durationMs: [2]int{5, 30},
		platform:   "javascript",
		spans: []spanNode{
			{op: "vue.render", description: "CommandPalette mount + focus trap", durationMs: [2]int{3, 12}},
			{op: "http.client", description: "GET /api/search?q={query}&limit=10", durationMs: [2]int{20, 80}},
			{op: "vue.render", description: "CommandPalette results list (n=10)", durationMs: [2]int{2, 8}},
		},
	},
	{
		name:       "transaction detail load",
		op:         "navigation",
		durationMs: [2]int{120, 550},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/transactions/{id}", durationMs: [2]int{25, 90}},
			{op: "http.client", description: "GET /api/transactions/{id}/spans", durationMs: [2]int{30, 110}},
			{op: "vue.render", description: "TransactionDetailView - span waterfall", durationMs: [2]int{20, 75}},
			{op: "browser.paint", description: "LCP - span waterfall visible", durationMs: [2]int{8, 30}},
		},
	},
	{
		name:       "performance overview load",
		op:         "pageload",
		durationMs: [2]int{200, 1100},
		platform:   "javascript",
		spans: []spanNode{
			{op: "browser.cache", description: "service worker cache lookup", durationMs: [2]int{2, 8}},
			{op: "http.client", description: "GET /api/transactions/summary?limit=25", durationMs: [2]int{70, 220}},
			{op: "http.client", description: "GET /api/spans/db?hours=24", durationMs: [2]int{50, 160}},
			{op: "browser.render", description: "vue component tree hydration", durationMs: [2]int{25, 90}},
			{op: "browser.paint", description: "LCP - transaction table visible", durationMs: [2]int{12, 50}},
		},
	},
	{
		name:       "releases list load",
		op:         "navigation",
		durationMs: [2]int{80, 380},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/releases?limit=20", durationMs: [2]int{30, 120}},
			{op: "vue.render", description: "ReleasesView - release rows", durationMs: [2]int{10, 40}},
			{op: "browser.paint", description: "LCP - release list visible", durationMs: [2]int{5, 20}},
		},
	},
	{
		name:       "settings load",
		op:         "pageload",
		durationMs: [2]int{150, 700},
		platform:   "javascript",
		spans: []spanNode{
			{op: "browser.cache", description: "service worker cache lookup", durationMs: [2]int{2, 8}},
			{op: "http.client", description: "GET /api/settings", durationMs: [2]int{40, 140}},
			{op: "http.client", description: "GET /api/projects", durationMs: [2]int{35, 120}},
			{op: "http.client", description: "GET /api/users", durationMs: [2]int{30, 100}},
			{op: "browser.render", description: "vue component tree hydration", durationMs: [2]int{18, 65}},
			{op: "browser.paint", description: "LCP - settings panel visible", durationMs: [2]int{8, 35}},
		},
	},
	{
		name:       "release detail load",
		op:         "navigation",
		durationMs: [2]int{100, 460},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/releases/{id}", durationMs: [2]int{25, 90}},
			{op: "http.client", description: "GET /api/releases/{id}/issues", durationMs: [2]int{30, 110}},
			{op: "http.client", description: "GET /api/releases/{id}/transactions", durationMs: [2]int{28, 100}},
			{op: "vue.render", description: "ReleaseDetailView - issues + tx tables", durationMs: [2]int{15, 55}},
			{op: "browser.paint", description: "LCP - release stats visible", durationMs: [2]int{6, 25}},
		},
	},
}

var jsProject = projectDef{
	name:         "javascript",
	description:  "Frontend JavaScript SPA (Vue 3, TanStack Query, browser errors)",
	releases:     jsReleases,
	environments: defaultEnvironments,
	issues:       jsIssues,
	issueCounts:  []int{12, 5, 4, 7},
	txs:          jsTxs,
	txCounts:     []int{30, 20, 25, 8, 20, 25, 15, 22, 18},
}

// =============================================================================
// Registry
// =============================================================================

var projectRegistry map[string]projectDef

func init() {
	mixed := projectDef{
		name:         "mixed",
		description:  "All project types combined (Laravel, Go, Python, JavaScript)",
		releases:     []string{"1.4.2", "1.4.3", "2.9.1", "1.0.0", "1.5.0"},
		environments: defaultEnvironments,
	}
	for _, src := range []projectDef{laravelProject, goProject, pythonProject, jsProject} {
		mixed.issues = append(mixed.issues, src.issues...)
		mixed.issueCounts = append(mixed.issueCounts, src.issueCounts...)
		mixed.txs = append(mixed.txs, src.txs...)
		mixed.txCounts = append(mixed.txCounts, src.txCounts...)
	}
	projectRegistry = map[string]projectDef{
		"laravel":    laravelProject,
		"go":         goProject,
		"python":     pythonProject,
		"javascript": jsProject,
		"mixed":      mixed,
	}
}

// =============================================================================
// Event builders
// =============================================================================

func buildErrorEvent(tmpl issueTemplate, ts time.Time, releases, envs []string) map[string]any {
	frames := make([]map[string]any, 0, len(tmpl.stacktrace))
	for _, f := range tmpl.stacktrace {
		frame := map[string]any{
			"function": f.function,
			"module":   f.module,
			"filename": f.filename,
			"lineno":   f.lineno,
			"in_app":   !strings.Contains(f.filename, "vendor") && !strings.Contains(f.filename, "node_modules"),
		}
		if f.contextLine != "" {
			frame["context_line"] = f.contextLine
			frame["pre_context"] = f.preContext
			frame["post_context"] = f.postContext
		}
		frames = append(frames, frame)
	}

	excValue := map[string]any{
		"type":  tmpl.excType,
		"value": tmpl.excValue,
		"stacktrace": map[string]any{
			"frames": frames,
		},
	}
	if tmpl.handled != nil {
		excValue["mechanism"] = map[string]any{
			"type":    "generic",
			"handled": *tmpl.handled,
		}
	}

	evt := map[string]any{
		"timestamp":   ts.Format(time.RFC3339Nano),
		"level":       tmpl.level,
		"platform":    tmpl.platform,
		"environment": randomChoice(envs),
		"release":     randomChoice(releases),
		"transaction": tmpl.transaction,
		"exception": map[string]any{
			"values": []map[string]any{excValue},
		},
		"tags":     buildTags(tmpl.platform),
		"contexts": buildContexts(tmpl.platform),
		"breadcrumbs": map[string]any{
			"values": []map[string]any{
				{
					"timestamp": ts.Add(-2 * time.Second).Format(time.RFC3339),
					"type":      "navigation",
					"message":   fmt.Sprintf("navigated to %s", tmpl.transaction),
				},
				{
					"timestamp": ts.Add(-500 * time.Millisecond).Format(time.RFC3339),
					"type":      "http",
					"message":   fmt.Sprintf("HTTP request to %s", tmpl.transaction),
					"data": map[string]any{
						"status_code": 500,
					},
				},
			},
		},
	}

	if mods, ok := modulesByPlatform[tmpl.platform]; ok {
		evt["modules"] = mods
	}

	if len(tmpl.fingerprint) > 0 {
		evt["fingerprint"] = tmpl.fingerprint
	}

	if tmpl.hasUser {
		u := seedUsers[rand.Intn(len(seedUsers))] //nolint:gosec
		user := map[string]any{"id": u.id}
		if u.username != "" {
			user["username"] = u.username
		}
		if u.email != "" {
			user["email"] = u.email
		}
		if u.name != "" {
			user["name"] = u.name
		}
		if u.ip != "" {
			user["ip_address"] = u.ip
		}
		evt["user"] = user
	}

	return evt
}

// buildSpanNodes recursively turns a []spanNode tree into flat Sentry span objects.
// Consecutive nodes with concurrent=true form a parallel group that all start at
// the same time as the first node in that group; the next sequential node starts
// when the slowest member of the group finishes.
func buildSpanNodes(nodes []spanNode, parentSpanID string, parentStart time.Time) []map[string]any {
	var out []map[string]any
	var cursor time.Time
	groupStart := parentStart
	groupEnd := parentStart

	for i, node := range nodes {
		var start time.Time
		if i == 0 || !node.concurrent {
			// Sequential: advance past the previous parallel group first.
			cursor = groupEnd
			start = cursor
			groupStart = cursor
			groupEnd = cursor
		} else {
			// Concurrent: start at the same time as the group's first member.
			start = groupStart
		}

		spanMs := jitterMs(node.durationMs[0], node.durationMs[1])
		if spanMs < 1 {
			spanMs = 1
		}
		end := start.Add(time.Duration(spanMs * float64(time.Millisecond)))

		spanID := newEventID()[:16]
		out = append(out, map[string]any{
			"span_id":         spanID,
			"parent_span_id":  parentSpanID,
			"op":              node.op,
			"description":     node.description,
			"start_timestamp": start.Format(time.RFC3339Nano),
			"timestamp":       end.Format(time.RFC3339Nano),
			"status":          "ok",
		})

		if len(node.children) > 0 {
			out = append(out, buildSpanNodes(node.children, spanID, start)...)
		}

		if end.After(groupEnd) {
			groupEnd = end
		}
	}

	return out
}

func buildTransaction(tmpl txTemplate, ts time.Time, releases, envs []string) map[string]any {
	totalMs := jitterMs(tmpl.durationMs[0], tmpl.durationMs[1])
	end := ts.Add(time.Duration(totalMs * float64(time.Millisecond)))

	traceID := newEventID()[:16]
	rootSpanID := newEventID()[:16]

	spans := buildSpanNodes(tmpl.spans, rootSpanID, ts)

	status := "ok"
	if rand.Float64() < 0.05 { //nolint:gosec - 5% chance of errored transaction
		status = "internal_error"
	}

	tx := map[string]any{
		"transaction":     tmpl.name,
		"start_timestamp": ts.Format(time.RFC3339Nano),
		"timestamp":       end.Format(time.RFC3339Nano),
		"platform":        tmpl.platform,
		"environment":     randomChoice(envs),
		"release":         randomChoice(releases),
		"contexts": map[string]any{
			"trace": map[string]any{
				"trace_id": traceID,
				"span_id":  rootSpanID,
				"op":       tmpl.op,
				"status":   status,
			},
		},
		"spans": spans,
	}

	if tmpl.op == "pageload" || tmpl.op == "navigation" {
		tx["measurements"] = seedWebVitals()
	}

	return tx
}

// seedWebVitals generates realistic Web Vitals with a ~70/20/10 good/ni/poor distribution.
func seedWebVitals() map[string]any {
	vitalMs := func(good, poor int) float64 { //nolint:gosec
		r := rand.Float64()
		switch {
		case r < 0.70:
			return jitterMs(good/4, good) // good range: 25%–100% of threshold
		case r < 0.90:
			return jitterMs(good, poor) // needs-improvement
		default:
			return jitterMs(poor, poor+poor/2) // poor
		}
	}
	clsVal := func() float64 { //nolint:gosec
		r := rand.Float64()
		switch {
		case r < 0.70:
			return rand.Float64() * 0.1 // good: 0–0.1
		case r < 0.90:
			return 0.1 + rand.Float64()*0.15 // needs-improvement: 0.1–0.25
		default:
			return 0.25 + rand.Float64()*0.2 // poor: 0.25–0.45
		}
	}
	return map[string]any{
		"lcp":  map[string]any{"value": vitalMs(2500, 4000), "unit": "millisecond"},
		"fcp":  map[string]any{"value": vitalMs(1800, 3000), "unit": "millisecond"},
		"inp":  map[string]any{"value": vitalMs(200, 500), "unit": "millisecond"},
		"ttfb": map[string]any{"value": vitalMs(800, 1800), "unit": "millisecond"},
		"cls":  map[string]any{"value": clsVal(), "unit": ""},
	}
}

// =============================================================================
// Main
// =============================================================================

// dbCreateMonitor inserts a cron monitor directly and returns its UUID.
func dbCreateMonitor(ctx context.Context, pool *pgxpool.Pool, projectID, name, schedule string, graceSecs int) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO cron_monitors (project_id, name, schedule, grace_period_secs, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id`,
		projectID, name, schedule, graceSecs,
	).Scan(&id)
	return id, err
}

func cronPing(baseURL, monitorID, status string, durationSecs float64) error {
	u := fmt.Sprintf("%s/api/cron/%s?status=%s", baseURL, monitorID, status)
	if durationSecs > 0 {
		u += fmt.Sprintf("&duration=%.1f", durationSecs)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping returned %s", resp.Status)
	}
	return nil
}

func cronCheckinStart(baseURL, monitorID string) (string, error) {
	u := fmt.Sprintf("%s/api/cron/%s/checkins/", baseURL, monitorID)
	body, _ := json.Marshal(map[string]string{"status": "in_progress"})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("checkin start returned %s", resp.Status)
	}
	var r struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&r) //nolint:errcheck
	return r.ID, nil
}

func cronCheckinFinish(baseURL, monitorID, checkinID, status string, durationSecs float64) error {
	u := fmt.Sprintf("%s/api/cron/%s/checkins/%s/", baseURL, monitorID, checkinID)
	payload := map[string]any{"status": status, "duration": durationSecs}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checkin finish returned %s", resp.Status)
	}
	return nil
}

// =============================================================================
// Uptime monitor seeding
// =============================================================================

func seedUptimeMonitors(pool *pgxpool.Pool, projectID string) {
	ctx := context.Background()

	// outageWindow is a closed interval [startAgo, endAgo] where startAgo is
	// further back in time (larger duration). A check whose "ago" falls in this
	// range is seeded as "down".
	type outageWindow struct {
		startAgo time.Duration
		endAgo   time.Duration
		errMsg   string
		code     int // 0 means no HTTP response (network error)
	}

	type monitorSpec struct {
		name          string
		url           string
		method        string
		intervalSecs  int
		expectedCodes string
		bodyContains  *string
		monitorStatus string // "active" or "paused"
		description   string
		baseRespMs    int // median response time for healthy checks
		outages       []outageWindow
		forceDown     bool // force last 3 historical checks + fresh check to "down"
		noChecks      bool // skip all check history (state remains "unknown")
	}

	sp := func(v string) *string { return &v }
	ip := func(v int) *int { return &v }

	specs := []monitorSpec{
		{
			name: "API health check", url: "https://api.example.com/health",
			method: "GET", intervalSecs: 60, expectedCodes: "200",
			monitorStatus: "active", description: "mostly up — small outage 3 days ago and 10 days ago",
			baseRespMs: 85,
			outages: []outageWindow{
				{startAgo: 73 * time.Hour, endAgo: 71 * time.Hour, errMsg: "upstream timeout: no response within 10s"},
				{startAgo: 240 * time.Hour, endAgo: 236 * time.Hour, errMsg: "connection refused: ECONNREFUSED"},
			},
		},
		{
			name: "Marketing site", url: "https://www.example.com",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			bodyContains:  sp("Welcome"),
			monitorStatus: "active", description: "100% uptime over 7 days",
			baseRespMs: 125,
		},
		{
			name: "Payment gateway", url: "https://pay.example.com/ping",
			method: "HEAD", intervalSecs: 60, expectedCodes: "200",
			monitorStatus: "active", description: "currently down — 503 errors",
			baseRespMs: 95,
			outages: []outageWindow{
				{startAgo: 6 * time.Hour, endAgo: 5 * time.Hour, errMsg: "503 Service Unavailable", code: 503},
			},
			forceDown: true,
		},
		{
			name: "Admin panel", url: "https://admin.example.com",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			monitorStatus: "active", description: "up with two planned maintenance windows",
			baseRespMs: 210,
			outages: []outageWindow{
				{startAgo: 26 * time.Hour, endAgo: 24 * time.Hour, errMsg: "connection refused: maintenance window"},
				{startAgo: 146 * time.Hour, endAgo: 144 * time.Hour, errMsg: "connection refused: maintenance window"},
			},
		},
		{
			name: "Staging environment", url: "https://staging.example.com/health",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			monitorStatus: "paused", description: "paused — 30 checks recorded before pause",
			baseRespMs: 155,
		},
		{
			name: "CDN edge node", url: "https://cdn.example.com/assets/app.js",
			method: "HEAD", intervalSecs: 300, expectedCodes: "200,304",
			monitorStatus: "active", description: "no history yet — state unknown",
			noChecks: true,
		},
	}

	fmt.Println("\n=== Uptime Monitors ===")

	for _, spec := range specs {
		var monID string
		err := pool.QueryRow(ctx, `
			INSERT INTO uptime_monitors
				(project_id, name, url, method, interval_secs, timeout_secs,
				 expected_codes, body_contains, status)
			VALUES ($1,$2,$3,$4,$5,10,$6,$7,$8)
			RETURNING id`,
			projectID, spec.name, spec.url, spec.method, spec.intervalSecs,
			spec.expectedCodes, spec.bodyContains, spec.monitorStatus,
		).Scan(&monID)
		if err != nil {
			fmt.Printf("  FAIL  create monitor %q: %v\n", spec.name, err)
			continue
		}

		if spec.noChecks {
			fmt.Printf("  OK    %q — %s\n", spec.name, spec.description)
			continue
		}

		// Generate check history.
		//
		// Phase 1 — historical bulk: 168 checks at 1-hour spacing over the past
		// 7 days. This is enough to populate the 24h / 7d uptime stats and the
		// 20-check recent-checks strip.
		// Paused monitors get 30 checks at 1-hour spacing (~1.25 days).
		//
		// Phase 2 — fresh probe: one additional check placed at ~intervalSecs ago
		// so the monitor looks like it was just polled.
		now := time.Now().UTC()
		numHistorical := 168
		historyDuration := 7 * 24 * time.Hour
		if spec.monitorStatus == "paused" {
			numHistorical = 30
			historyDuration = 30 * time.Hour
		}
		spacing := historyDuration / time.Duration(numHistorical)

		var lastCode *int
		var lastRespMs *int
		var lastCheckedAt time.Time
		var lastOkAt time.Time
		consecutiveFailures := 0
		totalChecks := 0

		insertCheck := func(status string, code *int, respMs *int, errMsg *string, checkedAt time.Time) {
			_, ierr := pool.Exec(ctx, `
				INSERT INTO uptime_checks
					(monitor_id, status, status_code, response_ms, error, checked_at)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				monID, status, code, respMs, errMsg, checkedAt,
			)
			if ierr != nil {
				fmt.Printf("    FAIL  check at %s: %v\n", checkedAt.Format(time.RFC3339), ierr)
				return
			}
			totalChecks++
			lastCode = code
			lastRespMs = respMs
			lastCheckedAt = checkedAt
			if status == "up" {
				lastOkAt = checkedAt
				consecutiveFailures = 0
			} else {
				consecutiveFailures++
			}
		}

		// Phase 1: historical checks, oldest to newest.
		for i := 0; i < numHistorical; i++ {
			ago := historyDuration - time.Duration(i)*spacing
			checkedAt := now.Add(-ago)

			// Determine whether this check falls in a configured outage window.
			isDown := false
			var downCode *int
			var downErr string

			if spec.forceDown && i >= numHistorical-3 {
				isDown = true
				downCode = ip(503)
				downErr = "503 Service Unavailable"
			} else {
				for _, o := range spec.outages {
					if ago <= o.startAgo && ago >= o.endAgo {
						isDown = true
						downErr = o.errMsg
						if o.code != 0 {
							downCode = ip(o.code)
						}
						break
					}
				}
			}

			if isDown {
				errStr := downErr
				insertCheck("down", downCode, nil, &errStr, checkedAt)
			} else {
				c := 200
				r := spec.baseRespMs + rand.Intn(40) - 20 //nolint:gosec
				if r < 10 {
					r = 10
				}
				insertCheck("up", &c, &r, nil, checkedAt)
			}
		}

		// Phase 2: fresh probe to make the monitor look live.
		if spec.monitorStatus == "active" {
			freshAgo := time.Duration(spec.intervalSecs) * time.Second
			if freshAgo > 2*time.Minute {
				freshAgo = 2 * time.Minute
			}
			freshAt := now.Add(-freshAgo)
			if spec.forceDown {
				code := 503
				errStr := "503 Service Unavailable"
				insertCheck("down", &code, nil, &errStr, freshAt)
			} else {
				c := 200
				r := spec.baseRespMs + rand.Intn(20) - 10 //nolint:gosec
				if r < 10 {
					r = 10
				}
				insertCheck("up", &c, &r, nil, freshAt)
			}
		}

		// Derive final monitor state and persist it.
		// failureThreshold is 2 (mirrors the constant in internal/storage/uptime.go).
		finalState := "up"
		if lastCheckedAt.IsZero() {
			finalState = "unknown"
		} else if consecutiveFailures >= 2 {
			finalState = "down"
		}

		nextCheckAt := lastCheckedAt.Add(time.Duration(spec.intervalSecs) * time.Second)
		var lastOkAtPtr *time.Time
		if !lastOkAt.IsZero() {
			lastOkAtPtr = &lastOkAt
		}

		_, err = pool.Exec(ctx, `
			UPDATE uptime_monitors SET
				state                = $2,
				consecutive_failures = $3,
				last_checked_at      = $4,
				last_ok_at           = $5,
				next_check_at        = $6,
				last_status_code     = $7,
				last_response_ms     = $8
			WHERE id = $1`,
			monID, finalState, consecutiveFailures,
			lastCheckedAt, lastOkAtPtr, nextCheckAt,
			lastCode, lastRespMs,
		)
		if err != nil {
			fmt.Printf("    FAIL  update state: %v\n", err)
		}

		fmt.Printf("  OK    %q — %s (state=%s, %d checks)\n",
			spec.name, spec.description, finalState, totalChecks)
	}
}

func seedCronMonitors(target dsn, pool *pgxpool.Pool) {
	type monitorDef struct {
		name        string
		schedule    string
		graceSecs   int
		description string
		seed        func(id string)
	}

	monitors := []monitorDef{
		{
			name: "Daily backup", schedule: "0 2 * * *", graceSecs: 600,
			description: "multiple ok runs → state ok",
			seed: func(id string) {
				// Several historical ok runs.
				for i := 0; i < 5; i++ {
					dur := 8.5 + float64(i)*0.3
					if err := cronPing(target.baseURL, id, "ok", dur); err != nil {
						fmt.Printf("    FAIL ping: %v\n", err)
					}
					time.Sleep(50 * time.Millisecond)
				}
			},
		},
		{
			name: "Payment processor", schedule: "*/15 * * * *", graceSecs: 120,
			description: "last run errored → state error",
			seed: func(id string) {
				// A few ok runs, then an error.
				for i := 0; i < 3; i++ {
					cronPing(target.baseURL, id, "ok", 1.2) //nolint:errcheck
					time.Sleep(50 * time.Millisecond)
				}
				cronPing(target.baseURL, id, "error", 0.3) //nolint:errcheck
			},
		},
		{
			name: "Queue worker heartbeat", schedule: "*/5 * * * *", graceSecs: 90,
			description: "currently running → state in_progress",
			seed: func(id string) {
				// A few completed runs first.
				for i := 0; i < 2; i++ {
					cronPing(target.baseURL, id, "ok", 0.8) //nolint:errcheck
					time.Sleep(50 * time.Millisecond)
				}
				// Start a run but never finish it.
				cronCheckinStart(target.baseURL, id) //nolint:errcheck
			},
		},
		{
			name: "Weekly report", schedule: "0 9 * * 1", graceSecs: 1800,
			description: "never ran → state unknown",
			seed:        func(id string) { /* no check-ins */ },
		},
		{
			name: "Hourly data sync", schedule: "0 * * * *", graceSecs: 300,
			description: "recent ok run → state ok",
			seed: func(id string) {
				// Mix of start/finish and simple pings.
				for i := 0; i < 3; i++ {
					checkinID, err := cronCheckinStart(target.baseURL, id)
					if err != nil {
						cronPing(target.baseURL, id, "ok", 2.1) //nolint:errcheck
					} else {
						time.Sleep(30 * time.Millisecond)
						cronCheckinFinish(target.baseURL, id, checkinID, "ok", 2.1) //nolint:errcheck
					}
					time.Sleep(50 * time.Millisecond)
				}
			},
		},
		{
			name: "Email digest", schedule: "0 8 * * *", graceSecs: 600,
			description: "mixed ok/error history, last error → state error",
			seed: func(id string) {
				statuses := []string{"ok", "ok", "error", "ok", "error"}
				for _, s := range statuses {
					cronPing(target.baseURL, id, s, 3.0) //nolint:errcheck
					time.Sleep(50 * time.Millisecond)
				}
			},
		},
		{
			name: "Thumbnail generator", schedule: "*/30 * * * *", graceSecs: 180,
			description: "start/finish cycle history → state ok",
			seed: func(id string) {
				for i := 0; i < 4; i++ {
					checkinID, err := cronCheckinStart(target.baseURL, id)
					if err != nil {
						fmt.Printf("    FAIL start: %v\n", err)
						continue
					}
					time.Sleep(30 * time.Millisecond)
					if err := cronCheckinFinish(target.baseURL, id, checkinID, "ok", 0.5+float64(i)*0.1); err != nil {
						fmt.Printf("    FAIL finish: %v\n", err)
					}
					time.Sleep(50 * time.Millisecond)
				}
			},
		},
	}

	fmt.Println("\n=== Cron Monitors ===")
	for _, m := range monitors {
		id, err := dbCreateMonitor(context.Background(), pool, target.projectID, m.name, m.schedule, m.graceSecs)
		if err != nil {
			fmt.Printf("  FAIL  create monitor %q: %v\n", m.name, err)
			continue
		}
		fmt.Printf("  OK    monitor %q (%s) - %s\n", m.name, m.schedule, m.description)
		m.seed(id)
	}
}

var canonicalSections = []string{"issues", "transactions", "logs", "monitors", "alerts"}

// sectionAliases maps performance sub-view names to their canonical section.
var sectionAliases = map[string]string{
	"queries": "transactions",
	"caches":  "transactions",
	"jobs":    "transactions",
	"browser": "transactions",
}

func parseSeedSections(s string) (map[string]bool, error) {
	out := make(map[string]bool)
	if strings.TrimSpace(s) == "all" {
		for _, sec := range canonicalSections {
			out[sec] = true
		}
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		if canonical, ok := sectionAliases[part]; ok {
			out[canonical] = true
			continue
		}
		valid := false
		for _, v := range canonicalSections {
			if part == v {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("unknown section %q - valid: %s (or aliases: queries, caches, jobs, browser)",
				part, strings.Join(canonicalSections, ", "))
		}
		out[part] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--seed is empty")
	}
	return out, nil
}

// dotenvDBURL looks for DATABASE_URL in .env files walking up from the working directory.
func dotenvDBURL() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, name := range []string{".env", ".env.local"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "#") {
					continue
				}
				if val, ok := strings.CutPrefix(line, "DATABASE_URL="); ok {
					return strings.Trim(strings.TrimSpace(val), `"'`)
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func main() {
	projectType := flag.String("type", "mixed", "project type to seed")
	listTypes := flag.Bool("list", false, "list available project types and exit")
	dbURL := flag.String("db", "", "postgres connection URL for monitors (default: DATABASE_URL from .env)")
	seedSections := flag.String("seed", "all", `comma-separated sections: issues, transactions, logs, monitors, alerts (or "all")`)
	flag.Parse()

	if *listTypes {
		fmt.Println("Available project types:")
		names := make([]string, 0, len(projectRegistry))
		for k := range projectRegistry {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			def := projectRegistry[name]
			fmt.Printf("  %-14s %s\n", name, def.description)
		}
		fmt.Printf("\nUsage: go run scripts/seed/main.go --type=TYPE http://PUBLIC_KEY@HOST/PROJECT_ID\n")
		return
	}

	seed, err := parseSeedSections(*seedSections)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Resolve DB URL: flag → .env DATABASE_URL → empty (skip monitors).
	resolvedDB := *dbURL
	if resolvedDB == "" {
		resolvedDB = dotenvDBURL()
	}

	// monitors requires a DB; error early only when explicitly requested.
	if seed["monitors"] && !seed["issues"] && !seed["transactions"] && !seed["logs"] && resolvedDB == "" {
		fmt.Fprintf(os.Stderr, "error: --db or DATABASE_URL in .env is required to seed monitors\n")
		os.Exit(1)
	}

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--type=TYPE] [--seed=SECTIONS] [--db=POSTGRES_URL] <DSN>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  DSN format:  http://PUBLIC_KEY@HOST/PROJECT_ID\n")
		fmt.Fprintf(os.Stderr, "  --list       show available project types\n")
		fmt.Fprintf(os.Stderr, "  --seed       issues, transactions, logs, monitors, alerts (default: all)\n")
		os.Exit(1)
	}

	def, ok := projectRegistry[*projectType]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown project type %q - run --list to see options\n", *projectType)
		os.Exit(1)
	}

	target, err := parseDSN(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	sections := make([]string, 0, len(seed))
	for _, s := range canonicalSections {
		if seed[s] {
			sections = append(sections, s)
		}
	}

	fmt.Printf("Seeding → %s\n", target.envelopeURL())
	fmt.Printf("  Type:       %s (%s)\n", def.name, def.description)
	fmt.Printf("  Public key: %s\n", target.publicKey)
	fmt.Printf("  Sections:   %s\n\n", strings.Join(sections, ", "))

	sent := 0
	failed := 0

	sema := make(chan struct{}, 20) // max 20 concurrent senders
	var deliverWg sync.WaitGroup
	var mu sync.Mutex
	var ratePauseUntil time.Time

	deliver := func(label string, payload any, typ string) {
		sema <- struct{}{} // blocks when all 20 slots are busy
		deliverWg.Add(1)
		go func() {
			defer deliverWg.Done()
			defer func() { <-sema }()
			envelope := buildEnvelope([]envelopeItem{{typ: typ, payload: payload}})
			for {
				mu.Lock()
				pause := time.Until(ratePauseUntil)
				mu.Unlock()
				if pause > 0 {
					time.Sleep(pause)
					continue
				}
				retryWait, err := send(target, envelope)
				mu.Lock()
				if retryWait > 0 {
					if until := time.Now().Add(retryWait); until.After(ratePauseUntil) {
						fmt.Printf("  WAIT  rate limited - sleeping %s before retry…\n", retryWait.Round(time.Second))
						ratePauseUntil = until
					}
					mu.Unlock()
					continue
				}
				if err != nil {
					fmt.Printf("  FAIL  %s: %v\n", label, err)
					failed++
				} else {
					fmt.Printf("  OK    %s\n", label)
					sent++
				}
				mu.Unlock()
				break
			}
		}()
	}

	if seed["issues"] {
		fmt.Println("=== Issues ===")
		for i, tmpl := range def.issues {
			count := 5
			if i < len(def.issueCounts) {
				count = def.issueCounts[i]
			}
			for j := 0; j < count; j++ {
				ts := randomPast(7 * 24 * time.Hour)
				evt := buildErrorEvent(tmpl, ts, def.releases, def.environments)
				label := fmt.Sprintf("[%s] %s: %s", tmpl.platform, tmpl.excType, truncate(tmpl.excValue, 60))
				deliver(label, evt, "event")
			}
		}

		fmt.Println("\n=== Fresh events (last 10 min) ===")
		for i, tmpl := range def.issues {
			if i%2 == 0 {
				continue
			}
			ts := time.Now().UTC().Add(-time.Duration(rand.Intn(10*60)) * time.Second) //nolint:gosec
			evt := buildErrorEvent(tmpl, ts, def.releases, def.environments)
			label := fmt.Sprintf("[fresh] [%s] %s: %s", tmpl.platform, tmpl.excType, truncate(tmpl.excValue, 60))
			deliver(label, evt, "event")
		}
	}

	if seed["transactions"] {
		fmt.Println("\n=== Transactions ===")
		for i, tmpl := range def.txs {
			count := 5
			if i < len(def.txCounts) {
				count = def.txCounts[i]
			}
			for j := 0; j < count; j++ {
				ts := trafficBiasedTime(7 * 24 * time.Hour)
				tx := buildTransaction(tmpl, ts, def.releases, def.environments)
				label := fmt.Sprintf("[%s] %s", tmpl.platform, tmpl.name)
				deliver(label, tx, "transaction")
			}
		}

		fmt.Println("\n=== Fresh transactions (last 5 min) ===")
		freshCount := 6
		if len(def.txs) < freshCount {
			freshCount = len(def.txs)
		}
		for _, tmpl := range def.txs[:freshCount] {
			ts := time.Now().UTC().Add(-time.Duration(rand.Intn(5*60)) * time.Second) //nolint:gosec
			tx := buildTransaction(tmpl, ts, def.releases, def.environments)
			label := fmt.Sprintf("[fresh] [%s] %s", tmpl.platform, tmpl.name)
			deliver(label, tx, "transaction")
		}
	}

	if seed["logs"] {
		fmt.Println("\n=== Logs ===")
		logBatches := buildLogBatches(def.releases, def.environments)
		for i, batch := range logBatches {
			label := fmt.Sprintf("[logs] batch %d (%d records)", i+1, len(batch))
			deliver(label, batch, "log")
		}

		fmt.Println("\n=== Fresh logs (last 2 min) ===")
		freshLogs := buildFreshLogs(def.releases, def.environments)
		deliver("[logs] fresh batch", freshLogs, "log")
	}

	if seed["monitors"] {
		if resolvedDB != "" {
			pool, err := pgxpool.New(context.Background(), resolvedDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect to DB: %v\n", err)
				os.Exit(1)
			}
			defer pool.Close()
			seedCronMonitors(target, pool)
			seedUptimeMonitors(pool, target.projectID)
		} else {
			fmt.Println("\n(skip monitors - set DATABASE_URL in .env or pass --db=POSTGRES_URL)")
		}
	}

	deliverWg.Wait()

	if seed["alerts"] {
		if resolvedDB != "" {
			pool, err := pgxpool.New(context.Background(), resolvedDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect to DB: %v\n", err)
				os.Exit(1)
			}
			defer pool.Close()
			seedAlertRules(pool)
		} else {
			fmt.Println("\n(skip alerts — set DATABASE_URL in .env or pass --db=POSTGRES_URL)")
		}
	}

	fmt.Printf("\nDone. %d sent, %d failed.\n", sent, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// =============================================================================
// Alert rule seeding
// =============================================================================

func seedAlertRules(pool *pgxpool.Pool) {
	// firingDef describes one alert_firings row to seed for a rule.
	type firingDef struct {
		firedAgo   time.Duration
		status     string // "success", "failed", "pending"
		statusCode *int
		errMsg     *string
		itemCount  *int
		attempt    int
		retryIn    *time.Duration // only for pending: offset from now for next_retry_at
	}

	type alertDef struct {
		name         string
		enabled      bool
		trigger      string
		threshold    *int
		windowMins   *int
		channel      string
		webhookURL   *string
		emailTo      *string
		cooldownMins int
		filterLevel  *string
		filterEnv    *string
		lastFiredAgo *time.Duration
		firings      []firingDef
	}

	ip := func(v int) *int { return &v }
	sp := func(v string) *string { return &v }
	dp := func(v time.Duration) *time.Duration { return &v }

	rules := []alertDef{
		{
			name: "Error spike — production", enabled: true,
			trigger: "event_count", threshold: ip(50), windowMins: ip(60),
			channel: "email", emailTo: sp("alerts@example.com"),
			cooldownMins: 60, filterEnv: sp("production"),
			lastFiredAgo: dp(45 * time.Minute),
			firings: []firingDef{
				{firedAgo: 45 * time.Minute, status: "success", statusCode: ip(200), itemCount: ip(67), attempt: 1},
				{firedAgo: 3 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(52), attempt: 1},
				{firedAgo: 8 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(88), attempt: 1},
				{firedAgo: 29 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(44), attempt: 1},
				{firedAgo: 53 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(71), attempt: 1},
			},
		},
		{
			name: "New fatal issues", enabled: true,
			trigger: "new_issue",
			channel: "slack", webhookURL: sp("https://hooks.slack.com/services/example/placeholder"),
			cooldownMins: 30, filterLevel: sp("fatal"),
			lastFiredAgo: dp(3 * time.Hour),
			firings: []firingDef{
				{firedAgo: 3 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(3), attempt: 1},
				{firedAgo: 6 * time.Hour, status: "failed", statusCode: ip(503), errMsg: sp("Slack returned 503 Service Unavailable"), itemCount: ip(2), attempt: 3},
				{firedAgo: 11 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(1), attempt: 1},
				// Pending retry: got 429, scheduled for retry in ~8 minutes.
				{firedAgo: 27 * time.Hour, status: "pending", statusCode: ip(429), errMsg: sp("Too Many Requests"), itemCount: ip(4), attempt: 2, retryIn: dp(8 * time.Minute)},
				{firedAgo: 51 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(2), attempt: 1},
			},
		},
		{
			name: "Production regression", enabled: true,
			trigger: "regressed",
			channel: "email", emailTo: sp("team@example.com"),
			cooldownMins: 120, filterEnv: sp("production"),
			lastFiredAgo: dp(26 * time.Hour),
			firings: []firingDef{
				{firedAgo: 26 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(1), attempt: 1},
				{firedAgo: 72 * time.Hour, status: "failed", statusCode: ip(500), errMsg: sp("SMTP delivery failed: connection timed out after 30s"), itemCount: ip(1), attempt: 3},
				{firedAgo: 120 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(1), attempt: 1},
			},
		},
		{
			name: "New or regressed — all envs", enabled: true,
			trigger: "new_or_regressed",
			channel: "email", emailTo: sp("oncall@example.com"),
			cooldownMins: 60,
			lastFiredAgo: dp(8 * time.Hour),
			firings: []firingDef{
				{firedAgo: 8 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(5), attempt: 1},
				// Pending: first attempt failed with connection refused, retrying shortly.
				{firedAgo: 30 * time.Hour, status: "pending", errMsg: sp("dial tcp: connection refused"), itemCount: ip(8), attempt: 1, retryIn: dp(3 * time.Minute)},
				{firedAgo: 54 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(3), attempt: 1},
				{firedAgo: 96 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(2), attempt: 1},
			},
		},
		{
			name: "Missed scheduled jobs", enabled: true,
			trigger: "cron_missed",
			channel: "email", emailTo: sp("alerts@example.com"),
			cooldownMins: 30,
			lastFiredAgo: nil, // never fired
			firings:      nil,
		},
		{
			name: "High error volume (disabled)", enabled: false,
			trigger: "event_count", threshold: ip(200), windowMins: ip(30),
			channel: "discord", webhookURL: sp("https://discord.com/api/webhooks/example/placeholder"),
			cooldownMins: 60,
			lastFiredAgo: dp(48 * time.Hour),
			firings: []firingDef{
				{firedAgo: 48 * time.Hour, status: "failed", statusCode: ip(401), errMsg: sp("Discord returned 401 Unauthorized: invalid webhook token"), itemCount: ip(234), attempt: 3},
				{firedAgo: 96 * time.Hour, status: "success", statusCode: ip(204), itemCount: ip(189), attempt: 1},
				{firedAgo: 144 * time.Hour, status: "success", statusCode: ip(204), itemCount: ip(211), attempt: 1},
			},
		},
	}

	fmt.Println("\n=== Alert Rules ===")
	for _, r := range rules {
		var lastFiredAt *time.Time
		if r.lastFiredAgo != nil {
			t := time.Now().UTC().Add(-*r.lastFiredAgo)
			lastFiredAt = &t
		}

		var id string
		err := pool.QueryRow(context.Background(), `
			INSERT INTO alert_rules
				(name, enabled, trigger, threshold, window_mins,
				 channel, webhook_url, email_to, cooldown_mins,
				 filter_level, filter_environment, min_occurrences, last_fired_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id`,
			r.name, r.enabled, r.trigger, r.threshold, r.windowMins,
			r.channel, r.webhookURL, r.emailTo, r.cooldownMins,
			r.filterLevel, r.filterEnv, nil, lastFiredAt,
		).Scan(&id)
		if err != nil {
			fmt.Printf("  FAIL  create alert %q: %v\n", r.name, err)
			continue
		}

		status := "enabled"
		if !r.enabled {
			status = "disabled"
		}
		if lastFiredAt != nil {
			fmt.Printf("  OK    %q (%s, %s) last fired %s ago\n", r.name, r.trigger, status, r.lastFiredAgo.Round(time.Minute))
		} else {
			fmt.Printf("  OK    %q (%s, %s) — never fired\n", r.name, r.trigger, status)
		}

		for _, f := range r.firings {
			firedAt := time.Now().UTC().Add(-f.firedAgo)
			var nextRetryAt *time.Time
			if f.retryIn != nil {
				t := time.Now().UTC().Add(*f.retryIn)
				nextRetryAt = &t
			}
			var firingID string
			ferr := pool.QueryRow(context.Background(), `
				INSERT INTO alert_firings
					(rule_id, fired_at, trigger, channel, status, status_code, error, item_count, attempt, next_retry_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				RETURNING id`,
				id, firedAt, r.trigger, r.channel,
				f.status, f.statusCode, f.errMsg, f.itemCount,
				f.attempt, nextRetryAt,
			).Scan(&firingID)
			if ferr != nil {
				fmt.Printf("    FAIL  firing (%s, -%s): %v\n", f.status, f.firedAgo.Round(time.Minute), ferr)
			} else {
				retryNote := ""
				if nextRetryAt != nil {
					retryNote = fmt.Sprintf(" retry in %s", f.retryIn.Round(time.Minute))
				}
				fmt.Printf("    firing  %-8s attempt=%d -%s%s\n", f.status, f.attempt, f.firedAgo.Round(time.Minute), retryNote)
			}
		}
	}
}

// =============================================================================
// Log seeding
// =============================================================================

var logMessages = map[string][]string{
	"trace": {
		"entering handler %s",
		"db query: SELECT * FROM %s WHERE id = $1",
		"cache lookup: key=%s hit=false",
		"serializing response: %d bytes",
		"middleware chain: %s",
	},
	"debug": {
		"request started: %s %s",
		"auth token validated for user %s",
		"cache hit: key=%s ttl=%ds",
		"query took %dms: SELECT FROM %s",
		"config loaded: %s=%s",
		"background job enqueued: %s",
		"session resumed: user_id=%s",
	},
	"info": {
		"user %s logged in from %s",
		"order #%d created by user %s",
		"password reset email sent to %s",
		"project %s: new deploy %s received",
		"cron job %s completed in %dms",
		"webhook delivered to %s: %d",
		"rate limit check passed: %s %d/%d",
		"file upload complete: %s (%d bytes)",
		"email delivered: %s → %s",
		"signup: user %s joined plan %s",
	},
	"warning": {
		"slow query detected: %dms > threshold %dms",
		"retry attempt %d/3 for %s",
		"disk usage at %d%% of quota",
		"deprecated endpoint %s called by %s",
		"auth: unusual login location for user %s",
		"queue depth high: %d items pending",
		"memory usage: %dMB (warning threshold %dMB)",
	},
	"error": {
		"database connection failed: %s",
		"failed to send email to %s: connection refused",
		"panic recovered in handler %s: %s",
		"payment processing failed for order #%d: %s",
		"file not found: %s",
		"external API %s returned 503 after %dms",
		"failed to acquire lock for job %s",
	},
	"fatal": {
		"cannot connect to database after %d retries: %s",
		"config error: required env var %s is missing",
		"migration failed at version %d: %s",
		"port %d already in use - cannot start",
	},
}

var logRoutes = []string{
	"GET /api/users", "POST /api/orders", "GET /api/products",
	"PUT /api/users/{id}", "DELETE /api/sessions", "GET /api/dashboard",
	"POST /api/webhooks", "GET /api/reports", "POST /api/payments",
}

var logTables = []string{"users", "orders", "products", "sessions", "events", "logs"}
var logJobs = []string{"send_digest", "cleanup_sessions", "sync_inventory", "generate_report", "process_refunds"}
var logKeys = []string{"feature_flags", "user_prefs", "rate_limits", "config_v2"}
var logPlans = []string{"free", "pro", "enterprise"}
var logAPIs = []string{"stripe.com", "sendgrid.com", "twilio.com", "github.com"}

func logBody(level string) string {
	msgs := logMessages[level]
	if len(msgs) == 0 {
		return "log entry"
	}
	tmpl := randomChoice(msgs)
	user := randomChoice(seedUsers)
	route := randomChoice(logRoutes)
	table := randomChoice(logTables)
	job := randomChoice(logJobs)
	api := randomChoice(logAPIs)
	n := rand.Intn(9000) + 1000 //nolint:gosec

	s := tmpl
	s = strings.ReplaceAll(s, "%s", func() string {
		candidates := []string{user.username, user.email, route, table, job, api, randomChoice(logKeys), randomChoice(logPlans)}
		return randomChoice(candidates)
	}())
	s = strings.ReplaceAll(s, "%d", strconv.Itoa(n))
	return s
}

func buildLogRecord(level string, ts time.Time, releases, environments []string) map[string]any {
	env := randomChoice(environments)
	release := randomChoice(releases)

	attrs := map[string]any{
		"sentry.environment": env,
		"sentry.release":     release,
	}

	// Attach a few extra structured attributes for variety.
	switch level {
	case "error", "fatal":
		attrs["http.status_code"] = 500
		attrs["http.method"] = randomChoice([]string{"GET", "POST", "PUT"})
		attrs["http.route"] = randomChoice(logRoutes)
	case "info":
		attrs["http.status_code"] = 200
		attrs["http.method"] = "GET"
		attrs["http.route"] = randomChoice(logRoutes)
		attrs["user.id"] = randomChoice(seedUsers).id
	case "warning":
		attrs["duration_ms"] = rand.Intn(4000) + 500 //nolint:gosec
	}

	rec := map[string]any{
		"timestamp":  float64(ts.UnixNano()) / 1e9,
		"level":      level,
		"body":       logBody(level),
		"attributes": attrs,
	}

	// Occasionally attach a trace_id.
	if rand.Intn(3) == 0 { //nolint:gosec
		rec["trace_id"] = newEventID()[:32]
	}

	return rec
}

var logLevelWeights = []struct {
	level  string
	weight int
}{
	{"trace", 5},
	{"debug", 15},
	{"info", 50},
	{"warning", 20},
	{"error", 8},
	{"fatal", 2},
}

func pickLogLevel() string {
	total := 0
	for _, lw := range logLevelWeights {
		total += lw.weight
	}
	n := rand.Intn(total) //nolint:gosec
	for _, lw := range logLevelWeights {
		n -= lw.weight
		if n < 0 {
			return lw.level
		}
	}
	return "info"
}

func buildLogBatches(releases, environments []string) [][]map[string]any {
	// Send historical logs in batches of 20 spread over the past 7 days.
	total := 80
	batchSize := 20
	var batches [][]map[string]any
	for i := 0; i < total; i += batchSize {
		var batch []map[string]any
		for j := 0; j < batchSize && i+j < total; j++ {
			ts := randomPast(7 * 24 * time.Hour)
			level := pickLogLevel()
			batch = append(batch, buildLogRecord(level, ts, releases, environments))
		}
		batches = append(batches, batch)
	}
	return batches
}

func buildFreshLogs(releases, environments []string) []map[string]any {
	var batch []map[string]any
	for i := 0; i < 10; i++ {
		ago := time.Duration(rand.Intn(2*60)) * time.Second //nolint:gosec
		ts := time.Now().UTC().Add(-ago)
		level := pickLogLevel()
		batch = append(batch, buildLogRecord(level, ts, releases, environments))
	}
	return batch
}
