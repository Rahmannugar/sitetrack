# SiteTrack

SiteTrack manages projects, tracks task dependencies and records material deliveries
between company and project stores. The backend is built with Go and PostgreSQL,
and Docker Compose starts the API, email worker and database together.

## Run

Install Docker with Compose, then create your environment file:

```sh
cp .env.example .env
openssl rand -hex 32
```

Generate a separate value for each of these settings in `.env`:

- `SITETRACK_OTP_SECRET`: protects OTP verification hashes.
- `SITETRACK_OUTBOX_ENCRYPTION_KEY`: encrypts queued email payloads.
- `SITETRACK_JWT_SIGNING_SECRET`: signs access tokens.

Set `SITETRACK_RESEND_API_KEY` and `SITETRACK_EMAIL_FROM` for verification and
password-reset emails. Then start the stack:

```sh
docker compose up -d --build --wait
docker compose rm -f migrate
```

Migrations run before the API and worker start. The API listens on
`http://localhost:8080`; PostgreSQL is exposed locally on port `5433` by default.
If you change the database port, update the host database URL in `.env` too.

| Reference | Location |
| --- | --- |
| Interactive API documentation | http://localhost:8080/docs |
| OpenAPI specification | http://localhost:8080/openapi.json |
| Assessment answer and deep dive | [SeronLabs Assessment.docx](<SeronLabs Assessment.docx>) |
| Domain relationships and flows | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Endpoint list | [API.md](API.md) |
| Full dataset and seeded accounts | [SEED.md](SEED.md) |
| Verification commands and results | [LOAD_TESTING.md](LOAD_TESTING.md) |

Stop the stack while retaining data with `docker compose down`. Use
`docker compose down --volumes` to remove its database data as well.

## Development and verification

Host commands use Go 1.27 and Task. SQL plan analysis uses Python 3, load tests use
k6, and Regel/TLA+ model checks use Java 21 or later.

| Command | Purpose |
| --- | --- |
| `task up-build` | Build and start the stack; remove the completed migrator |
| `task db-up` | Start PostgreSQL |
| `task migrate` | Apply migrations using `.env` |
| `task run-api` | Run the API on the host |
| `task run-worker` | Run the worker on the host |
| `task generate` | Regenerate sqlc adapters |
| `task check` | Check formatting, tests, vet, build and Compose configuration |
| `task test-integration` | Run database tests using `SITETRACK_TEST_DATABASE_URL` |
| `task models` | Check the bounded Regel/TLA+ models and expected counterexamples |
| `task logs` | Follow stack logs |

Use an isolated database for integration tests. Full-scale seeding, all-endpoint
verification and k6 commands are in the linked guides. Seeded users are verified
and share the default password **SiteTrack123!**.

Telemetry export is enabled with `SITETRACK_TELEMETRY_ENABLED=true` and a New Relic
ingest key in `SITETRACK_OTLP_API_KEY`. Rate limiting defaults to enabled;
`SITETRACK_RATE_LIMIT_ENABLED=false` disables it for load tests.

## Decisions and trade-offs

- A modular monolith keeps project, task, stock and notification changes within
  PostgreSQL transactions. One API and one worker match the deployment scope.
- PostgreSQL stores durable email jobs and notification replay state. This keeps
  the deployment small without introducing a separate queue or Redis.
- Each account has one active session. Database checks enforce logout and current
  permissions on the next request.
- Project row locks protect membership and dependency changes. Ordered delivery
  locks and exact decimal quantities protect stock balances under concurrent requests.
- Dependency state is stored and updated with task transitions, so blocked checks
  can use dependency records directly.
- Notifications commit with business changes and stream through SSE. Email delivery
  runs asynchronously through an outbox with leases, retries and idempotency keys.
- Cursor pagination bounds list responses. Measurements informed SQL changes;
  caching remains deferred until it addresses a measured bottleneck.

The in-memory rate limiter assumes a single API process. Multiple API instances
would require shared enforcement. With more time, I would extend sustained load
coverage to the remaining write flows and run longer tests on deployment hardware.
The existing tests verify every endpoint and analyze its SQL; the recorded k6 runs
cover fetching application data and delivery transitions.
