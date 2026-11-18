#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f .env ]; then
 cp .env.example .env
 echo "Created .env. Paste your Neon URL, save it, then run this script again."
 exit 0
fi
docker compose build
docker compose run --rm api sh -c 'go mod tidy && go run . migrate'
docker compose up -d
echo 'Open http://localhost:3000. Logs: docker compose logs -f'
