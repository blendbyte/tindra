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

Database fixtures use a `Demo:` name prefix. Alert rules are disabled and their historical failures have no scheduled retries. Automatic monitor checks are deferred for 30 days so synthetic endpoints do not overwrite the prepared states during screenshots. These are display fixtures, not active monitoring integrations.

## Focused testing

```sh
go run scripts/seed/main.go --seed=workflow 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
go run scripts/seed/main.go --seed=transactions,profiles,logs 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
go run scripts/seed/main.go --seed=queries,caches,jobs,browser 'http://PUBLIC_KEY@localhost:8080/PROJECT_ID'
```

Performance sub-view names are aliases for `transactions`. Profiles always emit their companion transactions and cover both PHP and Python wire formats, including when another project type is selected. `workflow` emits linked events, transactions and logs; with database access it also sets four issue states and adds comments using an existing operator account. Explicit `monitors` or `alerts` selections require database access.

The seeder tests validate temporal bounds, span parenting, envelope identifiers, profile parsing, cross-feature links, and database fixtures. An integration test runs the documented CLI through the real HTTP ingestion and grouping pipeline against a disposable PostgreSQL database.
