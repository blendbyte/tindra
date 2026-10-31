# Demo and test data

Run the app, create a dedicated demo project, and copy its DSN from project setup. Use a project with profiling enabled. Create your normal local operator account first if you want assignments and comments in issue details.

```sh
go run scripts/seed/main.go --type=mixed 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
```

The default `all` selection includes errors, transactions and spans, profiles, logs, linked investigations, and database fixtures when a database URL is available. Database lookup uses `--db`, then the `DATABASE_URL` environment variable, then local `.env` files. The database must contain the same project and public key as the DSN.

```sh
go run scripts/seed/main.go --db='postgres://tindra:tindra@localhost:5432/tindra?sslmode=disable' 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
```

Data is additive. Use a fresh demo project for a clean screenshot session; repeated runs add more telemetry, monitors, alert rules, and comments. Existing release dates are preserved. The seeder does not create login credentials or change existing accounts.

## Showcase for screenshots

For website and documentation screenshots, seed a complete shop into a fresh instance instead of one project. The showcase needs database access and takes the server URL rather than a DSN:

```sh
go run scripts/seed/main.go --showcase --scale=6 --db='postgres://tindra:tindra@localhost:5432/tindra?sslmode=disable' http://localhost:8080
```

It creates four projects that tell one story: **Storefront** (the Vue shop front with Web Vitals), **Shop API** (Laravel checkout, orders and PHP profiles), **Payments** (a Go service talking to Stripe) and **Workers** (Celery jobs with continuous Python profiles). Each ships its own release train. A team of five without passwords (nobody can sign in as them) owns and comments on the triaged issues and fills the Users page with different permission sets. Uptime monitors sit with the projects they watch, cron monitors with Workers, and alert rules with Shop API; none of them carries the `Demo:` prefix.

The run staggers when issues were last seen, makes one issue per project new today, gives the checkout incident (`PaymentGatewayTimeout`, trace `Checkout payment authorization`, customer Jane Doe) a six-hour history across several customers, and spreads arrival times so issue histograms show that history. `--scale` multiplies events, transactions and logs; 6 gives a few thousand transactions a day and takes under half a minute. Create your own operator account first; its name shows wherever you act in the screenshots.

## Screenshot tour

Use the **24h** window for the main tour and **7d** to show historical trends and release rollouts. All screenshots should use the same project and environment selection. The default mixed project includes Laravel, Go, Python, and JavaScript examples; use `--list` to see individual project types.

| Screen | What to highlight |
| --- | --- |
| Dashboard and Issues | Recent activity, varied errors and affected customers, production and staging filters |
| Issue details | `PaymentGatewayTimeout`, request context, source snippets, breadcrumbs, linked trace and logs, triage state, assignment and comment |
| Transactions | `Checkout payment authorization`, nested waterfalls, cache lookup, inventory query, payment error and retry job |
| Queries, Caches, Jobs | Query timing, cache hit/miss rates, queue spans and failed work |
| Browser | Page loads and navigation with all five Web Vitals, including good and degraded samples |
| Profiles | The transaction names printed at the end of the run, covering transaction-based and continuous profiles |
| User investigation | Jane Doe (`101`) has the same identity across the linked errors, transactions and logs |
| Releases | Deployments spread over a week, with matching telemetry release versions |
| Logs | All six severity levels, structured attributes, and links back to the checkout trace |
| Monitors | Healthy, failing, paused and unknown uptime states; successful, failed and running cron jobs |
| Alerts | Project-scoped rules, several triggers and channels, and successful or failed delivery history |

Outside the showcase, database fixtures use a `Demo:` name prefix. Alert rules are disabled and their historical failures have no scheduled retries. Automatic monitor checks are deferred for 30 days so synthetic endpoints do not overwrite the prepared states during screenshots. These are display fixtures, not active monitoring integrations.

## Focused testing

```sh
go run scripts/seed/main.go --seed=workflow 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
go run scripts/seed/main.go --seed=transactions,profiles,logs 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
go run scripts/seed/main.go --seed=queries,caches,jobs,browser 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
```

Performance sub-view names are aliases for `transactions`. Profiles always emit their companion transactions and cover both PHP and Python wire formats, including when another project type is selected. `workflow` emits linked events, transactions and logs; with database access it also sets four issue states and adds comments using an existing operator account. Explicit `monitors` or `alerts` selections require database access.

The seeder tests validate temporal bounds, span parenting, envelope identifiers, profile parsing, cross-feature links, and database fixtures. An integration test runs the documented CLI through the real HTTP ingestion and grouping pipeline against a disposable PostgreSQL database.
