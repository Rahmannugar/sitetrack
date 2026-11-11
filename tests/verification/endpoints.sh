#!/bin/sh
set -eu
source_database=${SITETRACK_ENDPOINT_SOURCE_DATABASE:-sitetrack}
check_database="sitetrack_$(date +%s)_endpoint_check"
created=false
cleanup() {
  if [ "$created" = true ]; then
    docker compose exec -T postgres dropdb -U sitetrack --force "$check_database"
  fi
  task up
}
trap cleanup EXIT
trap 'exit 130' INT TERM
docker compose stop api worker
docker compose exec -T postgres createdb -U sitetrack --strategy=FILE_COPY -T "$source_database" "$check_database"
created=true
address=$(docker compose port postgres 5432)
port=${address##*:}
export SITETRACK_ENDPOINT_DATABASE_URL="postgres://sitetrack:sitetrack@127.0.0.1:$port/$check_database?sslmode=disable"
go test -race ./cmd/api -run TestEveryEndpointWithQueryPlans -count=1 -v
