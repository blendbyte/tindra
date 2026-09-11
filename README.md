<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="logo-dark.png">
    <img src="logo.png" width="280" alt="Tindra">
  </picture>
</p>

<p align="center">Self-hosted error tracking, performance monitoring, uptime monitoring and cron monitoring.</p>

<p align="center">
  <a href="https://tindra.sh">tindra.sh</a> &nbsp;·&nbsp;
  <a href="https://tindra.sh/docs">Docs</a> &nbsp;·&nbsp;
  <a href="https://github.com/blendbyte/tindra/releases">Releases</a>
</p>

<p align="center">
  <a href="https://github.com/blendbyte/tindra/actions/workflows/ci.yml"><img src="https://github.com/blendbyte/tindra/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/blendbyte/tindra"><img src="https://codecov.io/gh/blendbyte/tindra/branch/main/graph/badge.svg" alt="Coverage"></a>
  <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/blendbyte/tindra/pkgs/container/tindra"><img src="https://img.shields.io/badge/docker-ghcr.io-2496ED?logo=docker&logoColor=white" alt="Docker"></a>
  <a href="https://github.com/blendbyte/tindra/releases"><img src="https://img.shields.io/github/v/release/blendbyte/tindra" alt="Release"></a>
  <a href="https://www.elastic.co/licensing/elastic-license"><img src="https://img.shields.io/badge/license-ELv2-blue" alt="License: ELv2"></a>
</p>

---

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="dashboard-dark.png">
    <img src="dashboard-light.png" alt="Tindra dashboard showing error and latency metrics, transaction activity, monitors, issues, and release health">
  </picture>
</p>

Tindra runs as one Go binary with one Postgres database and uses Sentry SDKs with a Tindra DSN.

- **Dashboard** with KPI strip, transaction density heatmap, hottest issues, release health, and recent alerts
- **Error tracking** with grouping, stack traces, breadcrumbs, tags, assignees, merge and resolve
- **Log search** with severity, environment, text, and user filters, plus volume alerts created directly from a query
- **Performance monitoring** with transaction list, span waterfall, and p50/p75/p95/p99 percentiles
- **Profiling** with flame graphs on the transaction detail page, from both transaction-based and continuous Sentry SDK profiling
- **User-scoped debugging** to follow an end user across errors, logs, transactions, web vitals, and performance spans
- **Shared investigation filters** that preserve project, environment, user, and time range across views, with shareable URLs and custom windows up to 90 days
- **Cron monitors** with check-in history, missed/error alerts, and Sentry, Oh Dear, and Spatie SDK compatibility
- **Uptime monitors** with HTTP/HTTPS probing, configurable intervals and timeouts, expected status codes and body assertions, 24h/7d/30d uptime stats, and down/recovery alerts
- **Releases** linked to issues and regressions
- **Alerts** via email, Slack, Discord, Microsoft Teams, and webhooks, with filters, log-volume thresholds, and cooldowns
- **Source maps** resolved server-side, no client exposure
- **Guided project setup** with test-event confirmation, ingestion diagnostics, and source map verification against real stack frames
- **Ingestion monitoring** with authenticated Prometheus metrics for queue depth, retries, rejected data, and ingestion health
- **SSO and MFA** with Google, GitHub, Microsoft, Auth0, Zitadel, and OIDC providers, plus authenticator-based two-factor authentication. Microsoft requires a tenant ID and explicit account linking
- **Automatic refresh** with pause and manual refresh controls
- **MCP server** built in to inspect full event payloads, source-mapped stack traces, breadcrumbs, and older event occurrences from Claude or any MCP client via `POST /mcp` using an API token
- **Keyboard-first UI** with command palette, full dark mode, and virtualized lists

## Self-host

```bash
bash -c "$(curl -sSL https://install.tindra.sh)"
```

The installer creates a `docker-compose.yml` with a random database password, pulls the images, and sets up your first account. No manual SQL, no config files.

Full setup guide, environment variable reference, and backup docs at [tindra.sh/docs](https://tindra.sh/docs).

## Local development

Requires Go 1.27+, Bun, and Docker Compose. From the repository root:

```bash
cp .env.example .env
```

Set `PUBLIC_URL=http://localhost:5173` in `.env`, then install dependencies and start the backend:

```bash
(cd web && bun install --frozen-lockfile)
make web
make db
make run
```

The frontend build supplies the assets embedded by Go. The development database listens on `127.0.0.1:5432`, uses the credentials in `.env.example`, and stores data in a separate `tindra-dev` volume. The backend applies migrations on startup and listens on port 8080.

Once the backend is running, create an account in another terminal, replacing the example password with your own (at least 12 characters), then start the frontend:

```bash
make cli ARGS='users create --email you@example.com --password "your-local-password"'
cd web
bun run dev
```

Open `http://localhost:5173`. Two-factor enrollment is required by default. Use `make db-stop` to stop the development database.

## License

[Elastic License 2.0](LICENSE)

## Maintained by Blendbyte

<br>

<p align="center">
  <a href="https://www.blendbyte.com">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://www.blendbyte.com/logo_horizontal_light.png">
      <img src="https://www.blendbyte.com/logo_horizontal.png" alt="Blendbyte" width="360">
    </picture>
  </a>
</p>

<p align="center">
  <strong><a href="https://www.blendbyte.com">Blendbyte</a></strong> builds cloud infrastructure, web apps, and developer tools.<br>
  We've been shipping software to production for 20+ years.
</p>

<p align="center">
  This package runs in our own stack, which is why we keep it maintained.<br>
  Issues and PRs get read. Good ones get merged.
</p>

<br>

<p align="center">
  <a href="https://www.blendbyte.com">blendbyte.com</a> · <a href="mailto:hello@blendbyte.com">hello@blendbyte.com</a>
</p>
