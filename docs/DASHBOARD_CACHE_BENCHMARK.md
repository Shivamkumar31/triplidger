# Dashboard Redis benchmark

This benchmark compares the authenticated dashboard endpoint only. It does not create
expenses, invitations, settlements, or modify financial records.

## Requirements

- Docker Desktop.
- A running TripLedger API and database.
- A test account that belongs to the test group.
- Unchanged expense data for the baseline and cached trials.
- Environment variables for credentials and group ID; never put credentials in `.env`
  files that may be shared or in shell history.

## k6 workload

`benchmarks/dashboard.js` reads:

- `BASE_URL`
- `GROUP_ID`
- `TEST_EMAIL`
- `TEST_PASSWORD`
- `APP_ORIGIN` (optional; defaults to `BASE_URL`)
- `K6_VUS` (default `1`)
- `K6_ITERATIONS` (default `100`)

The script logs in once per virtual user through `POST /api/auth/login`, sends the required
`Origin` and `X-TripLedger: 1` headers, and relies on k6's virtual-user-local cookie jar.
The warm-up is a separate dashboard request. Only subsequent dashboard requests contribute
to the dashboard latency trend and the result counters.

Default workload:

- 1 virtual user
- 100 measured iterations
- 1-second pause between measured dashboard requests

A later 5-user run uses `K6_VUS=5`, optionally with the same 100 measured iterations per
virtual user. k6 iteration counts are global for the scenario, so record the chosen value.

## Windows PowerShell Docker command

Set credentials in the current PowerShell process without printing or hardcoding them:

```powershell
$env:TEST_EMAIL = Read-Host "Test email"
$env:TEST_PASSWORD = Read-Host "Test password" -AsSecureString
```

`Read-Host -AsSecureString` is not directly usable by k6. To avoid storing a credential in
the shell history, enter it without a secure prompt and clear it after the run:

```powershell
$env:TEST_EMAIL = Read-Host "Test email"
$env:TEST_PASSWORD = Read-Host "Test password"
$env:GROUP_ID = "<unchanged-test-group-uuid>"
$env:BASE_URL = "http://api:8080"
$env:APP_ORIGIN = "http://localhost:3000"
```

Run k6 attached to the Compose network. The project network is commonly
`tripledger_default`; replace it with `docker network ls` output if the Compose project name
is different:

```powershell
docker run --rm --network tripledger_default `
  -e BASE_URL="$env:BASE_URL" `
  -e APP_ORIGIN="$env:APP_ORIGIN" `
  -e GROUP_ID="$env:GROUP_ID" `
  -e TEST_EMAIL="$env:TEST_EMAIL" `
  -e TEST_PASSWORD="$env:TEST_PASSWORD" `
  -e K6_VUS=1 `
  -e K6_ITERATIONS=100 `
  -v "${PWD}/benchmarks:/benchmarks:ro" `
  grafana/k6:0.49.0 run /benchmarks/dashboard.js
```

Clear credentials when finished:

```powershell
Remove-Item Env:TEST_EMAIL, Env:TEST_PASSWORD
```

For a host-based API such as `http://host.docker.internal:8080`, Docker Desktop networking
can reach the API without placing k6 on the Compose network. Do not run this against a
public deployment automatically.

## Cache configuration

Disable caching and skip Redis:

```dotenv
CACHE_ENABLED=false
REDIS_URL=
```

Restart the API. The dashboard response will report `X-Cache: BYPASS`.

Enable caching:

```dotenv
CACHE_ENABLED=true
REDIS_URL=redis://redis:6379
CACHE_TTL_SECONDS=60
REDIS_TIMEOUT_MS=250
```

Start Redis and the API:

```powershell
docker compose --profile cache up -d --build redis api
docker compose restart api
```

## Comparison procedure

1. Freeze one development group and leave its expense and settlement data unchanged.
2. Record the test environment: OS, CPU, memory, Docker version, API instance, PostgreSQL
   branch/region, Redis profile, group member count, expense count, settlement count,
   `CACHE_TTL_SECONDS`, virtual users, iteration count, and k6 version.
3. Warm PostgreSQL and the application under the disabled-cache configuration.
4. Run three independent trials with `CACHE_ENABLED=false`, saving each result separately.
5. Restart Redis/API as needed, warm the application, and populate the cache for the test
   group. Verify a dashboard request returns `X-Cache: HIT`.
6. Run three independent enabled-cache trials, saving each result separately.
7. Leave normal cache expiry enabled. A TTL expiry is expected to produce `MISS` for a
   request and is included in the measured run. Each `MISS` refills the PostgreSQL-calculated
   payload for its version.

Warm both configurations for an identical warm-up period and use the same test group,
financial data, virtual-user count, iteration count, and pause. Do not infer Redis speedup
from one warm-up request or one trial.

Save output as separate files by trial, for example:

```text
benchmarks/results/baseline-1.txt
benchmarks/results/baseline-2.txt
benchmarks/results/baseline-3.txt
benchmarks/results/cached-1.txt
benchmarks/results/cached-2.txt
benchmarks/results/cached-3.txt
```

## Calculate latency reduction

Using the median of three trial p95 values for a configuration:

```text
latency reduction (%) =
  (baseline p95 - cached p95) / baseline p95 * 100
```

The k6 script reports p50 and p95, request count, requests per second, error rate, and
`HIT`, `MISS`, `BYPASS`, and `ERROR` cache-status counts. Use the cache-status mix alongside
latency; it is not enough to compare p95 values alone.

## Verify headers in Chrome DevTools

Open `http://localhost:3000`, sign in, and open the dashboard. In Chrome DevTools:

1. Select **Network**.
2. Reload the dashboard and filter for `dashboard` or inspect
   `/api/groups/<group-id>/dashboard`.
3. Open **Headers** and confirm `X-Cache` is `HIT`, `MISS`, `BYPASS`, or `ERROR`.
4. Confirm `Server-Timing` includes `db`, `cache_get`, and, on a cache fill, `cache_set`.
   Open **Timing** for the request timing breakdown.
5. Confirm `Cache-Control: no-store`.

Chrome DevTools shows headers for the Next.js `/api/*` proxy because the Go upstream headers
are forwarded unchanged and `Server-Timing` is exposed to browser JavaScript. Credentials,
cookies, and cached payload data are not written to the observability headers or cache logs.
