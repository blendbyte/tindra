// Seed script: delivers realistic issue and transaction data to a Tindra DSN.
//
// Usage:
//
//	go run scripts/seed/main.go [--type=TYPE] [--seed=SECTIONS] [--scale=N] [--db=POSTGRES_URL] <DSN>
//	go run scripts/seed/main.go --showcase [--scale=N] [--db=POSTGRES_URL] <SERVER_URL>
//	go run scripts/seed/main.go --list
//
// --showcase seeds a whole shop for screenshots: four projects, a team, and
// monitors and alert rules spread over them (see README.md).
//
// DSN format: http://PUBLIC_KEY@HOST/PROJECT_ID
//
// --seed accepts a comma-separated list of sections, or "all" (default):
//
//	issues        error events
//	transactions  transactions + spans (also feeds queries/caches/jobs/browser views)
//	profiles      profiled transactions with flame graphs, both wire formats
//	logs          structured log records
//	monitors      cron and uptime monitor history (requires --db)
//	alerts        disabled alert rules and delivery history (requires --db)
//	workflow      linked investigations, with issue states and comments when --db is set
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
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
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
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return dsn{}, fmt.Errorf("DSN must use http or https with a host and no query or fragment")
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

var seedHTTPClient = &http.Client{Timeout: 20 * time.Second}

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
	for _, item := range items {
		if payload, ok := item.payload.(map[string]any); ok && item.typ == "event" && payload["event_id"] != nil {
			header["event_id"] = payload["event_id"]
			break
		}
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

	resp, err := seedHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := 60 * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, e := strconv.Atoi(ra); e == nil && secs > 0 {
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
	{"101", "jdoe", "jane.doe@example.com", "Jane Doe", "203.0.113.42"},
	{"204", "m_chen", "marcus.chen@example.com", "Marcus Chen", "198.51.100.17"},
	{"317", "priya_k", "priya@example.com", "Priya Krishnamurthy", "203.0.113.91"},
	{"482", "tobiasw", "tobias.w@example.com", "Tobias Weber", "198.51.100.55"},
	{"539", "lfernandez", "l.fernandez@example.com", "Lucía Fernández", "203.0.113.8"},
	{"601", "anon_user", "", "", "198.51.100.200"},
	{"718", "saraht", "sarah.thornton@example.com", "Sarah Thornton", "203.0.113.34"},
	{"825", "devraj_m", "devraj@example.com", "Devraj Mehta", "198.51.100.77"},
	{"890", "nina_ok", "nina@example.com", "Nina Okonkwo", "203.0.113.19"},
	{"912", "guest", "", "", "198.51.100.9"},
}

// sentryUserMap is the event/transaction `user` object from set_user().
func sentryUserMap(u seedUser) map[string]any {
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
	return user
}

// sentryUserAttrs is the same identity as log attributes (user.id, …).
func sentryUserAttrs(u seedUser) map[string]any {
	attrs := map[string]any{"user.id": u.id}
	if u.username != "" {
		attrs["user.username"] = u.username
	}
	if u.email != "" {
		attrs["user.email"] = u.email
	}
	if u.name != "" {
		attrs["user.name"] = u.name
	}
	return attrs
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
	now := time.Now().UTC()
	t := randomPast(maxAgo)
	h := weightedHour()
	m := rand.Intn(60) //nolint:gosec
	s := rand.Intn(60) //nolint:gosec
	candidate := time.Date(t.Year(), t.Month(), t.Day(), h, m, s, 0, time.UTC)
	if candidate.After(now) || candidate.Before(now.Add(-maxAgo)) {
		return t
	}
	return candidate
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
			[]string{"Chrome", "Mobile Safari", "Safari", "Firefox", "Edge"},
			[]int{46, 24, 12, 11, 7},
		)
		tags["browser.version"] = weightedChoice(
			[]string{"139.0", "140.0", "141.0", "26.0"},
			[]int{15, 35, 30, 20},
		)
		tags["os.name"] = weightedChoice(
			[]string{"iOS", "Android", "macOS", "Windows"},
			[]int{30, 22, 26, 22},
		)
		tags["url"] = randomChoice([]string{
			"https://shop.example.com/", "https://shop.example.com/cart", "https://shop.example.com/checkout", "https://shop.example.com/products/trail-runner-gtx",
		})
	case "go":
		tags["go.version"] = weightedChoice(
			[]string{"go1.25.3", "go1.26.1"},
			[]int{35, 65},
		)
		tags["server_name"] = weightedChoice(
			[]string{"payments-7f9c", "payments-b21d", "payments-e4a0"},
			[]int{40, 35, 25},
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
			"version":         weightedChoice([]string{"go1.25.3", "go1.26.1"}, []int{35, 65}),
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
		browser := weightedChoice([]string{"Chrome", "Mobile Safari", "Safari", "Firefox", "Edge"}, []int{46, 24, 12, 11, 7})
		browserVersion := map[string]string{
			"Chrome":        weightedChoice([]string{"140.0.7339", "141.0.7390"}, []int{45, 55}),
			"Mobile Safari": weightedChoice([]string{"18.6", "26.0"}, []int{35, 65}),
			"Safari":        weightedChoice([]string{"18.6", "26.0"}, []int{40, 60}),
			"Firefox":       weightedChoice([]string{"143.0", "144.0"}, []int{40, 60}),
			"Edge":          weightedChoice([]string{"140.0.3485", "141.0.3537"}, []int{40, 60}),
		}[browser]
		ctx["browser"] = map[string]any{
			"name":    browser,
			"version": browserVersion,
		}
		// Browser events carry the visitor's OS, not a server's.
		osName := map[string]string{"Mobile Safari": "iOS"}[browser]
		if osName == "" {
			osName = weightedChoice([]string{"macOS", "Windows", "Android"}, []int{40, 38, 22})
		}
		ctx["os"] = map[string]any{"name": osName}
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
		excValue:    "SQLSTATE[23000]: Integrity constraint violation: 1062 Duplicate entry 'alice@example.com' for key 'users.users_email_unique'",
		level:       "error",
		platform:    "php",
		transaction: "POST /api/auth/register",
		handled:     new(false),
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
		excValue:    "The card number field is required. (and 2 more errors)",
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
		excValue:    "This action is unauthorized.",
		level:       "error",
		platform:    "php",
		transaction: "POST /api/orders/{id}/refund",
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
				function:    "refund",
				module:      "App\\Policies\\OrderPolicy",
				filename:    "app/Policies/OrderPolicy.php",
				lineno:      41,
				preContext:  []string{"    public function refund(User $user, Order $order): bool", "    {"},
				contextLine: "        return $user->can('orders.refund') && $order->total <= $user->refund_limit;",
				postContext: []string{"    }", ""},
			},
			{
				function: "handle",
				module:   "Illuminate\\Auth\\Middleware\\Authorize",
				filename: "vendor/laravel/framework/src/Illuminate/Auth/Middleware/Authorize.php",
				lineno:   44,
			},
			{
				function:    "store",
				module:      "App\\Http\\Controllers\\Api\\RefundController",
				filename:    "app/Http/Controllers/Api/RefundController.php",
				lineno:      27,
				preContext:  []string{"    public function store(RefundRequest $request, Order $order): JsonResponse", "    {"},
				contextLine: "        $this->authorize('refund', $order);",
				postContext: []string{"        $refund = $this->refunds->create($order, $request->validated());", "        return response()->json($refund, 201);", "    }"},
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
		excValue:    "Your card was declined.",
		level:       "error",
		platform:    "php",
		transaction: "POST /api/subscriptions",
		handled:     new(false),
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
		excValue:    "App\\Jobs\\SyncProductCatalog has been attempted too many times.",
		level:       "fatal",
		platform:    "php",
		transaction: "queue:work --queue=sync",
		handled:     new(false),
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
		excValue:    "Call to a member function load() on null",
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
		excValue:    "Error while reading line from the server. [tcp://redis:6379]",
		level:       "error",
		platform:    "php",
		transaction: "GET /admin/dashboard",
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
				module:      "App\\Http\\Controllers\\Admin\\DashboardController",
				filename:    "app/Http/Controllers/Admin/DashboardController.php",
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
		name:       "GET /admin/dashboard",
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
	// N+1: loading the author of each review on a product page
	{
		name:       "GET /api/products/{slug}/reviews",
		op:         "http.server",
		durationMs: [2]int{90, 260},
		platform:   "php",
		spans: []spanNode{
			{op: "db.query", description: "SELECT `id`, `customer_id`, `rating`, `body`, `created_at` FROM `product_reviews` WHERE `product_id` = ? AND `approved` = 1 ORDER BY `created_at` DESC LIMIT 8", durationMs: [2]int{6, 20}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
			{op: "db.query", description: "SELECT `id`, `first_name`, `avatar_url` FROM `customers` WHERE `id` = ? LIMIT 1", durationMs: [2]int{2, 6}},
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

// The shop's payment service: authorizes and captures card payments, handles
// provider webhooks and reconciles the ledger.
var goIssues = []issueTemplate{
	{
		excType:     "*url.Error",
		excValue:    `Post "https://api.stripe.com/v1/payment_intents": context deadline exceeded`,
		level:       "error",
		platform:    "go",
		transaction: "POST /v1/payments/authorize",
		hasUser:     true,
		stacktrace: []stackFrame{
			{"(*Client).do", "net/http", "net/http/client.go", 724, "", nil, nil},
			{
				function:    "(*Gateway).CreateIntent",
				module:      "payments/internal/stripe",
				filename:    "internal/stripe/gateway.go",
				lineno:      88,
				preContext:  []string{"\treq.Header.Set(\"Idempotency-Key\", key)", "\tctx, cancel := context.WithTimeout(ctx, g.timeout)", "\tdefer cancel()"},
				contextLine: "\tresp, err := g.http.Do(req.WithContext(ctx))",
				postContext: []string{"\tif err != nil {", "\t\treturn nil, fmt.Errorf(\"create intent: %w\", err)", "\t}"},
			},
			{
				function:    "(*Service).Authorize",
				module:      "payments/internal/payments",
				filename:    "internal/payments/authorize.go",
				lineno:      54,
				preContext:  []string{"\tif err := s.ledger.Reserve(ctx, p); err != nil {", "\t\treturn nil, err", "\t}"},
				contextLine: "\tintent, err := s.gateway.CreateIntent(ctx, p.Amount, p.Currency, p.IdempotencyKey)",
				postContext: []string{"\tif err != nil {", "\t\treturn nil, s.release(ctx, p, err)", "\t}"},
			},
			{"(*Handler).authorize", "payments/internal/api", "internal/api/payments.go", 41, "", nil, nil},
			{"ServeHTTP", "github.com/go-chi/chi/v5", "github.com/go-chi/chi/v5/mux.go", 90, "", nil, nil},
			{"(*conn).serve", "net/http", "net/http/server.go", 2092, "", nil, nil},
		},
	},
	{
		excType:     "*webhook.SignatureError",
		excValue:    "no signatures found matching the expected signature for payload",
		level:       "warning",
		platform:    "go",
		transaction: "POST /webhooks/stripe",
		stacktrace: []stackFrame{
			{
				function:    "Verify",
				module:      "payments/internal/webhook",
				filename:    "internal/webhook/verify.go",
				lineno:      37,
				preContext:  []string{"\texpected := sign(secret, ts, body)", "\tfor _, sig := range header.Signatures {", "\t\tif hmac.Equal(sig, expected) {", "\t\t\treturn nil", "\t\t}", "\t}"},
				contextLine: "\treturn &SignatureError{Reason: \"no signatures found matching the expected signature for payload\"}",
				postContext: []string{"}"},
			},
			{"(*Handler).stripeWebhook", "payments/internal/api", "internal/api/webhooks.go", 29, "", nil, nil},
			{"ServeHTTP", "github.com/go-chi/chi/v5", "github.com/go-chi/chi/v5/mux.go", 90, "", nil, nil},
		},
	},
	{
		excType:     "*pgconn.PgError",
		excValue:    "ERROR: deadlock detected (SQLSTATE 40P01)",
		level:       "error",
		platform:    "go",
		transaction: "ledger.reconcile",
		stacktrace: []stackFrame{
			{"(*Conn).Exec", "github.com/jackc/pgx/v5", "github.com/jackc/pgx/v5/conn.go", 457, "", nil, nil},
			{
				function:    "(*Ledger).Settle",
				module:      "payments/internal/ledger",
				filename:    "internal/ledger/settle.go",
				lineno:      112,
				preContext:  []string{"\tfor _, entry := range batch {", "\t\t// Entries arrive in provider order, not account order."},
				contextLine: "\t\tif _, err := tx.Exec(ctx, settleEntry, entry.AccountID, entry.Amount); err != nil {",
				postContext: []string{"\t\t\treturn fmt.Errorf(\"settle %s: %w\", entry.ID, err)", "\t\t}", "\t}"},
			},
			{"(*Reconciler).Run", "payments/internal/ledger", "internal/ledger/reconcile.go", 66, "", nil, nil},
			{"(*Scheduler).run", "payments/internal/jobs", "internal/jobs/scheduler.go", 48, "", nil, nil},
		},
	},
	{
		excType:     "runtime.Error",
		excValue:    "invalid memory address or nil pointer dereference",
		level:       "fatal",
		platform:    "go",
		transaction: "POST /v1/refunds",
		handled:     new(false),
		stacktrace: []stackFrame{
			{
				function:    "(*Service).Refund",
				module:      "payments/internal/payments",
				filename:    "internal/payments/refund.go",
				lineno:      73,
				preContext:  []string{"\tcharge, err := s.store.Charge(ctx, req.PaymentID)", "\tif err != nil {", "\t\treturn nil, err", "\t}"},
				contextLine: "\tif req.Amount > charge.Capture.Amount-charge.Refunded {",
				postContext: []string{"\t\treturn nil, ErrRefundExceedsCapture", "\t}"},
			},
			{"(*Handler).refund", "payments/internal/api", "internal/api/refunds.go", 35, "", nil, nil},
			{"ServeHTTP", "github.com/go-chi/chi/v5", "github.com/go-chi/chi/v5/mux.go", 90, "", nil, nil},
			{"(*conn).serve", "net/http", "net/http/server.go", 2092, "", nil, nil},
		},
	},
	{
		excType:     "*payments.IdempotencyError",
		excValue:    "idempotency key reused with a different amount",
		level:       "warning",
		platform:    "go",
		transaction: "POST /v1/payments/authorize",
		stacktrace: []stackFrame{
			{
				function:    "(*Store).Claim",
				module:      "payments/internal/payments",
				filename:    "internal/payments/idempotency.go",
				lineno:      52,
				preContext:  []string{"\tif prev.Amount != p.Amount || prev.Currency != p.Currency {"},
				contextLine: "\t\treturn &IdempotencyError{Key: p.IdempotencyKey, Reason: \"reused with a different amount\"}",
				postContext: []string{"\t}", "\treturn nil"},
			},
			{"(*Service).Authorize", "payments/internal/payments", "internal/payments/authorize.go", 39, "", nil, nil},
			{"(*Handler).authorize", "payments/internal/api", "internal/api/payments.go", 41, "", nil, nil},
		},
	},
	{
		excType:     "*json.SyntaxError",
		excValue:    "invalid character '<' looking for beginning of value",
		level:       "error",
		platform:    "go",
		transaction: "fx.refresh",
		stacktrace: []stackFrame{
			{"(*decodeState).unmarshal", "encoding/json", "encoding/json/decode.go", 175, "", nil, nil},
			{
				function:    "(*Rates).Refresh",
				module:      "payments/internal/fx",
				filename:    "internal/fx/rates.go",
				lineno:      61,
				preContext:  []string{"\tdefer resp.Body.Close()", "\tvar body ratesResponse"},
				contextLine: "\tif err := json.NewDecoder(resp.Body).Decode(&body); err != nil {",
				postContext: []string{"\t\treturn fmt.Errorf(\"decode rates: %w\", err)", "\t}"},
			},
			{"(*Scheduler).run", "payments/internal/jobs", "internal/jobs/scheduler.go", 48, "", nil, nil},
		},
	},
}

var goTxs = []txTemplate{
	{
		name:       "POST /v1/payments/authorize",
		op:         "http.server",
		durationMs: [2]int{180, 720},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT amount, currency, status FROM idempotency_keys WHERE key = $1", durationMs: [2]int{2, 8}},
			{op: "db.query", description: "INSERT INTO ledger_holds (account_id, amount, currency, expires_at) VALUES ($1, $2, $3, $4)", durationMs: [2]int{3, 12}},
			{op: "http.client", description: "POST https://api.stripe.com/v1/payment_intents", durationMs: [2]int{140, 620}},
			{op: "db.query", description: "INSERT INTO payments (id, order_id, amount, currency, provider_ref, status) VALUES ($1, $2, $3, $4, $5, 'authorized')", durationMs: [2]int{3, 10}},
			{op: "cache.set", description: "SET payment:{id}", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "POST /v1/payments/{id}/capture",
		op:         "http.server",
		durationMs: [2]int{120, 460},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, amount, provider_ref, status FROM payments WHERE id = $1 FOR UPDATE", durationMs: [2]int{2, 9}},
			{op: "http.client", description: "POST https://api.stripe.com/v1/payment_intents/{id}/capture", durationMs: [2]int{90, 380}},
			{op: "db.query", description: "UPDATE payments SET status = 'captured', captured_at = now() WHERE id = $1", durationMs: [2]int{2, 8}},
			{op: "queue.publish", description: "payments.captured", durationMs: [2]int{1, 4}},
		},
	},
	{
		name:       "GET /v1/payments/{id}",
		op:         "http.server",
		durationMs: [2]int{6, 40},
		platform:   "go",
		spans: []spanNode{
			{op: "cache.get", description: "GET payment:{id}", durationMs: [2]int{1, 3}},
			{op: "db.query", description: "SELECT id, order_id, amount, currency, status, captured_at FROM payments WHERE id = $1", durationMs: [2]int{2, 12}},
		},
	},
	{
		name:       "POST /v1/refunds",
		op:         "http.server",
		durationMs: [2]int{150, 520},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, amount, refunded, provider_ref FROM payments WHERE id = $1 FOR UPDATE", durationMs: [2]int{2, 9}},
			{op: "http.client", description: "POST https://api.stripe.com/v1/refunds", durationMs: [2]int{110, 430}},
			{op: "db.query", description: "INSERT INTO refunds (payment_id, amount, reason, provider_ref) VALUES ($1, $2, $3, $4)", durationMs: [2]int{2, 8}},
			{op: "queue.publish", description: "payments.refunded", durationMs: [2]int{1, 4}},
		},
	},
	{
		name:       "POST /webhooks/stripe",
		op:         "http.server",
		durationMs: [2]int{8, 45},
		platform:   "go",
		spans: []spanNode{
			{op: "webhook.verify", description: "verify Stripe-Signature", durationMs: [2]int{1, 3}},
			{op: "db.query", description: "INSERT INTO provider_events (id, type, payload) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING", durationMs: [2]int{2, 9}},
			{op: "queue.publish", description: "payments.provider_event", durationMs: [2]int{1, 4}},
		},
	},
	{
		name:       "ledger.reconcile",
		op:         "task",
		durationMs: [2]int{900, 4200},
		platform:   "go",
		spans: []spanNode{
			{op: "http.client", description: "GET https://api.stripe.com/v1/balance_transactions", durationMs: [2]int{180, 900}},
			{op: "db.query", description: "SELECT id, provider_ref, amount FROM payments WHERE captured_at >= $1 AND reconciled_at IS NULL", durationMs: [2]int{40, 260}},
			{op: "db.query", description: "UPDATE ledger_entries SET settled = true WHERE account_id = $1 AND id = ANY($2)", durationMs: [2]int{60, 600}},
			{op: "db.query", description: "UPDATE payments SET reconciled_at = now() WHERE id = ANY($1)", durationMs: [2]int{20, 140}},
		},
	},
	{
		name:       "fx.refresh",
		op:         "task",
		durationMs: [2]int{120, 600},
		platform:   "go",
		spans: []spanNode{
			{op: "http.client", description: "GET https://rates.example.com/v1/latest?base=USD", durationMs: [2]int{80, 520}},
			{op: "cache.set", description: "SET fx:rates", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "settlement.export",
		op:         "task",
		durationMs: [2]int{2200, 7800},
		platform:   "go",
		spans: []spanNode{
			{op: "db.query", description: "SELECT p.id, p.amount, p.currency, r.amount AS refunded FROM payments p LEFT JOIN refunds r ON r.payment_id = p.id WHERE p.captured_at::date = $1", durationMs: [2]int{300, 1800}},
			{op: "file.write", description: "write settlement CSV", durationMs: [2]int{200, 900}},
			{op: "http.client", description: "PUT https://storage.example.com/settlements/{date}.csv", durationMs: [2]int{150, 800}},
		},
	},
}

var goProject = projectDef{
	name:         "go",
	description:  "Go payment service (chi, pgx, Stripe, webhooks, ledger jobs)",
	releases:     goReleases,
	environments: defaultEnvironments,
	issues:       goIssues,
	issueCounts:  []int{18, 9, 4, 2, 6, 3},
	txs:          goTxs,
	txCounts:     []int{30, 22, 40, 8, 26, 6, 12, 3},
}

// =============================================================================
// Python project
// =============================================================================

var pythonReleases = []string{"0.9.0", "0.9.1", "1.0.0", "1.0.1"}

var pythonIssues = []issueTemplate{
	{
		excType:     "ValueError",
		excValue:    "invalid literal for int() with base 10: 'undefined'",
		level:       "error",
		platform:    "python",
		transaction: "POST /webhooks/supplier",
		stacktrace: []stackFrame{
			{
				function:    "parse_stock_update",
				module:      "suppliers.parser",
				filename:    "suppliers/parser.py",
				lineno:      77,
				preContext:  []string{"def parse_stock_update(raw: dict) -> StockUpdate:", "    sku = raw['sku'].strip().upper()"},
				contextLine: "    quantity = int(raw.get('quantity'))",
				postContext: []string{"    return StockUpdate(sku=sku, quantity=quantity)", ""},
			},
			{
				function:    "supplier_webhook",
				module:      "suppliers.views",
				filename:    "suppliers/views.py",
				lineno:      41,
				preContext:  []string{"@csrf_exempt", "def supplier_webhook(request):"},
				contextLine: "    update = parse_stock_update(json.loads(request.body))",
				postContext: []string{"    apply_stock_update.delay(update.sku, update.quantity)", "    return JsonResponse({'ok': True})"},
			},
			{"dispatch", "django.core.handlers.base", "django/core/handlers/base.py", 115, "", nil, nil},
			{"__call__", "django.core.handlers.wsgi", "django/core/handlers/wsgi.py", 140, "", nil, nil},
		},
	},
	{
		excType:     "RateLimitError",
		excValue:    "Error code: 429 - Rate limit reached for requests",
		level:       "error",
		platform:    "python",
		transaction: "celery: app.tasks.write_product_copy",
		stacktrace: []stackFrame{
			{"_handle_response", "openai._base_client", "openai/_base_client.py", 455, "", nil, nil},
			{"create", "openai.resources.chat.completions", "openai/resources/chat/completions.py", 88, "", nil, nil},
			{
				function:    "draft_description",
				module:      "app.copywriter",
				filename:    "app/copywriter.py",
				lineno:      31,
				preContext:  []string{"def draft_description(product: Product) -> str:", "    prompt = DESCRIPTION_PROMPT.format(name=product.name, specs=product.specs)"},
				contextLine: "    response = client.chat.completions.create(model=settings.COPY_MODEL, messages=[{\"role\": \"user\", \"content\": prompt}])",
				postContext: []string{"    return response.choices[0].message.content", ""},
			},
			{
				function:    "write_product_copy",
				module:      "app.tasks",
				filename:    "app/tasks.py",
				lineno:      88,
				preContext:  []string{"@shared_task(bind=True, max_retries=3)", "def write_product_copy(self, product_id: int):", "    product = Product.objects.get(pk=product_id)"},
				contextLine: "    product.description = draft_description(product)",
				postContext: []string{"    product.save(update_fields=['description'])"},
			},
			{"__call__", "celery.app.trace", "celery/app/trace.py", 760, "", nil, nil},
		},
	},
	{
		excType:     "ValidationError",
		excValue:    "{'email': ['Enter a valid email address.']}",
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
		excValue:    "2 validation errors for CreateItemRequest\nprice\n  Input should be greater than 0 [type=greater_than, input_value=0, input_type=int]\nsku\n  String should match pattern '^[A-Z0-9-]{6,16}$' [type=string_pattern_mismatch, input_value='tr-01', input_type=str]",
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
		excValue:    "(psycopg2.OperationalError) FATAL:  remaining connection slots are reserved for non-replication superuser connections",
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
		excValue:    "Can't retry app.tasks.send_invoice_pdf[c8f3b2d1-4e5a-4b6c-9d8e-1f2a3b4c5d6e] args:('INV-20482',) kwargs:{}",
		level:       "fatal",
		platform:    "python",
		transaction: "celery:app.tasks.send_invoice_pdf",
		handled:     new(false),
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
		name:       "GET /v1/recommendations/{customer_id}",
		op:         "http.server",
		durationMs: [2]int{20, 140},
		platform:   "python",
		spans: []spanNode{
			{op: "cache.get", description: "recs:{customer_id}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT product_id, quantity FROM order_items JOIN orders ON orders.id = order_items.order_id WHERE orders.customer_id = %s ORDER BY orders.created_at DESC LIMIT 50", durationMs: [2]int{6, 40}},
			{op: "model.predict", description: "rank candidates (implicit ALS)", durationMs: [2]int{8, 60}},
			{op: "cache.set", description: "recs:{customer_id} ttl=900", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "POST /api/v1/items",
		op:         "http.server",
		durationMs: [2]int{25, 110},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT categories.id, categories.name FROM categories WHERE categories.id = %s FOR SHARE", durationMs: [2]int{4, 15}},
			{op: "db.query", description: "INSERT INTO items (id, name, sku, price, category_id, created_at) VALUES (%s, %s, %s, %s, %s, NOW()) RETURNING id, created_at", durationMs: [2]int{5, 20}},
			{op: "cache.delete", description: "items:category:{category_id}", durationMs: [2]int{1, 4}},
		},
	},
	{
		name:       "celery: app.tasks.build_sales_report",
		op:         "queue.task",
		durationMs: [2]int{800, 5000},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT orders.id, orders.total, orders.currency, orders.created_at, customers.country FROM orders JOIN customers ON customers.id = orders.customer_id WHERE orders.created_at BETWEEN %s AND %s", durationMs: [2]int{200, 2000}},
			{op: "compute", description: "aggregate revenue by day and country", durationMs: [2]int{100, 800}},
			{op: "file.write", description: "render report PDF", durationMs: [2]int{300, 1500}},
			{op: "http.client", description: "PUT https://storage.example.com/reports/{report_id}.pdf", durationMs: [2]int{80, 400}},
			{op: "db.query", description: "UPDATE reports SET status = 'complete', file_url = %s, completed_at = NOW() WHERE id = %s", durationMs: [2]int{3, 12}},
		},
	},
	{
		name:       "POST /webhooks/supplier",
		op:         "http.server",
		durationMs: [2]int{8, 40},
		platform:   "python",
		spans: []spanNode{
			{op: "http.verify", description: "verify HMAC-SHA256 webhook signature", durationMs: [2]int{1, 3}},
			{op: "cache.get", description: "idempotency:{webhook_id}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "INSERT INTO supplier_events (id, supplier_id, event_type, payload, received_at) VALUES (%s, %s, %s, %s, NOW()) ON CONFLICT (id) DO NOTHING", durationMs: [2]int{3, 12}},
			{op: "queue.dispatch", description: "app.tasks.apply_stock_update", durationMs: [2]int{2, 6}},
		},
	},
	{
		name:       "GET /api/v1/search",
		op:         "http.server",
		durationMs: [2]int{30, 180},
		platform:   "python",
		spans: []spanNode{
			{op: "cache.get", description: "search:{query_hash}", durationMs: [2]int{1, 4}},
			{op: "db.query", description: "SELECT id, name, ts_rank(search_vector, query) AS rank FROM items, to_tsquery('english', %s) query WHERE search_vector @@ query ORDER BY rank DESC LIMIT 20", durationMs: [2]int{15, 80}},
			{op: "db.query", description: "SELECT item_id, url FROM item_images WHERE item_id = ANY(%s) AND is_primary = true", durationMs: [2]int{5, 20}},
			{op: "cache.set", description: "search:{query_hash} ttl=30", durationMs: [2]int{1, 3}},
		},
	},
	{
		name:       "celery: app.tasks.apply_stock_update",
		op:         "queue.task",
		durationMs: [2]int{30, 260},
		platform:   "python",
		spans: []spanNode{
			{op: "db.query", description: "SELECT id, stock_qty, reserved_qty FROM inventory WHERE sku = %s FOR UPDATE", durationMs: [2]int{3, 20}},
			{op: "db.query", description: "UPDATE inventory SET stock_qty = %s, updated_at = NOW() WHERE sku = %s", durationMs: [2]int{3, 14}},
			{op: "cache.delete", description: "product:{sku}:availability", durationMs: [2]int{1, 3}},
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

// The shop's storefront: a Vue single-page app for browsing, cart and checkout.
var jsIssues = []issueTemplate{
	{
		excType:     "ChunkLoadError",
		excValue:    "Loading chunk 482 failed.",
		level:       "error",
		platform:    "javascript",
		transaction: "/products/:slug",
		handled:     new(false),
		hasUser:     true,
		stacktrace: []stackFrame{
			{"requireEnsure", "webpack/runtime", "assets/runtime.4f2a9c.js", 1, "", nil, nil},
			{
				function:    "loadProductGallery",
				module:      "router/routes",
				filename:    "src/router/routes.ts",
				lineno:      27,
				preContext:  []string{"  {", "    path: '/products/:slug',"},
				contextLine: "    component: () => import('@/views/ProductView.vue'),",
				postContext: []string{"    meta: { layout: 'shop' },", "  },"},
			},
			{"resolveAsyncComponents", "vue-router", "node_modules/vue-router/dist/vue-router.mjs", 2041, "", nil, nil},
		},
	},
	{
		excType:     "TypeError",
		excValue:    "Cannot read properties of undefined (reading 'price')",
		level:       "error",
		platform:    "javascript",
		transaction: "/cart",
		handled:     new(false),
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "lineTotal",
				module:      "cart/totals",
				filename:    "src/cart/totals.ts",
				lineno:      14,
				preContext:  []string{"export function lineTotal(line: CartLine) {", "  // Variants removed from the catalog leave the line without one."},
				contextLine: "  return line.variant.price * line.quantity",
				postContext: []string{"}"},
			},
			{
				function:    "CartSummary.setup",
				module:      "components/cart",
				filename:    "src/components/cart/CartSummary.vue",
				lineno:      22,
				preContext:  []string{"const cart = useCart()"},
				contextLine: "const subtotal = computed(() => cart.lines.reduce((sum, line) => sum + lineTotal(line), 0))",
				postContext: []string{"const shipping = useShippingEstimate(subtotal)"},
			},
			{"callWithErrorHandling", "runtime-core", "node_modules/@vue/runtime-core/dist/runtime-core.esm-bundler.js", 199, "", nil, nil},
		},
	},
	{
		excType:     "TypeError",
		excValue:    "Failed to fetch",
		level:       "error",
		platform:    "javascript",
		transaction: "/checkout",
		hasUser:     true,
		stacktrace: []stackFrame{
			{
				function:    "request",
				module:      "api/client",
				filename:    "src/api/client.ts",
				lineno:      31,
				preContext:  []string{"export async function request<T>(path: string, init?: RequestInit): Promise<T> {"},
				contextLine: "  const res = await fetch(`${API_BASE}${path}`, { credentials: 'include', ...init })",
				postContext: []string{"  if (!res.ok) throw new ApiError(res.status, await res.text())", "  return res.json()"},
			},
			{
				function:    "submitOrder",
				module:      "checkout/submit",
				filename:    "src/checkout/submit.ts",
				lineno:      48,
				preContext:  []string{"  const payment = await confirmCardPayment(form.card)"},
				contextLine: "  return request<Order>('/api/checkout', { method: 'POST', body: JSON.stringify(payload) })",
				postContext: []string{"}"},
			},
		},
	},
	{
		excType:     "Error",
		excValue:    "Hydration completed but contains mismatches.",
		level:       "warning",
		platform:    "javascript",
		transaction: "/",
		stacktrace: []stackFrame{
			{"hydrate", "runtime-core", "node_modules/@vue/runtime-core/dist/runtime-core.esm-bundler.js", 1630, "", nil, nil},
			{
				function:    "mountApp",
				module:      "main",
				filename:    "src/main.ts",
				lineno:      19,
				preContext:  []string{"const app = createSSRApp(App)", "app.use(router).use(pinia)"},
				contextLine: "app.mount('#app', true)",
				postContext: []string{""},
			},
		},
	},
	{
		excType:     "AbortError",
		excValue:    "The user aborted a request.",
		level:       "info",
		platform:    "javascript",
		transaction: "/search",
		stacktrace: []stackFrame{
			{
				function:    "useSearch.run",
				module:      "search/useSearch",
				filename:    "src/search/useSearch.ts",
				lineno:      36,
				preContext:  []string{"  controller?.abort()", "  controller = new AbortController()"},
				contextLine: "  results.value = await request<SearchResult[]>(`/api/search?q=${encodeURIComponent(q)}`, { signal: controller.signal })",
				postContext: []string{""},
			},
		},
	},
}

var jsTxs = []txTemplate{
	{
		name:       "/",
		op:         "pageload",
		durationMs: [2]int{420, 1600},
		platform:   "javascript",
		spans: []spanNode{
			{op: "resource.script", description: "/assets/index.8c1f3e.js", durationMs: [2]int{60, 260}},
			{op: "resource.css", description: "/assets/index.2b7d41.css", durationMs: [2]int{20, 90}, concurrent: true},
			{op: "http.client", description: "GET /api/catalog/featured", durationMs: [2]int{60, 240}},
			{op: "resource.img", description: "/images/hero/autumn-collection.webp", durationMs: [2]int{90, 520}, concurrent: true},
			{op: "ui.render", description: "hydrate app", durationMs: [2]int{30, 120}},
		},
	},
	{
		name:       "/products/:slug",
		op:         "pageload",
		durationMs: [2]int{380, 1500},
		platform:   "javascript",
		spans: []spanNode{
			{op: "resource.script", description: "/assets/ProductView.91ad2c.js", durationMs: [2]int{40, 200}},
			{op: "http.client", description: "GET /api/products/{slug}", durationMs: [2]int{50, 220}},
			{op: "http.client", description: "GET /api/products/{slug}/reviews", durationMs: [2]int{40, 180}, concurrent: true},
			{op: "resource.img", description: "/images/products/{slug}/1.webp", durationMs: [2]int{80, 480}},
			{op: "ui.render", description: "render product gallery", durationMs: [2]int{15, 70}},
		},
	},
	{
		name:       "/collections/:handle",
		op:         "navigation",
		durationMs: [2]int{120, 600},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/collections/{handle}?page=1", durationMs: [2]int{50, 260}},
			{op: "resource.img", description: "/images/products/*/thumb.webp", durationMs: [2]int{40, 300}},
			{op: "ui.render", description: "render product grid", durationMs: [2]int{12, 60}},
		},
	},
	{
		name:       "/cart",
		op:         "navigation",
		durationMs: [2]int{90, 420},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/cart", durationMs: [2]int{30, 160}},
			{op: "http.client", description: "GET /api/shipping/estimate", durationMs: [2]int{40, 220}},
			{op: "ui.render", description: "render cart", durationMs: [2]int{8, 40}},
		},
	},
	{
		name:       "/checkout",
		op:         "pageload",
		durationMs: [2]int{520, 2100},
		platform:   "javascript",
		spans: []spanNode{
			{op: "resource.script", description: "/assets/CheckoutView.d03b7e.js", durationMs: [2]int{50, 240}},
			{op: "resource.script", description: "https://js.stripe.com/v3/", durationMs: [2]int{120, 650}, concurrent: true},
			{op: "http.client", description: "GET /api/cart", durationMs: [2]int{30, 160}},
			{op: "http.client", description: "GET /api/checkout/session", durationMs: [2]int{40, 200}},
			{op: "ui.render", description: "mount payment form", durationMs: [2]int{30, 140}},
		},
	},
	{
		name:       "/account/orders",
		op:         "navigation",
		durationMs: [2]int{110, 520},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/orders?limit=20", durationMs: [2]int{40, 260}},
			{op: "ui.render", description: "render order history", durationMs: [2]int{10, 50}},
		},
	},
	{
		name:       "/search",
		op:         "navigation",
		durationMs: [2]int{80, 380},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "GET /api/search?q={query}", durationMs: [2]int{40, 260}},
			{op: "ui.render", description: "render results", durationMs: [2]int{8, 40}},
		},
	},
	{
		name:       "add to cart",
		op:         "ui.action.click",
		durationMs: [2]int{60, 320},
		platform:   "javascript",
		spans: []spanNode{
			{op: "http.client", description: "POST /api/cart/lines", durationMs: [2]int{40, 260}},
			{op: "ui.render", description: "update cart badge", durationMs: [2]int{2, 12}},
		},
	},
}

var jsProject = projectDef{
	name:         "javascript",
	description:  "Storefront single-page app (Vue 3, browser errors, Web Vitals)",
	releases:     jsReleases,
	environments: defaultEnvironments,
	issues:       jsIssues,
	issueCounts:  []int{22, 12, 9, 6, 3},
	txs:          jsTxs,
	txCounts:     []int{40, 34, 26, 22, 18, 10, 14, 24},
}

// =============================================================================
// Registry
// =============================================================================

var projectRegistry map[string]projectDef

func init() {
	mixed := projectDef{
		name:         "mixed",
		description:  "All project types combined (Laravel, Go, Python, JavaScript)",
		releases:     []string{"1.4.0", "1.4.1", "1.4.2", "1.5.0"},
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
		"release":     releaseAt(ts, releases),
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
		evt["user"] = sentryUserMap(randomChoice(seedUsers))
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
		span := map[string]any{
			"span_id":         spanID,
			"parent_span_id":  parentSpanID,
			"op":              node.op,
			"description":     node.description,
			"start_timestamp": start.Format(time.RFC3339Nano),
			"timestamp":       end.Format(time.RFC3339Nano),
			"status":          "ok",
			"data":            spanData(node.op, node.description),
		}
		if strings.HasPrefix(node.op, "queue") || strings.HasPrefix(node.op, "task") || strings.HasPrefix(node.op, "job") || strings.HasPrefix(node.op, "celery") {
			if rand.Intn(20) == 0 {
				span["status"] = "internal_error"
				span["data"].(map[string]any)["messaging.message.retry.count"] = 2
			} //nolint:gosec
		}
		out = append(out, span)

		if len(node.children) > 0 {
			children := buildSpanNodes(node.children, spanID, start)
			for _, child := range children {
				childEnd, _ := time.Parse(time.RFC3339Nano, child["timestamp"].(string))
				if childEnd.After(end) {
					end = childEnd
				}
			}
			span["timestamp"] = end.Format(time.RFC3339Nano)
			out = append(out, children...)
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

	traceID := newEventID()
	rootSpanID := newEventID()[:16]

	spans := buildSpanNodes(tmpl.spans, rootSpanID, ts)

	status := "ok"
	if rand.Float64() < 0.05 { // 5% chance of errored transaction
		status = "internal_error"
	}

	tx := map[string]any{
		"transaction":     tmpl.name,
		"start_timestamp": ts.Format(time.RFC3339Nano),
		"timestamp":       end.Format(time.RFC3339Nano),
		"platform":        tmpl.platform,
		"environment":     randomChoice(envs),
		"release":         releaseAt(ts, releases),
		"contexts": map[string]any{
			"trace": map[string]any{
				"trace_id": traceID,
				"span_id":  rootSpanID,
				"op":       tmpl.op,
				"status":   status,
			},
		},
		"spans": fitSpans(spans, ts, end),
		"tags":  buildTags(tmpl.platform),
	}

	if tmpl.op == "pageload" || tmpl.op == "navigation" {
		measurements := seedWebVitals()
		tx["measurements"] = measurements
		lcp := measurements["lcp"].(map[string]any)["value"].(float64)
		if lcp > totalMs {
			tx["timestamp"] = ts.Add(time.Duration(lcp * float64(time.Millisecond))).Format(time.RFC3339Nano)
		}
	}

	// Always attach a user so the User lens has traces and page loads to show.
	tx["user"] = sentryUserMap(randomChoice(seedUsers))

	return tx
}

// seedWebVitals generates realistic Web Vitals with a ~84/11/5 good/ni/poor distribution.
func seedWebVitals() map[string]any {
	vitalMs := func(good, poor int) float64 { //nolint:gosec
		r := rand.Float64()
		switch {
		case r < 0.84:
			return jitterMs(good/4, good) // good range: 25%–100% of threshold
		case r < 0.95:
			return jitterMs(good, poor) // needs-improvement
		default:
			return jitterMs(poor, poor+poor/2) // poor
		}
	}
	clsVal := func() float64 { //nolint:gosec
		r := rand.Float64()
		switch {
		case r < 0.84:
			return rand.Float64() * 0.1 // good: 0–0.1
		case r < 0.95:
			return 0.1 + rand.Float64()*0.15 // needs-improvement: 0.1–0.25
		default:
			return 0.25 + rand.Float64()*0.2 // poor: 0.25–0.45
		}
	}
	lcp := vitalMs(2500, 4000)
	fcp := min(vitalMs(1800, 3000), lcp)
	return map[string]any{
		"lcp":  map[string]any{"value": lcp, "unit": "millisecond"},
		"fcp":  map[string]any{"value": fcp, "unit": "millisecond"},
		"inp":  map[string]any{"value": vitalMs(200, 500), "unit": "millisecond"},
		"ttfb": map[string]any{"value": min(vitalMs(800, 1800), fcp), "unit": "millisecond"},
		"cls":  map[string]any{"value": clsVal(), "unit": ""},
	}
}

// =============================================================================
// Main
// =============================================================================

// =============================================================================
// Uptime monitor seeding
// =============================================================================

// seedUptimeMonitors writes the uptime fixtures. projectFor maps each
// fixture's project key ("storefront", "shop-api", "payments") to a project.
func seedUptimeMonitors(pool *pgxpool.Pool, projectFor func(key string) string, prefix string) (failed int) {
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
		project       string
		noChecks      bool // skip all check history (state remains "unknown")
	}

	sp := func(v string) *string { return &v }
	ip := func(v int) *int { return &v }

	specs := []monitorSpec{
		{
			name: "API health check", project: "shop-api", url: "https://api.example.com/health",
			method: "GET", intervalSecs: 60, expectedCodes: "200",
			monitorStatus: "active", description: "mostly up: small outage 3 days ago and 10 days ago",
			baseRespMs: 85,
			outages: []outageWindow{
				{startAgo: 73 * time.Hour, endAgo: 71 * time.Hour, errMsg: "upstream timeout: no response within 10s"},
				{startAgo: 240 * time.Hour, endAgo: 236 * time.Hour, errMsg: "connection refused: ECONNREFUSED"},
			},
		},
		{
			name: "Marketing site", project: "storefront", url: "https://www.example.com",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			bodyContains:  sp("Welcome"),
			monitorStatus: "active", description: "100% uptime over 7 days",
			baseRespMs: 125,
		},
		{
			name: "Payment gateway", project: "payments", url: "https://pay.example.com/ping",
			method: "HEAD", intervalSecs: 60, expectedCodes: "200",
			monitorStatus: "active", description: "currently down: 503 errors",
			baseRespMs: 95,
			outages: []outageWindow{
				{startAgo: 6 * time.Hour, endAgo: 5 * time.Hour, errMsg: "503 Service Unavailable", code: 503},
			},
			forceDown: true,
		},
		{
			name: "Admin panel", project: "shop-api", url: "https://admin.example.com",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			monitorStatus: "active", description: "up with two planned maintenance windows",
			baseRespMs: 210,
			outages: []outageWindow{
				{startAgo: 26 * time.Hour, endAgo: 24 * time.Hour, errMsg: "connection refused: maintenance window"},
				{startAgo: 146 * time.Hour, endAgo: 144 * time.Hour, errMsg: "connection refused: maintenance window"},
			},
		},
		{
			name: "Staging environment", project: "storefront", url: "https://staging.example.com/health",
			method: "GET", intervalSecs: 300, expectedCodes: "200",
			monitorStatus: "paused", description: "paused: 30 checks recorded before pause",
			baseRespMs: 155,
		},
		{
			name: "CDN edge node", project: "storefront", url: "https://cdn.example.com/assets/app.js",
			method: "HEAD", intervalSecs: 300, expectedCodes: "200,304",
			monitorStatus: "active", description: "no history yet: state unknown",
			noChecks: true,
		},
	}

	fmt.Println("\n=== Uptime Monitors ===")

	for _, spec := range specs {
		var monID string
		err := pool.QueryRow(ctx, `
			INSERT INTO uptime_monitors
				(project_id, name, url, method, interval_secs, timeout_secs,
				 expected_codes, body_contains, status, next_check_at)
			VALUES ($1,$2,$3,$4,$5,10,$6,$7,$8, NOW() + INTERVAL '30 days')
			RETURNING id`,
			projectFor(spec.project), prefix+spec.name, spec.url, spec.method, spec.intervalSecs,
			spec.expectedCodes, spec.bodyContains, spec.monitorStatus,
		).Scan(&monID)
		if err != nil {
			failed++
			fmt.Printf("  FAIL  create monitor %q: %v\n", spec.name, err)
			continue
		}

		if spec.noChecks {
			fmt.Printf("  OK    %q: %s\n", spec.name, spec.description)
			continue
		}

		// Generate check history.
		//
		// Phase 1: historical bulk: 168 checks at 1-hour spacing over the past
		// 7 days. This is enough to populate the 24h / 7d uptime stats and the
		// 20-check recent-checks strip.
		// Paused monitors get 30 checks at 1-hour spacing (~1.25 days).
		//
		// Phase 2: fresh probe: one additional check placed at ~intervalSecs ago
		// so the monitor looks like it was just polled.
		now := time.Now().UTC()
		numHistorical := 168
		historyDuration := 7 * 24 * time.Hour
		if spec.monitorStatus == "paused" {
			numHistorical = 30
			historyDuration = 30 * time.Hour
		}
		spacing := historyDuration / time.Duration(numHistorical)

		var lastError *string
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
				failed++
				fmt.Printf("    FAIL  check at %s: %v\n", checkedAt.Format(time.RFC3339), ierr)
				return
			}
			totalChecks++
			lastError = errMsg
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
				r := max(spec.baseRespMs+rand.Intn(40)-20, 10) //nolint:gosec
				insertCheck("up", &c, &r, nil, checkedAt)
			}
		}

		// Phase 2: fresh probe to make the monitor look live.
		if spec.monitorStatus == "active" {
			freshAgo := min(time.Duration(spec.intervalSecs)*time.Second, 2*time.Minute)
			freshAt := now.Add(-freshAgo)
			if spec.forceDown {
				code := 503
				errStr := "503 Service Unavailable"
				insertCheck("down", &code, nil, &errStr, freshAt)
			} else {
				c := 200
				r := max(spec.baseRespMs+rand.Intn(20)-10, 10) //nolint:gosec
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

		nextCheckAt := now.Add(30 * 24 * time.Hour)
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
				last_response_ms     = $8,
				last_error           = $9,
				went_down_at         = CASE WHEN $2='down' THEN $4::timestamptz - INTERVAL '3 hours' ELSE NULL END
			WHERE id = $1`,
			monID, finalState, consecutiveFailures,
			lastCheckedAt, lastOkAtPtr, nextCheckAt,
			lastCode, lastRespMs, lastError,
		)
		if err != nil {
			failed++
			fmt.Printf("    FAIL  update state: %v\n", err)
		}

		fmt.Printf("  OK    %q: %s (state=%s, %d checks)\n",
			spec.name, spec.description, finalState, totalChecks)
	}
	return failed
}

func seedCronMonitors(target dsn, pool *pgxpool.Pool, prefix string) (failed int) {
	ctx := context.Background()
	now := time.Now().UTC()
	// every is the schedule's spacing, so the run history fills the 20-run
	// strip at the rhythm the schedule promises.
	specs := []struct {
		name, schedule, status string
		every                  time.Duration
	}{
		{"Daily backup", "0 2 * * *", "ok", 24 * time.Hour},
		{"Payment processor", "*/15 * * * *", "error", 15 * time.Minute},
		{"Queue worker heartbeat", "*/5 * * * *", "in_progress", 5 * time.Minute},
		{"Weekly report", "0 9 * * 1", "unknown", 7 * 24 * time.Hour},
		{"Hourly inventory sync", "0 * * * *", "ok", time.Hour},
		{"Email digest", "0 8 * * *", "error", 24 * time.Hour},
		{"Thumbnail generator", "*/30 * * * *", "ok", 30 * time.Minute},
	}
	fmt.Println("\n=== Cron Monitors ===")
	for _, spec := range specs {
		err := func() error {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			var id string
			err = tx.QueryRow(ctx, `INSERT INTO cron_monitors(project_id,name,schedule,grace_period_secs,next_expected_at)
                VALUES($1,$2,$3,300,$4) RETURNING id`, target.projectID, prefix+spec.name, spec.schedule, now.Add(30*24*time.Hour)).Scan(&id)
			if err != nil {
				return err
			}
			if spec.status != "unknown" {
				for i := 19; i >= 0; i-- {
					status := "ok"
					if i == 0 {
						status = spec.status
					} else if (i == 1 || i == 7) && spec.status == "error" {
						status = "error"
					}
					at := now.Add(-time.Duration(i)*spec.every - time.Minute)
					var finished *time.Time
					var duration *int
					if status != "in_progress" {
						end := at.Add(2500 * time.Millisecond)
						finished = &end
						ms := 2500
						duration = &ms
					}
					if _, err := tx.Exec(ctx, `INSERT INTO cron_checkins(monitor_id,status,duration_ms,environment,started_at,finished_at,received_at)
                        VALUES($1,$2,$3,'production',$4,$5,$4)`, id, status, duration, at, finished); err != nil {
						return err
					}
				}
				_, err = tx.Exec(ctx, `UPDATE cron_monitors SET last_checkin_status=$2,last_checkin_at=$3,is_running=$4,last_ok_at=CASE WHEN $2='ok' THEN $3::timestamptz ELSE $5::timestamptz END WHERE id=$1`, id, spec.status, now.Add(-time.Minute), spec.status == "in_progress", now.Add(-2*spec.every-time.Minute))
				if err != nil {
					return err
				}
			}
			return tx.Commit(ctx)
		}()
		if err != nil {
			failed++
			fmt.Printf("  FAIL  %s: %v\n", spec.name, err)
		} else {
			fmt.Printf("  OK    %s (%s)\n", spec.name, spec.status)
		}
	}
	return failed
}

var canonicalSections = []string{"issues", "transactions", "profiles", "logs", "monitors", "alerts", "workflow"}

// sectionAliases maps performance sub-view names to their canonical section.
var sectionAliases = map[string]string{
	"queries": "transactions",
	"caches":  "transactions",
	"jobs":    "transactions",
	"browser": "transactions",
}

// profiles is deliberately not an alias for transactions: it emits its own
// profiled transactions so a profile always has something to hang off, and so
// --seed=transactions alone stays as cheap as it was.

func parseSeedSections(s string) (map[string]bool, error) {
	out := make(map[string]bool)
	if strings.TrimSpace(s) == "all" {
		for _, sec := range canonicalSections {
			out[sec] = true
		}
		return out, nil
	}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		if canonical, ok := sectionAliases[part]; ok {
			out[canonical] = true
			continue
		}
		valid := slices.Contains(canonicalSections, part)
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
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
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
			for line := range strings.SplitSeq(string(data), "\n") {
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
	seedSections := flag.String("seed", "all", `comma-separated sections: issues, transactions, profiles, logs, monitors, alerts, workflow (or "all")`)
	scale := flag.Int("scale", 1, "multiply the volume of events, transactions and logs")
	showcase := flag.Bool("showcase", false, "seed a complete shop across four projects with a team, for screenshots (requires --db; takes the server URL instead of a DSN)")
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
	if strings.TrimSpace(*seedSections) != "all" && (seed["monitors"] || seed["alerts"]) && resolvedDB == "" {
		fmt.Fprintf(os.Stderr, "error: --db or DATABASE_URL in .env is required to seed monitors or alerts\n")
		os.Exit(1)
	}

	if *showcase {
		if flag.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "Usage: %s --showcase [--scale=N] [--db=POSTGRES_URL] <SERVER_URL>\n", os.Args[0])
			os.Exit(1)
		}
		os.Exit(runShowcase(flag.Arg(0), resolvedDB, max(1, *scale)))
	}

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--type=TYPE] [--seed=SECTIONS] [--db=POSTGRES_URL] <DSN>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  DSN format:  http://PUBLIC_KEY@HOST/PROJECT_ID\n")
		fmt.Fprintf(os.Stderr, "  --list       show available project types\n")
		fmt.Fprintf(os.Stderr, "  --seed       issues, transactions, profiles, logs, monitors, alerts, workflow (default: all)\n")
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

	if resolvedDB != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := pgxpool.New(ctx, resolvedDB)
		if err == nil {
			var matches bool
			err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id::text=$1 AND public_key=$2)`, target.projectID, target.publicKey).Scan(&matches)
			if err == nil && !matches {
				err = fmt.Errorf("database does not contain the project and key from the DSN")
			}
			if err == nil && (seed["issues"] || seed["transactions"] || seed["profiles"] || seed["workflow"]) {
				err = seedReleases(ctx, pool, target.projectID, def.releases)
			}
			pool.Close()
		}
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "database preflight: %v\n", err)
			os.Exit(1)
		}
	}

	res := seedProject(target, def, seed, resolvedDB, seedOptions{scale: max(1, *scale), prefix: "Demo: "})
	printSummary(res, seed)
	if res.failed > 0 {
		os.Exit(1)
	}
}

// seedOptions tune one project's run. The zero value matches the original
// single-project seed apart from the volume, which scale sets.
type seedOptions struct {
	scale int // multiplies events, transactions and logs
	// prefix is put before monitor and alert rule names, so fixtures added to
	// a real instance stand out ("Demo: ").
	prefix string
	// profiles limits the profile section to "php" or "python" formats; empty
	// seeds both.
	profiles string
	// spread staggers when each issue was last seen, quietest issues longest
	// ago, so a list does not show every issue as seen minutes ago.
	spread bool
	// showcaseIssue repeats the linked checkout failure this many extra times,
	// for different customers, so its issue has a real history.
	showcaseIssue int
}

// seedResult is what one project's seeding run delivered.
type seedResult struct {
	sent, failed  int
	profiledNames []string
}

func printSummary(res seedResult, seed map[string]bool) {
	fmt.Printf("\nDone. %d envelopes sent, %d failures.\n", res.sent, res.failed)
	fmt.Println("Screenshot tour: Dashboard, Issues, Performance (transactions, queries, caches, jobs, browser), Releases, Logs, and Monitors. Use the 24h window; use 7d for the deployment history.")
	if seed["workflow"] {
		fmt.Println("Open Checkout payment authorization and PaymentGatewayTimeout to explore linked traces, logs, and the same customer. With --db, the workflow also includes triage states and comments when an existing operator account is available.")
	}
	if seed["monitors"] || seed["alerts"] {
		fmt.Println("Seeded monitors defer automatic checks for 30 days. Seeded alert rules are disabled and have no pending deliveries.")
	}
	if len(res.profiledNames) > 0 {
		fmt.Println("\nTransactions with a flame graph (Performance → Transactions):")
		for _, n := range res.profiledNames {
			fmt.Printf("  %s\n", n)
		}
	}
}

// seedProject delivers the selected sections of one project definition to
// one project. With a database URL it also writes the database fixtures.
func seedProject(target dsn, def projectDef, seed map[string]bool, resolvedDB string, opts seedOptions) seedResult {
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

	// deliverItems sends several items in one envelope, which is what an SDK
	// does when a profile accompanies its transaction.
	// Filled in by the profiles section so the summary can point at the
	// transactions that actually have a flame graph.
	var profiledNames []string
	var workflowIDs []string

	deliverItems := func(label string, items []envelopeItem) {
		sema <- struct{}{}
		deliverWg.Add(1)
		go func() {
			defer deliverWg.Done()
			defer func() { <-sema }()
			envelope := buildEnvelope(items)
			retries := 0
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
					retries++
					if retries > 5 {
						failed++
						fmt.Printf("  FAIL  %s: rate limit retry budget exhausted\n", label)
						mu.Unlock()
						return
					}
					retryWait = min(retryWait, 2*time.Minute)
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
					if opts.scale == 1 {
						fmt.Printf("  OK    %s\n", label)
					}
					sent++
				}
				mu.Unlock()
				return
			}
		}()
	}

	deliver := func(label string, payload any, typ string) {
		deliverItems(label, []envelopeItem{{typ: typ, payload: payload}})
	}

	if seed["issues"] {
		fmt.Println("=== Issues ===")
		// The server keeps the first event it processes as an issue's first
		// seen time and release, so each issue's oldest event goes out, and is
		// grouped, before the rest of its history.
		type plannedEvent struct {
			tmpl issueTemplate
			ts   time.Time
		}
		var oldest, rest []plannedEvent
		for i, tmpl := range def.issues {
			count := 5
			if i < len(def.issueCounts) {
				count = def.issueCounts[i]
			}
			count *= opts.scale
			offset := issueOffset(i, opts.spread)
			events := make([]plannedEvent, 0, count+1)
			// With spread, each project's last issue is new today: its whole
			// history falls inside the last ten hours.
			fresh := opts.spread && i == len(def.issues)-1
			for j := 0; j < count; j++ {
				at := showcaseTime(j, count).Add(-offset)
				if fresh {
					at = trafficBiasedTime(10 * time.Hour)
				}
				events = append(events, plannedEvent{tmpl, at})
			}
			if opts.spread && i < 3 || !opts.spread && i%2 == 1 {
				ago := time.Duration(rand.Intn(10*60)) * time.Second //nolint:gosec
				events = append(events, plannedEvent{tmpl, time.Now().UTC().Add(-ago)})
			}
			first := 0
			for j, e := range events {
				if e.ts.Before(events[first].ts) {
					first = j
				}
			}
			oldest = append(oldest, events[first])
			rest = append(rest, append(events[:first:first], events[first+1:]...)...)
		}
		send := func(events []plannedEvent) {
			for _, e := range events {
				evt := buildErrorEvent(e.tmpl, e.ts, def.releases, def.environments)
				deliver(fmt.Sprintf("[%s] %s: %s", e.tmpl.platform, e.tmpl.excType, truncate(e.tmpl.excValue, 60)), evt, "event")
			}
		}
		send(oldest)
		deliverWg.Wait()
		time.Sleep(groupingDelay)
		send(rest)
	}

	if seed["transactions"] {
		fmt.Println("\n=== Transactions ===")
		for i, tmpl := range def.txs {
			count := 5
			if i < len(def.txCounts) {
				count = def.txCounts[i]
			}
			count *= opts.scale
			for j := 0; j < count; j++ {
				ts := showcaseTime(j, count)
				tx := buildTransaction(tmpl, ts, def.releases, def.environments)
				label := fmt.Sprintf("[%s] %s", tmpl.platform, tmpl.name)
				deliver(label, tx, "transaction")
			}
		}

		fmt.Println("\n=== Fresh transactions (last 5 min) ===")
		freshCount := len(def.txs)
		for _, tmpl := range def.txs[:freshCount] {
			ts := time.Now().UTC().Add(-time.Duration(30+rand.Intn(4*60)) * time.Second) //nolint:gosec
			tx := buildTransaction(tmpl, ts, def.releases, def.environments)
			tx["environment"] = "production"
			label := fmt.Sprintf("[fresh] [%s] %s", tmpl.platform, tmpl.name)
			deliver(label, tx, "transaction")
		}
	}

	if seed["profiles"] {
		phpProfiles, pyProfiles := laravelProfiles, pythonProfiles
		switch opts.profiles {
		case "php":
			pyProfiles = nil
		case "python":
			phpProfiles = nil
		}
		fmt.Println("\n=== Profiled transactions (v1, PHP style) ===")
		for _, tmpl := range phpProfiles {
			for i := 0; i < 6; i++ {
				ts := time.Now().UTC().Add(-profileAge())
				tx, profile := buildV1Profile(tmpl, ts, def.releases, def.environments)
				// One envelope carrying both, exactly as the SDK sends it.
				label := fmt.Sprintf("[profile v1] [php] %s", tmpl.txName)
				deliverItems(label, []envelopeItem{
					{typ: "transaction", payload: tx},
					{typ: "profile", payload: profile},
				})
			}
		}

		fmt.Println("\n=== Profiled transactions (v2, continuous) ===")
		for _, tmpl := range pyProfiles {
			for i := 0; i < 3; i++ {
				start := time.Now().UTC().Add(-profileAge())
				chunk, txs := buildV2Session(tmpl, start, 3, def.releases, def.environments)
				// The chunk flushes on the profiler's own schedule, separately
				// from the transactions it happens to cover.
				deliverItems(fmt.Sprintf("[profile v2] [python] chunk for %s", tmpl.txName),
					[]envelopeItem{{typ: "profile_chunk", payload: chunk}})
				for _, tx := range txs {
					deliverItems(fmt.Sprintf("[profile v2] [python] %s", tmpl.txName),
						[]envelopeItem{{typ: "transaction", payload: tx}})
				}
			}
		}

		// These names exist nowhere else in the seed data, so anything on this
		// list is guaranteed to have a flame graph when you open it.
		profiledNames = nil
		for _, tmpl := range phpProfiles {
			profiledNames = append(profiledNames, tmpl.txName+"  (v1, transaction-based)")
		}
		for _, tmpl := range pyProfiles {
			profiledNames = append(profiledNames, tmpl.txName+"  (v2, continuous)")
		}
	}

	if seed["logs"] {
		fmt.Println("\n=== Logs ===")
		logBatches := buildLogBatches(def.releases, def.environments, opts.scale)
		for i, batch := range logBatches {
			label := fmt.Sprintf("[logs] batch %d (%d records)", i+1, len(batch))
			deliver(label, batch, "log")
		}

		fmt.Println("\n=== Fresh logs (last 2 min) ===")
		freshLogs := buildFreshLogs(def.releases, def.environments)
		deliver("[logs] fresh batch", freshLogs, "log")
	}

	if seed["workflow"] {
		fmt.Println("\n=== Linked investigations ===")
		if opts.showcaseIssue > 0 {
			// Earlier occurrences of the checkout failure, oldest first so the
			// issue starts at the first of them.
			customers := slices.DeleteFunc(slices.Clone(seedUsers), func(u seedUser) bool { return u.email == "" })
			for k := opts.showcaseIssue; k > 0; k-- {
				at := time.Now().UTC().Add(-time.Duration(k) * 6 * time.Hour / time.Duration(opts.showcaseIssue)).Add(-time.Duration(rand.Intn(300)) * time.Second) //nolint:gosec
				items, _ := buildInvestigationAt(def, 0, at, customers[k%len(customers)])
				deliverItems("[workflow] earlier checkout failure", items)
				if k == opts.showcaseIssue {
					deliverWg.Wait()
					time.Sleep(groupingDelay)
				}
			}
		}
		for i := range 4 {
			items, eventID := buildInvestigation(def, i)
			workflowIDs = append(workflowIDs, eventID)
			deliverItems("[workflow] linked error, transaction and logs", items)
		}
	}

	deliverWg.Wait()

	if seed["monitors"] {
		if resolvedDB != "" {
			pool, err := pgxpool.New(context.Background(), resolvedDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect to DB: %v\n", err)
				return seedResult{sent: sent, failed: failed + 1}
			}
			defer pool.Close()
			failed += seedCronMonitors(target, pool, opts.prefix)
			failed += seedUptimeMonitors(pool, func(string) string { return target.projectID }, opts.prefix)
		} else {
			fmt.Println("\n(skip monitors - set DATABASE_URL in .env or pass --db=POSTGRES_URL)")
		}
	}

	if len(workflowIDs) > 0 && resolvedDB != "" {
		pool, err := pgxpool.New(context.Background(), resolvedDB)
		if err == nil {
			err = seedWorkflow(context.Background(), pool, target.projectID, workflowIDs)
			pool.Close()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflow: %v\n", err)
			failed++
		}
	}

	if seed["alerts"] {
		if resolvedDB != "" {
			pool, err := pgxpool.New(context.Background(), resolvedDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect to DB: %v\n", err)
				return seedResult{sent: sent, failed: failed + 1}
			}
			defer pool.Close()
			failed += seedAlertRules(pool, target.projectID, opts.prefix)
		} else {
			fmt.Println("\n(skip alerts: set DATABASE_URL in .env or pass --db=POSTGRES_URL)")
		}
	}

	return seedResult{sent: sent, failed: failed, profiledNames: profiledNames}
}

// =============================================================================
// Alert rule seeding
// =============================================================================

func seedAlertRules(pool *pgxpool.Pool, projectID, prefix string) (failed int) {
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
		filterSearch *string
		lastFiredAgo *time.Duration
		firings      []firingDef
	}

	ip := func(v int) *int { return &v }
	sp := func(v string) *string { return &v }
	dp := func(v time.Duration) *time.Duration { return &v }

	rules := []alertDef{
		{
			name: "Error spike: production", enabled: false,
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
			name: "New fatal issues", enabled: false,
			trigger: "new_issue",
			channel: "slack", webhookURL: sp("https://hooks.slack.com/services/example/placeholder"),
			cooldownMins: 30, filterLevel: sp("fatal"),
			lastFiredAgo: dp(3 * time.Hour),
			firings: []firingDef{
				{firedAgo: 3 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(3), attempt: 1},
				{firedAgo: 6 * time.Hour, status: "failed", statusCode: ip(503), errMsg: sp("Slack returned 503 Service Unavailable"), itemCount: ip(2), attempt: 3},
				{firedAgo: 11 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(1), attempt: 1},
				// Historical failure only; demo data must never schedule real delivery.
				{firedAgo: 27 * time.Hour, status: "failed", statusCode: ip(429), errMsg: sp("Too Many Requests"), itemCount: ip(4), attempt: 2, retryIn: nil},
				{firedAgo: 51 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(2), attempt: 1},
			},
		},
		{
			name: "Production regression", enabled: false,
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
			name: "New or regressed: all envs", enabled: false,
			trigger: "new_or_regressed",
			channel: "email", emailTo: sp("oncall@example.com"),
			cooldownMins: 60,
			lastFiredAgo: dp(8 * time.Hour),
			firings: []firingDef{
				{firedAgo: 8 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(5), attempt: 1},
				// Historical delivery failure, without a live retry.
				{firedAgo: 30 * time.Hour, status: "failed", errMsg: sp("dial tcp: connection refused"), itemCount: ip(8), attempt: 1, retryIn: nil},
				{firedAgo: 54 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(3), attempt: 1},
				{firedAgo: 96 * time.Hour, status: "success", statusCode: ip(200), itemCount: ip(2), attempt: 1},
			},
		},
		{
			name: "Missed scheduled jobs", enabled: false,
			trigger: "cron_missed",
			channel: "email", emailTo: sp("alerts@example.com"),
			cooldownMins: 30,
			lastFiredAgo: nil, // never fired
			firings:      nil,
		},
		{
			name: "Error logs: stripe", enabled: false,
			trigger: "log_count", threshold: ip(10), windowMins: ip(5),
			channel: "email", emailTo: sp("alerts@example.com"),
			cooldownMins: 60, filterLevel: sp("error"), filterEnv: sp("production"),
			filterSearch: sp("stripe"),
			lastFiredAgo: dp(12 * time.Minute),
			firings: []firingDef{
				{firedAgo: 12 * time.Minute, status: "success", statusCode: ip(200), itemCount: ip(14), attempt: 1},
			},
		},
		{
			name: "High error volume", enabled: false,
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
				 filter_level, filter_environment, min_occurrences, filter_search, last_fired_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id`,
			prefix+r.name, r.enabled, r.trigger, r.threshold, r.windowMins,
			r.channel, r.webhookURL, r.emailTo, r.cooldownMins,
			r.filterLevel, r.filterEnv, nil, r.filterSearch, lastFiredAt,
		).Scan(&id)
		if err != nil {
			failed++
			fmt.Printf("  FAIL  create alert %q: %v\n", r.name, err)
			continue
		}
		{
			if _, perr := pool.Exec(context.Background(), `
				INSERT INTO alert_rule_projects (rule_id, project_id)
				VALUES ($1, $2)
			`, id, projectID); perr != nil {
				failed++
				fmt.Printf("  FAIL  attach project for %q: %v\n", r.name, perr)
			}
		}

		status := "enabled"
		if !r.enabled {
			status = "disabled"
		}
		if lastFiredAt != nil {
			fmt.Printf("  OK    %q (%s, %s) last fired %s ago\n", r.name, r.trigger, status, r.lastFiredAgo.Round(time.Minute))
		} else {
			fmt.Printf("  OK    %q (%s, %s): never fired\n", r.name, r.trigger, status)
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
				failed++
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
	return failed
}

// =============================================================================
// Log seeding
// =============================================================================

// Log templates name their placeholders, so each one gets a value of the
// right kind: a route where a route belongs, a sensible percentage, a
// threshold below the measured value.
var logMessages = map[string][]string{
	"trace": {
		"entering handler {route}",
		"cache lookup key=product:{sku} hit=false",
		"serialized response: {bytes} bytes",
	},
	"debug": {
		"request started: {route}",
		"session resumed for {user}",
		"cache hit key=cart:{session} ttl={ttl}s",
		"query took {ms}ms: SELECT … FROM {table}",
		"job {job} enqueued on queue default",
	},
	"info": {
		"{user} signed in",
		"order {order} placed by {user} ({items} items)",
		"payment {payment} captured for order {order}",
		"order {order} shipped via {carrier}",
		"password reset email sent to {email}",
		"job {job} completed in {ms}ms",
		"webhook {webhook} delivered: HTTP 200",
		"stock updated for {sku}: {qty} available",
		"coupon {coupon} applied to order {order}",
	},
	"warning": {
		"slow query: {ms_slow}ms on {table} (threshold 500ms)",
		"retry {attempt}/3 for job {job}",
		"disk usage at {pct}% on db-1",
		"carrier {carrier} rates API slow: {ms_slow}ms",
		"queue default backlog: {backlog} jobs pending",
		"low stock for {sku}: {low} left",
		"card declined for order {order}: insufficient_funds",
	},
	"error": {
		"payment authorization failed for order {order}: provider timeout",
		"failed to send order confirmation to {email}: SMTP 421",
		"{api} returned 503 after {ms_slow}ms",
		"could not acquire lock inventory:{sku}",
		"webhook {webhook} delivery failed: HTTP 500",
	},
	"fatal": {
		"worker crashed: out of memory (limit 512MB)",
		"cannot reach database after 5 retries: connection refused",
	},
}

var logRoutes = []string{
	"GET /api/products/{slug}", "POST /api/cart/lines", "GET /api/cart",
	"POST /api/checkout", "GET /api/orders", "POST /v1/payments/authorize",
	"POST /webhooks/stripe", "GET /api/search",
}

var logTables = []string{"orders", "order_items", "products", "inventory", "payments", "customers"}
var logJobs = []string{"SendOrderConfirmation", "SyncProductCatalog", "apply_stock_update", "build_sales_report", "ledger.reconcile"}
var logAPIs = []string{"api.stripe.com", "api.sendgrid.com", "rates.example.com", "api.carrier.example.com"}
var logCarriers = []string{"DHL", "UPS", "FedEx"}
var logSKUs = []string{"TR-GTX-42", "PK-38L-OLV", "JK-RAIN-M", "SB-DOWN-L", "HL-USB-400"}

// logPlaceholder resolves one named placeholder to a value of its kind.
func logPlaceholder(name string) string {
	user := randomChoice(seedUsers)
	n := func(lo, hi int) string { return strconv.Itoa(lo + rand.Intn(hi-lo+1)) } //nolint:gosec
	switch name {
	case "user":
		if user.username != "" {
			return user.username
		}
		return "guest"
	case "email":
		if user.email != "" {
			return user.email
		}
		return "jane.doe@example.com"
	case "route":
		return randomChoice(logRoutes)
	case "table":
		return randomChoice(logTables)
	case "job":
		return randomChoice(logJobs)
	case "api":
		return randomChoice(logAPIs)
	case "carrier":
		return randomChoice(logCarriers)
	case "sku":
		return randomChoice(logSKUs)
	case "order":
		return "ORD-" + n(10200, 10999)
	case "payment":
		return "pay_" + newEventID()[:12]
	case "webhook":
		return "evt_" + newEventID()[:12]
	case "session":
		return newEventID()[:8]
	case "coupon":
		return randomChoice([]string{"AUTUMN15", "WELCOME10", "FREESHIP"})
	case "items":
		return n(1, 6)
	case "qty":
		return n(12, 340)
	case "low":
		return n(1, 4)
	case "ms":
		return n(4, 180)
	case "ms_slow":
		return n(620, 4800)
	case "ttl":
		return n(30, 900)
	case "bytes":
		return n(800, 48000)
	case "attempt":
		return n(1, 3)
	case "pct":
		return n(81, 94)
	case "backlog":
		return n(240, 1800)
	}
	return name
}

func logBody(level string) string {
	msgs := logMessages[level]
	if len(msgs) == 0 {
		return "log entry"
	}
	tmpl := randomChoice(msgs)
	var b strings.Builder
	for {
		start := strings.IndexByte(tmpl, '{')
		end := strings.IndexByte(tmpl, '}')
		if start < 0 || end < start {
			b.WriteString(tmpl)
			return b.String()
		}
		b.WriteString(tmpl[:start])
		b.WriteString(logPlaceholder(tmpl[start+1 : end]))
		tmpl = tmpl[end+1:]
	}
}

func buildLogRecord(level string, ts time.Time, releases, environments []string) map[string]any {
	env := randomChoice(environments)
	release := releaseAt(ts, releases)

	attrs := map[string]any{
		"sentry.environment": env,
		"sentry.release":     release,
	}
	for k, v := range sentryUserAttrs(randomChoice(seedUsers)) {
		attrs[k] = v
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
	case "warning":
		attrs["duration_ms"] = rand.Intn(4000) + 500 //nolint:gosec
	}

	rec := map[string]any{
		"timestamp":  float64(ts.UnixNano()) / 1e9,
		"level":      level,
		"body":       logBody(level),
		"attributes": attrs,
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
	{"fatal", 1},
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

func buildLogBatches(releases, environments []string, scale int) [][]map[string]any {
	// Send historical logs in batches of 20 spread over the past 7 days.
	total := 360 * max(1, scale)
	batchSize := 20
	var batches [][]map[string]any
	for i := 0; i < total; i += batchSize {
		var batch []map[string]any
		for j := 0; j < batchSize && i+j < total; j++ {
			ts := showcaseTime(i+j, total)
			level := pickLogLevel()
			batch = append(batch, buildLogRecord(level, ts, releases, environments))
		}
		batches = append(batches, batch)
	}
	return batches
}

func buildFreshLogs(releases, environments []string) []map[string]any {
	var batch []map[string]any
	for i := range 30 {
		ago := time.Duration(rand.Intn(2*60)) * time.Second //nolint:gosec
		ts := time.Now().UTC().Add(-ago)
		// Every severity shows in the last day, but a fatal only once.
		level := []string{"info", "info", "info", "debug", "warning", "info", "error", "debug"}[i%8]
		if i < 2 {
			level = []string{"fatal", "trace"}[i]
		}
		batch = append(batch, buildLogRecord(level, ts, releases, environments))
	}
	return batch
}

// --- Profiles ---
//
// Kept in this file rather than a sibling because the documented way to run
// the seeder is "go run scripts/seed/main.go", which compiles this file alone.

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
	// spans keep the waterfall above the flame graph from being empty. A
	// profiled transaction with no spans leaves the detail page dominated by
	// "No spans recorded" and pushes the graph off the bottom of the screen.
	// They mirror the profile's own branches so both tell the same story.
	spans []spanNode
}

// sampleIntervalNs is the default sampling period in both the PHP and Python
// SDKs, 101 Hz. Seeding at the real rate keeps the sample counts and the
// millisecond figures in the UI honest.
const sampleIntervalNs = int64(9_900_990)

var laravelProfiles = []profileTemplate{
	{
		txName:     "GET /api/orders/export",
		op:         "http.server",
		platform:   "php",
		durationMs: [2]int{280, 620},
		root: []profileFrame{
			{function: "/var/www/app/public/index.php", filename: "public/index.php", absPath: "/var/www/app/public/index.php", lineno: 17},
			{function: `Illuminate\Foundation\Http\Kernel::handle`, module: `Illuminate\Foundation\Http\Kernel`, filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", lineno: 141},
			{function: `Illuminate\Routing\Router::dispatch`, module: `Illuminate\Routing\Router`, filename: "vendor/laravel/framework/src/Illuminate/Routing/Router.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Routing/Router.php", lineno: 714},
			{function: `App\Http\Controllers\OrderController::export`, module: `App\Http\Controllers\OrderController`, filename: "app/Http/Controllers/OrderController.php", absPath: "/var/www/app/app/Http/Controllers/OrderController.php", lineno: 42},
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
		spans: []spanNode{
			{op: "middleware.handle", description: "Authenticate", durationMs: [2]int{3, 8}},
			{op: "cache.get", description: "orders:index:page:1", durationMs: [2]int{2, 9}},
			{op: "db.query", description: "SELECT COUNT(*) AS aggregate FROM `orders` WHERE `status` = ?", durationMs: [2]int{12, 40}},
			{op: "db.query", description: "SELECT * FROM `orders` WHERE `status` = ? ORDER BY `created_at` DESC LIMIT 50", durationMs: [2]int{90, 240}},
			{op: "db.query", description: "SELECT * FROM `order_items` WHERE `order_items`.`order_id` IN (?, ?, ?, ?, ?)", durationMs: [2]int{20, 60}},
			{op: "function", description: "OrderResource::collection", durationMs: [2]int{30, 90}, children: []spanNode{
				{op: "function", description: "Money::format", durationMs: [2]int{8, 30}},
			}},
			{op: "template.render", description: "resources/views/orders/index.blade.php", durationMs: [2]int{18, 60}},
		},
	},
	{
		txName:     "POST /api/checkout/confirm",
		op:         "http.server",
		platform:   "php",
		durationMs: [2]int{420, 980},
		root: []profileFrame{
			{function: "/var/www/app/public/index.php", filename: "public/index.php", absPath: "/var/www/app/public/index.php", lineno: 17},
			{function: `Illuminate\Foundation\Http\Kernel::handle`, module: `Illuminate\Foundation\Http\Kernel`, filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", absPath: "/var/www/app/vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", lineno: 141},
			{function: `App\Http\Controllers\CheckoutController::confirm`, module: `App\Http\Controllers\CheckoutController`, filename: "app/Http/Controllers/CheckoutController.php", absPath: "/var/www/app/app/Http/Controllers/CheckoutController.php", lineno: 71},
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
		spans: []spanNode{
			{op: "http.client", description: "POST https://api.stripe.com/v1/payment_intents", durationMs: [2]int{180, 520}},
			{op: "db.query", description: "UPDATE `inventory` SET `reserved` = `reserved` + ? WHERE `sku` = ?", durationMs: [2]int{40, 130}},
			{op: "db.query", description: "INSERT INTO `orders` (`user_id`, `total`, `status`) VALUES (?, ?, ?)", durationMs: [2]int{8, 26}},
		},
	},
}

var pythonProfiles = []profileTemplate{
	{
		txName:     "GET /api/reports/revenue",
		op:         "http.server",
		platform:   "python",
		durationMs: [2]int{350, 900},
		root: []profileFrame{
			{function: "run", module: "gunicorn.workers.sync", filename: "gunicorn/workers/sync.py", absPath: "/usr/lib/python3.12/site-packages/gunicorn/workers/sync.py", lineno: 128},
			{function: "__call__", module: "django.core.handlers.wsgi", filename: "django/core/handlers/wsgi.py", absPath: "/usr/lib/python3.12/site-packages/django/core/handlers/wsgi.py", lineno: 124},
			{function: "revenue_report", module: "reports.views", filename: "reports/views.py", absPath: "/srv/app/reports/views.py", lineno: 57, inApp: true},
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
		spans: []spanNode{
			{op: "db.query", description: "SELECT date_trunc('day', created_at), SUM(amount) FROM ledger GROUP BY 1", durationMs: [2]int{120, 380}},
			{op: "http.client", description: "GET https://charts.internal/render", durationMs: [2]int{40, 140}},
		},
	},
	{
		txName:     "celery: app.tasks.process_export",
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
		spans: []spanNode{
			{op: "db.query", description: "SELECT * FROM exports WHERE state = 'pending' FOR UPDATE SKIP LOCKED", durationMs: [2]int{60, 190}},
			{op: "file.write", description: "/tmp/export-batch.csv", durationMs: [2]int{300, 900}},
			{op: "http.client", description: "PUT s3://exports/2026/report.csv", durationMs: [2]int{120, 460}},
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
	n := rand.Intn(total) //nolint:gosec
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
	release := releaseAt(ts, releases)
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
		"spans": fitSpans(buildSpanNodes(tmpl.spans, spanID, ts), ts, end),
		"user":  sentryUserMap(randomChoice(seedUsers)),
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
	threadID := fmt.Sprintf("%d", 8412331008+rand.Intn(4096)) //nolint:gosec
	release := releaseAt(start, releases)
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
		spanID := newEventID()[:16]

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
					"span_id":  spanID,
					"op":       tmpl.op,
					"status":   "ok",
					// This is how the server picks one thread's samples out of
					// a chunk that may hold several.
					"data": map[string]any{"thread.id": threadID, "thread.name": "MainThread"},
				},
				// And this is the only link from a transaction to its chunks.
				"profile": map[string]any{"profiler_id": profilerID},
			},
			"spans": fitSpans(buildSpanNodes(tmpl.spans, spanID, txStart), txStart, txEnd),
			"user":  sentryUserMap(randomChoice(seedUsers)),
		})
	}
	return chunk, txs
}

// profileAge keeps complete sample windows in the past and inside the default 24h view.
func profileAge() time.Duration {
	return time.Duration(1+rand.Intn(24*60-1)) * time.Minute //nolint:gosec
}

// releaseAt models a gradual rollout, with older clients still on prior builds.
func releaseAt(ts time.Time, releases []string) string {
	age := max(time.Duration(0), time.Since(ts))
	index := max(0, len(releases)-1-int(age/(7*24*time.Hour/time.Duration(len(releases)))))
	return releases[index]
}

// groupingDelay gives the server's asynchronous writers time to group the
// events already accepted before later ones arrive.
const groupingDelay = 2 * time.Second

// issueOffsetCap keeps the quietest issue inside the default 24h window.
const issueOffsetCap = 20 * time.Hour

// issueOffset moves an issue's whole history back in time: busy issues stay
// current, quieter ones were last seen hours ago.
func issueOffset(index int, spread bool) time.Duration {
	if !spread {
		return 0
	}
	return min(time.Duration(index*index)*9*time.Minute, issueOffsetCap)
}

// Keep every screen populated in the default 24h filter and retain a weekly baseline.
func showcaseTime(index, count int) time.Time {
	now := time.Now().UTC().Add(-time.Minute)
	if index < max(1, count*2/3) {
		return now.Add(-time.Duration(index) * 23 * time.Hour / time.Duration(max(1, count*2/3)))
	}
	return trafficBiasedTime(7 * 24 * time.Hour).Add(-time.Minute)
}

func spanData(op, description string) map[string]any {
	switch {
	case strings.HasPrefix(op, "cache"):
		data := map[string]any{"cache.item_size": 2048, "cache.key": strings.TrimPrefix(description, "GET ")}
		if op == "cache.get" {
			hit := rand.Intn(5) != 0 //nolint:gosec
			if strings.Contains(description, "(miss)") {
				hit = false
			}
			data["cache.hit"] = hit
		}
		return data
	case strings.HasPrefix(op, "db"):
		system := "postgresql"
		if strings.Contains(description, "`") {
			system = "mysql"
		}
		return map[string]any{"db.system": system, "db.name": "commerce", "db.rows_affected": 24}
	case strings.HasPrefix(op, "queue"), strings.HasPrefix(op, "task"), strings.HasPrefix(op, "job"), strings.HasPrefix(op, "celery"):
		return map[string]any{"messaging.destination.name": "orders", "messaging.message.retry.count": 0}
	default:
		return map[string]any{}
	}
}

// Preserve nested and parallel relationships while fitting the entire tree to its transaction.
func fitSpans(spans []map[string]any, start, end time.Time) []map[string]any {
	latest := end
	for _, span := range spans {
		at, _ := time.Parse(time.RFC3339Nano, span["timestamp"].(string))
		if at.After(latest) {
			latest = at
		}
	}
	if !latest.After(end) {
		return spans
	}
	ratio := float64(end.Sub(start)) / float64(latest.Sub(start))
	for _, span := range spans {
		for _, key := range []string{"start_timestamp", "timestamp"} {
			at, _ := time.Parse(time.RFC3339Nano, span[key].(string))
			span[key] = start.Add(time.Duration(float64(at.Sub(start)) * ratio)).Format(time.RFC3339Nano)
		}
	}
	return spans
}

// checkoutStacks are the PHP stack traces of the four linked checkout
// failures, so each issue points at the code that raised it.
var checkoutStacks = func() [][]stackFrame {
	controller := stackFrame{
		function: "store", module: "App\\Http\\Controllers\\Api\\CheckoutController", filename: "app/Http/Controllers/Api/CheckoutController.php", lineno: 58,
		preContext:  []string{"        $order = $this->orders->draft($request->user(), $cart);", "        $this->inventory->reserve($order);", "        $this->promotions->apply($order, $request->input('coupon'));"},
		contextLine: "        $payment = $this->payments->authorize($order);",
		postContext: []string{"        $order->markPaid($payment);", "        SendReceipt::dispatch($order);"},
	}
	kernel := stackFrame{function: "handle", module: "Illuminate\\Foundation\\Http\\Kernel", filename: "vendor/laravel/framework/src/Illuminate/Foundation/Http/Kernel.php", lineno: 147}
	at := func(line int, context string, pre ...string) stackFrame {
		f := controller
		f.lineno, f.contextLine, f.preContext, f.postContext = line, context, pre, nil
		return f
	}
	return [][]stackFrame{
		{
			{function: "transfer", module: "GuzzleHttp\\Handler\\CurlHandler", filename: "vendor/guzzlehttp/guzzle/src/Handler/CurlHandler.php", lineno: 44},
			{
				function: "authorize", module: "App\\Services\\Payments\\PaymentClient", filename: "app/Services/Payments/PaymentClient.php", lineno: 37,
				preContext:  []string{"    public function authorize(Order $order): Payment", "    {", "        $payload = PaymentPayload::fromOrder($order);"},
				contextLine: "        $response = $this->http->timeout(3)->post('/v1/payments/authorize', $payload);",
				postContext: []string{"", "        return Payment::fromResponse($response->throw());", "    }"},
			},
			controller,
			kernel,
		},
		{
			{
				function: "reserve", module: "App\\Services\\InventoryService", filename: "app/Services/InventoryService.php", lineno: 52,
				preContext:  []string{"        $stock = Inventory::lockForUpdate()->findOrFail($line->product_id);", "        if ($stock->available() < $line->quantity) {"},
				contextLine: "            throw new InventoryConflict('Reserved quantity exceeds available inventory');",
				postContext: []string{"        }", "        $stock->reserve($line->quantity);"},
			},
			at(56, "        $this->inventory->reserve($order);", "        $order = $this->orders->draft($request->user(), $cart);"),
			kernel,
		},
		{
			{
				function: "failed", module: "App\\Jobs\\SendReceipt", filename: "app/Jobs/SendReceipt.php", lineno: 61,
				preContext:  []string{"    public function failed(Throwable $e): void", "    {"},
				contextLine: "        throw new ReceiptDeliveryError('Receipt delivery exhausted its retry budget', previous: $e);",
				postContext: []string{"    }"},
			},
			{function: "process", module: "Illuminate\\Queue\\Worker", filename: "vendor/laravel/framework/src/Illuminate/Queue/Worker.php", lineno: 435},
		},
		{
			{
				function: "apply", module: "App\\Services\\PromotionService", filename: "app/Services/PromotionService.php", lineno: 29,
				preContext:  []string{"        $promotion = $this->promotions->find($code);", "        if ($promotion->ends_at->isPast()) {"},
				contextLine: "            throw new ExpiredPromotion('Promotion expired before checkout completed');",
				postContext: []string{"        }"},
			},
			at(57, "        $this->promotions->apply($order, $request->input('coupon'));", "        $this->inventory->reserve($order);"),
			kernel,
		},
	}
}()

// checkoutSpans is the checkout request behind the linked investigations:
// authenticate, load the cart, lock stock, apply promotions, then wait on the
// payment provider before writing the order and queueing its follow-ups.
var checkoutSpans = []spanNode{
	{op: "middleware.handle", description: "Authenticate session", durationMs: [2]int{6, 6}},
	{op: "cache.get", description: "GET cart:9f2c41", durationMs: [2]int{3, 3}},
	{op: "db.query", description: "SELECT id, sku, price FROM products WHERE id IN (?, ?, ?)", durationMs: [2]int{12, 12}},
	{op: "db.query", description: "SELECT id, quantity FROM inventory WHERE product_id = $1 FOR UPDATE", durationMs: [2]int{95, 95}},
	{op: "function", description: "Apply promotions", durationMs: [2]int{9, 9}, children: []spanNode{
		{op: "cache.get", description: "GET promo:AUTUMN15", durationMs: [2]int{2, 2}},
	}},
	{op: "http.client", description: "POST https://payments.example.com/v1/payments/authorize", durationMs: [2]int{3000, 3000}},
	{op: "db.query", description: "INSERT INTO orders (customer_id, total, currency, status) VALUES (?, ?, ?, ?)", durationMs: [2]int{6, 6}},
	{op: "queue.publish", description: "orders.receipt", durationMs: [2]int{5, 5}},
	{op: "queue.publish", description: "orders.payment_retry", durationMs: [2]int{4, 4}},
}

func buildInvestigation(def projectDef, index int) ([]envelopeItem, string) {
	return buildInvestigationAt(def, index, time.Now().UTC().Add(-time.Duration(3+index*4)*time.Minute), seedUsers[0])
}

// buildInvestigationAt builds one linked checkout failure for customer u at ts.
func buildInvestigationAt(def projectDef, index int, ts time.Time, u seedUser) ([]envelopeItem, string) {
	names := []string{"Checkout payment authorization", "Inventory reservation", "Receipt delivery", "Discount validation"}
	tmpl := def.issues[index%len(def.issues)]
	tmpl.excType = []string{"PaymentGatewayTimeout", "InventoryConflict", "ReceiptDeliveryError", "ExpiredPromotion"}[index]
	tmpl.excValue = []string{"Payment provider did not respond within 3 seconds", "Reserved quantity exceeds available inventory", "Receipt delivery exhausted its retry budget", "Promotion expired before checkout completed"}[index]
	tmpl.transaction = "POST /api/checkout"
	tmpl.fingerprint = []string{"tindra-demo", def.name, names[index]}
	if tmpl.platform == "php" {
		tmpl.stacktrace = checkoutStacks[index]
	}
	// Each occurrence is its own order; the most recent one is ORD-10482.
	order := "ORD-10482"
	if age := time.Since(ts); age > 30*time.Minute {
		order = fmt.Sprintf("ORD-%d", 10482-int(age/(10*time.Minute)))
	}
	event := buildErrorEvent(tmpl, ts.Add(3100*time.Millisecond), def.releases, []string{"production"})
	eventID := newEventID()
	event["event_id"] = eventID
	event["user"] = sentryUserMap(u)
	event["request"] = map[string]any{"url": "https://shop.example.com/api/checkout", "method": "POST", "headers": map[string]string{"Content-Type": "application/json"}, "data": map[string]any{"currency": "USD", "items": 3, "order_id": order}}
	tx := buildTransaction(txTemplate{name: names[index], platform: tmpl.platform, op: "http.server", durationMs: [2]int{3200, 3200}, spans: checkoutSpans}, ts, def.releases, []string{"production"})
	tx["event_id"] = newEventID()
	tx["user"] = sentryUserMap(u)
	trace := tx["contexts"].(map[string]any)["trace"].(map[string]any)
	trace["status"] = "internal_error"
	event["contexts"].(map[string]any)["trace"] = trace
	// Each case fails in its own span: the payment call, the inventory lock,
	// the receipt job or the promotion lookup.
	failing := []string{"payments.example.com", "FROM inventory", "orders.receipt", "promo:"}[index]
	for _, span := range tx["spans"].([]map[string]any) {
		description, _ := span["description"].(string)
		if strings.Contains(description, failing) {
			span["status"] = "internal_error"
		}
		if strings.HasPrefix(description, "GET cart:") {
			span["data"].(map[string]any)["cache.hit"] = index%2 == 0
		}
	}
	var logs []map[string]any
	for i, body := range []string{"Checkout started for order " + order, "Inventory reserved for 3 items", tmpl.excValue} {
		record := buildLogRecord([]string{"info", "debug", "error"}[i], ts.Add(time.Duration(i)*time.Second), def.releases, []string{"production"})
		record["body"], record["trace_id"], record["span_id"] = body, trace["trace_id"], trace["span_id"]
		attrs := record["attributes"].(map[string]any)
		for key := range attrs {
			if strings.HasPrefix(key, "user.") {
				delete(attrs, key)
			}
		}
		for key, value := range sentryUserAttrs(u) {
			attrs[key] = value
		}
		attrs["http.status_code"] = []int{200, 200, 500}[i]
		attrs["order.id"], attrs["http.route"], attrs["http.method"] = order, "/api/checkout", "POST"
		logs = append(logs, record)
	}
	return []envelopeItem{{typ: "event", payload: event}, {typ: "transaction", payload: tx}, {typ: "log", payload: logs}}, eventID
}

// Only touch the four events created by this run, after asynchronous grouping finishes.
func seedWorkflow(ctx context.Context, pool *pgxpool.Pool, projectID string, eventIDs []string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		var grouped int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE project_id=$1 AND event_id=ANY($2) AND issue_id IS NOT NULL`, projectID, eventIDs).Scan(&grouped); err != nil {
			return err
		}
		if grouped == len(eventIDs) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for seeded issues: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for i, eventID := range eventIDs {
		status := []string{"regressed", "open", "resolved", "ignored"}[i%4]
		var issueID, previousStatus string
		if err := tx.QueryRow(ctx, `WITH previous AS (
            SELECT id,status FROM issues WHERE id=(SELECT issue_id FROM events WHERE project_id=$1 AND event_id=$2) FOR UPDATE
        ) UPDATE issues i SET status=$3,regressed_at=CASE WHEN $3='regressed' THEN i.last_seen ELSE NULL END
          FROM previous p WHERE i.id=p.id RETURNING i.id,p.status`, projectID, eventID, status).Scan(&issueID, &previousStatus); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO issue_history(issue_id,event_type,details,created_at)
            SELECT $1,CASE WHEN $2='regressed' THEN 'regressed' ELSE 'status_changed' END,
                jsonb_build_object('from',$3::text,'to',$2::text),NOW()-INTERVAL '30 seconds'
            WHERE $2<>$3`, issueID, status, previousStatus)
		if err != nil {
			return err
		}
		// With a team, each issue goes to a different person and the comment
		// comes from whoever picked it up; alone, the operator does both.
		_, err = tx.Exec(ctx, `UPDATE issues SET assignee_id=(SELECT id FROM users WHERE perm_manage_issues OR perm_manage_users ORDER BY created_at OFFSET $2 % GREATEST(1,(SELECT count(*) FROM users WHERE perm_manage_issues OR perm_manage_users)) LIMIT 1) WHERE id=$1`, issueID, i+1)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `INSERT INTO issue_comments(issue_id,user_id,body)
            SELECT $1,assignee_id,$2 FROM issues WHERE id=$1 AND assignee_id IS NOT NULL`, issueID,
			[]string{"Reproduced after the latest deployment. The linked trace shows the payment timeout; retry handling is under investigation.", "Inventory contention is isolated to the checkout path. Checking the query plan before changing the lock strategy.", "Fixed in the latest release and verified with a replay of the affected order.", "Expected validation failure for expired promotions. Ignored while the checkout copy is updated."}[i%4])
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Register deployment dates before ingest creates releases with today's date.
// Existing releases retain their metadata when a demo is topped up.
func seedReleases(ctx context.Context, pool *pgxpool.Pool, projectID string, releases []string) error {
	now := time.Now().UTC()
	for i, version := range releases {
		deployed := now.Add(-time.Duration(len(releases)-i) * 7 * 24 * time.Hour / time.Duration(len(releases)))
		if _, err := pool.Exec(ctx, `INSERT INTO releases(project_id,version,deployed_at) VALUES($1,$2,$3) ON CONFLICT(project_id,version) DO NOTHING`, projectID, version, deployed); err != nil {
			return err
		}
	}
	return nil
}

// =============================================================================
// Showcase
// =============================================================================

// The showcase seeds one fictional outdoor-gear shop for screenshots: four
// projects that share releases, customers and a checkout incident, a small
// team that triages it, and monitors and alert rules spread over the projects
// they watch. Fixture names carry no "Demo:" prefix.

type showcaseProject struct {
	slug, name string
	def        projectDef
	sections   []string
	opts       seedOptions
}

// withReleases gives a project its own release train: each service ships on
// its own schedule, and none of them runs a beta in production.
func withReleases(def projectDef, releases ...string) projectDef {
	def.releases = releases
	return def
}

func showcaseProjects(scale int) []showcaseProject {
	common := []string{"issues", "transactions", "logs"}
	return []showcaseProject{
		{slug: "storefront", name: "Storefront", def: withReleases(jsProject, "2.12.0", "2.13.0", "2.13.1", "2.14.0"), sections: common,
			opts: seedOptions{scale: scale, spread: true}},
		{slug: "shop-api", name: "Shop API", def: withReleases(laravelProject, "3.6.2", "3.7.0", "3.7.1", "3.8.0"), sections: append(common, "profiles", "workflow"),
			opts: seedOptions{scale: scale, spread: true, profiles: "php", showcaseIssue: 24}},
		{slug: "payments", name: "Payments", def: withReleases(goProject, "1.18.0", "1.18.1", "1.19.0", "1.19.1"), sections: common,
			opts: seedOptions{scale: scale, spread: true}},
		{slug: "workers", name: "Workers", def: withReleases(pythonProject, "0.31.0", "0.32.0", "0.32.1", "0.33.0"), sections: append(common, "profiles"),
			opts: seedOptions{scale: scale, spread: true, profiles: "python"}},
	}
}

// showcaseTeam are the people who triage the shop's issues. They have no
// password, so nobody can sign in as them; they exist to own issues, write
// comments and fill the Users page with real permission sets.
var showcaseTeam = []struct {
	name, email string
	perms       storage.UserPermissions
}{
	{"Mara Lindqvist", "mara.lindqvist@example.com", storage.UserPermissions{ManageProjects: true, ManageUsers: true, ManageAlerts: true, ManageIssues: true}},
	{"Jonas Becker", "jonas.becker@example.com", storage.UserPermissions{ManageAlerts: true, ManageIssues: true}},
	{"Aiko Tanaka", "aiko.tanaka@example.com", storage.UserPermissions{ManageIssues: true}},
	{"Samuel Okafor", "samuel.okafor@example.com", storage.UserPermissions{ManageProjects: true, ManageAlerts: true, ManageIssues: true}},
	{"Lena Vogel", "lena.vogel@example.com", storage.UserPermissions{}},
}

// serverBase accepts the server URL or any project DSN on it.
func serverBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("expected the server URL, for example http://localhost:8080")
	}
	return u.Scheme + "://" + u.Host, nil
}

// ensureProject returns the project with this slug, creating it when missing.
func ensureProject(ctx context.Context, pool *pgxpool.Pool, slug, name string) (id, key string, err error) {
	err = pool.QueryRow(ctx, `SELECT id::text, public_key FROM projects WHERE slug=$1`, slug).Scan(&id, &key)
	if err == nil {
		return id, key, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	p, err := storage.CreateProject(ctx, pool, slug, name)
	if err != nil {
		return "", "", err
	}
	return p.ID, p.PublicKey, nil
}

// ensureTeam adds the showcase team, leaving accounts that already exist alone.
func ensureTeam(ctx context.Context, pool *pgxpool.Pool) error {
	for _, m := range showcaseTeam {
		_, err := pool.Exec(ctx, `INSERT INTO users (email, name, password_hash,
				perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues, created_at)
			VALUES ($1, $2, '', $3, $4, $5, $6, now() - interval '90 days')
			ON CONFLICT (email) DO NOTHING`,
			m.email, m.name, m.perms.ManageProjects, m.perms.ManageUsers, m.perms.ManageAlerts, m.perms.ManageIssues)
		if err != nil {
			return fmt.Errorf("team member %s: %w", m.email, err)
		}
	}
	return nil
}

// runShowcase seeds the whole shop and returns the process exit code.
func runShowcase(rawURL, dbURL string, scale int) int {
	if dbURL == "" {
		fmt.Println("error: --showcase needs --db or DATABASE_URL in .env")
		return 1
	}
	base, err := serverBase(rawURL)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return 1
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("error: connect to DB: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := ensureTeam(ctx, pool); err != nil {
		fmt.Printf("error: %v\n", err)
		return 1
	}

	projects := showcaseProjects(scale)
	targets := map[string]dsn{}
	for _, p := range projects {
		id, key, err := ensureProject(ctx, pool, p.slug, p.name)
		if err == nil {
			err = seedReleases(ctx, pool, id, p.def.releases)
		}
		if err != nil {
			fmt.Printf("error: project %s: %v\n", p.slug, err)
			return 1
		}
		targets[p.slug] = dsn{publicKey: key, baseURL: base, projectID: id}
	}

	start := time.Now()
	var total seedResult
	for _, p := range projects {
		fmt.Printf("\n##### %s (%s) #####\n", p.name, p.slug)
		seed := map[string]bool{}
		for _, s := range p.sections {
			seed[s] = true
		}
		res := seedProject(targets[p.slug], p.def, seed, dbURL, p.opts)
		total.sent += res.sent
		total.failed += res.failed
		total.profiledNames = append(total.profiledNames, res.profiledNames...)
	}

	// The server stamps everything with the time it arrived, and issue
	// histograms count by that; spread the seeded history back out.
	time.Sleep(groupingDelay)
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.projectID)
	}
	for _, table := range []string{"events", "transactions"} {
		if _, err := pool.Exec(ctx, `UPDATE `+table+` SET received_at = timestamp WHERE project_id = ANY($1::uuid[]) AND received_at > timestamp`, ids); err != nil {
			fmt.Printf("  FAIL  backdate %s: %v\n", table, err)
			total.failed++
		}
	}

	total.failed += seedUptimeMonitors(pool, func(key string) string { return targets[key].projectID }, "")
	total.failed += seedCronMonitors(targets["workers"], pool, "")
	total.failed += seedAlertRules(pool, targets["shop-api"].projectID, "")

	fmt.Printf("\nDone in %s. %d envelopes sent, %d failures.\n", time.Since(start).Round(time.Second), total.sent, total.failed)
	fmt.Println("Projects: Storefront, Shop API, Payments and Workers, with releases, a team of five and their monitors and alert rules.")
	fmt.Println("The checkout incident: Shop API → PaymentGatewayTimeout, its trace Checkout payment authorization, customer Jane Doe (101).")
	if len(total.profiledNames) > 0 {
		fmt.Println("\nTransactions with a flame graph:")
		for _, n := range total.profiledNames {
			fmt.Printf("  %s\n", n)
		}
	}
	if total.failed > 0 {
		return 1
	}
	return 0
}
