# TripLedger — Go + Next.js + Neon

A runnable development starter for group expenses. PostgreSQL lives in **Neon**;
Docker runs the Go API and Next.js/React frontend. There is **no PostgreSQL container**.

## Included

- Registration/login/logout with bcrypt password hashing and database-backed HttpOnly sessions.
- Create groups, reusable expiring invitations, join groups, membership checks.
- Equal splits among selected members, exact integer paise, deterministic rounding.
- Transactional expense creation, durable idempotency keys, safe concurrent editing and revision snapshots.
- Correct balance calculation including confirmed repayments; settlement suggestions.
- Record a repayment, recipient confirmation/rejection, sender cancellation.
- Responsive dashboard, expense editing, member balances, and repayment history.
- SQL migrations compatible with the seven tables from the planning conversation.
- Go ledger tests and an end-to-end Python API smoke test.
- Optional Redis dashboard cache. It is disabled when `REDIS_URL` is empty.

## Prerequisites

Install Docker Desktop (Linux containers / WSL 2 on Windows) and have a Neon project.
You do not need Go, Node, or PostgreSQL installed on your laptop for Docker setup.
Docker's first build needs internet access to download images and dependencies.

## Start on Windows (PowerShell)

Extract the ZIP, open the `tripledger` folder in VS Code, then open a terminal there.

```powershell
Copy-Item .env.example .env
```

Edit `.env`. Copy your full connection string from Neon's **Connect** dialog:

```dotenv
DATABASE_URL='postgresql://USER:PASSWORD@YOUR-NEON-HOST/neondb?sslmode=require'
APP_ORIGIN=http://localhost:3000
COOKIE_SECURE=false
```

Keep any additional TLS parameters supplied by Neon. Never paste the URL into the frontend,
commit `.env`, or share the password. Neon pooled URLs are supported by pgx.
Use a development branch while learning and testing.

Build containers, apply migrations, and start:

```powershell
docker compose build
docker compose run --rm api sh -c "go mod tidy && go run . migrate"
docker compose up -d
```

Open **http://localhost:3000**. The first compilation can take a little time.

```powershell
docker compose logs -f
```

Alternatively, after editing `.env`, run `powershell -ExecutionPolicy Bypass -File scripts/start.ps1`.
The bypass applies to that invocation only; inspect the small script first.
On macOS/Linux use `sh scripts/start.sh`.

## If you already created the seven tables in Neon

Run the same migration command above. Migration 001 uses `CREATE TABLE IF NOT EXISTS`
and preserves existing rows. Migration 002 adds sessions, idempotency, and expense revisions.
The runner records checksums and applies each migration once, under a transaction and advisory lock.
It does **not** upgrade arbitrary differing table definitions: your existing schema must match
the seven-table design supplied in the conversation. No DROP/TRUNCATE is run.

The schema_migrations bookkeeping table means you will see more than seven tables.
Existing disabled demo-password rows cannot log in. Register with a new email.
Do not edit an applied migration: create a new numbered migration and add its name in migrations/migrate.go.

## Try the complete user journey

1. Register as Shivam in your normal browser. Create `Goa Trip`.
2. Click **Invite members**, copy the link.
3. Open an incognito/private browser, visit the link, register as Aman, click **Accept invitation**.
4. In Shivam's browser click **Refresh**, then **Add expense**: Dinner, ₹900, paid by Shivam,
   include both participants. Shivam should receive ₹450; Aman owes ₹450.
5. In Aman's browser refresh, record a ₹450 repayment to Shivam after paying externally.
6. In Shivam's browser open **Settlements**, refresh, and confirm receipt.
7. Both balances become zero; spending remains ₹900.
8. Edit Dinner to ₹1,000. Aman now owes ₹50; the prior repayment is preserved.

There are no seeded real users and no hardcoded credentials. All dashboard data comes from Neon.
Refresh is manual; realtime updates are a later phase. Invite links using localhost only work
on the same computer. Don't share a localhost invite expecting another device to reach it.

## Development

The frontend source is bind-mounted; Next.js provides hot reload.
Go source is bind-mounted but does not auto-restart. After editing Go code:

```powershell
docker compose restart api
```

After dependency changes rebuild the relevant image. If a stale frontend node_modules volume
causes dependency errors, stop the app and remove only its web-modules volume before rebuilding.
Do not run a broad volume cleanup on other projects.

Run Go tests / vet / formatting:

```powershell
docker compose run --rm api sh -c "go mod tidy && go test -race ./... && go vet ./..."
docker compose run --rm api sh -c "gofmt -w main.go internal migrations"
```

Verify the frontend production build:

```powershell
docker compose run --rm web npm run build
```

Run the full API integration smoke test (Python 3 on your host):

```powershell
python scripts/smoke_test.py
```

The smoke test creates unique test accounts and a test group on your configured Neon branch.
It leaves those records for inspection and never deletes existing data. Use a development branch.

Stop services (Neon data is unaffected):

```powershell
docker compose down
```

## Redis dashboard cache

Redis is optional and holds disposable dashboard data only. PostgreSQL remains authoritative
for users, sessions, memberships, expenses, repayments, revisions, and idempotency records.

Set these values in `.env`:

```dotenv
REDIS_URL=
CACHE_TTL_SECONDS=60
REDIS_TIMEOUT_MS=250
```

Running without Redis:

```powershell
docker compose up -d --build
```

To run with Redis, set `REDIS_URL=redis://redis:6379` in `.env`, then start:

```powershell
docker compose --profile cache up -d --build
```

Use one Redis profile container per Compose project. Redis is reachable only by Compose services;
it has no host port mapping. The disposable cache uses `64mb`, `allkeys-lru`, no AOF and no RDB
save. `maxmemory` limits Redis cache data, not the memory used by the API or Docker host process.

Check cache hits and misses with the API container logs:

```powershell
docker compose logs -f api
```

Request the same member dashboard twice. You should see `dashboard cache miss` followed by
`dashboard cache hit` for that group. Mutations increment `financial_version` transactionally,
so old cache keys are not selected after a committed change.

Temporarily stop Redis and verify PostgreSQL fallback:

```powershell
docker compose stop redis
docker compose restart api
```

Dashboard requests continue to work from PostgreSQL. Restart Redis:

```powershell
docker compose start redis
docker compose restart api
```

Redis startup or request failures never make the API unhealthy; the existing `/healthz` check
still depends only on PostgreSQL.

Set `CACHE_ENABLED=false` to disable all Redis cache operations explicitly, including cache
initialization and dashboard cache reads/writes. Set it to `true` to enable Redis when
`REDIS_URL` is configured. Dashboard responses include `X-Cache: HIT`, `MISS`, `BYPASS`, or
`ERROR`.

For a reproducible local benchmark, use [docs/DASHBOARD_CACHE_BENCHMARK.md](docs/DASHBOARD_CACHE_BENCHMARK.md).

## Architecture

Browser -> Next.js `/api/*` rewrite -> Go API -> Neon PostgreSQL.
The browser never receives DATABASE_URL. Requests use an HttpOnly SameSite=Lax cookie;
mutations require the configured Origin and X-TripLedger header. CORS is not enabled.
The API's localhost port is available for debugging, but the UI uses the same-origin proxy.

For a future HTTPS deployment, set APP_ORIGIN to the exact public frontend origin and
COOKIE_SECURE=true. These Compose defaults bind ports to localhost and are for development.

Every group mutation locks its group row before financial writes. Expense and share changes,
revision snapshots, idempotency response, and financial-version increment commit together.
Dashboard reads use a repeatable-read snapshot. Separate UNION ALL ledger entries avoid
multiplying amounts through joins. Only confirmed repayments affect balances.

Amounts are paise, not floating-point rupees. This starter caps an individual expense/repayment
at 100,000,000,000 paise and groups at 100 members / 10,000 expenses. Suggested transfers are
valid simplifications; they do not guarantee the globally smallest number of transfers.
Actual repayments are accepted as reported, even if they exceed the currently suggested balance:
they may reverse who owes whom. Recipient confirmation is the approval step.

## Scope and limitations

Implemented: the features listed at the top. Not implemented: real payment processing, email
sending, password reset/verification, upload/CDN integration, PDF exports, worker/outbox,
member removal, group archiving controls, expense void controls, audit-history UI, and full
pagination. The dashboard shows the latest 100 expenses and up to 100 repayments (pending first);
totals include every active expense and confirmed repayment. SQL supports some future fields.

Split totals are validated by the Go service, not a cross-row database trigger. Direct manual SQL
can violate that invariant. Owner creation/active membership checks are also service responsibilities.
Session records expire after 7 days; schedule cleanup of expired session rows later.
Idempotency records are retained permanently in this starter to prevent accidental replay.
Expense revisions are stored in the database but not exposed as a history screen yet.
The login limiter is conservative (20 attempts per email and globally per minute per process),
not a production distributed anti-abuse system. Deploying publicly also requires email/account
recovery workflows, observability, secrets handling, HTTPS and an application security review.

## Validation performed for this delivery

Validated in Docker with the configured development database:

- `docker compose config` passed.
- `go test -race ./...` passed for the API and ledger packages.
- The full Python API smoke test passed with Redis disabled.
- Redis profile startup passed; the smoke test passed while Redis was serving cache fills.
- Stopping Redis and running the smoke test passed with PostgreSQL fallback.
- Restarting Redis and the API returned the API to a healthy state.

The smoke test creates unique disposable accounts/groups and retains them in your development
database for inspection. It does not delete existing data.

Dependencies are specified in go.mod/package.json. Dependency installation generates go.sum and
package-lock.json on your machine; commit those generated lock files once the first build succeeds.
Future production Dockerfiles should use npm ci with the committed lock file.

## Troubleshooting

- `DATABASE_URL ... Set ...`: create `.env` next to `compose.yaml` and replace the placeholder.
- Database connection error: check Neon branch/URL/password and keep TLS parameters; don't print credentials in logs.
- `relation sessions does not exist`: run the migration command before starting the API.
- `Invalid request origin`: use http://localhost:3000 exactly, not 127.0.0.1, or update APP_ORIGIN.
- API healthy but browser fails: check `docker compose logs web` for dependency/build errors.
- Port already in use: stop the other local service; if changing the frontend port, update APP_ORIGIN too.
- Login fails for example users from SQL: those accounts have disabled password hashes; register a fresh email.
- Group list empty in another browser: log in and accept an invitation first.
- `migration changed after application`: restore the original file and use a new migration for changes.

See docs/API.md and docs/ROADMAP.md for endpoints and the next implementation phases.
