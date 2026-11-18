$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
if (!(Test-Path ".env")) { Copy-Item ".env.example" ".env"; Write-Host "Created .env. Paste your Neon URL, save it, then run this script again."; exit 0 }
docker compose build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
docker compose run --rm api sh -c "go mod tidy && go run . migrate"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
docker compose up -d
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Open http://localhost:3000 when the web service is ready. Logs: docker compose logs -f"
