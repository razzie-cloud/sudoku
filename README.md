# Sudoku

Sudoku is a signed-in Razzie Cloud app for playing random 9×9 games and
numbered puzzle collections. It follows Chess's layout for New game,
Sessions, Archive and Puzzles. PostgreSQL stores each player's boards and
collection progress.

See [the design](docs/design.md) for the difficulty levels, save behavior and
platform integration. This repository owns the Go module
`github.com/razzie-cloud/sudoku`.

## Run and verify

Run `go run ./cmd/sudoku` with these settings:

| Variable | Purpose |
| --- | --- |
| `ORIGIN` | Exact public origin for this app |
| `ACCESS_GATEWAY_URL` | Gateway base URL for public key discovery |
| `POSTGRES_URI` or `POSTGRES_URI_FROM` | Direct database URI or broker URI discovery endpoint |
| `SERVICE_PORT` | Private HTTP port, 8080 by default |
| `LANDING_ORIGIN` | Cloud home link, defaults to `https://razzie.cloud` |
| `POSTGRES_PASSWORD_FILE` | Optional file supplying the database password |
| `BROKER_TOKEN_FILE`, `BROKER_CA_FILE` | Optional broker authentication and CA files |
| `LOG_LEVEL` | DEBUG, INFO, WARN or ERROR, defaults to INFO |

The gateway signs requests for the configured public origin. For development,
use the parent reverse proxy for sign-in and the configured Sudoku host.

Run `go build ./...`, `go vet ./...`, `go test ./...` and
`go test -race ./...`. Set `TEST_DATABASE_URL` to a disposable loopback
PostgreSQL database to include the persistence tests. They use isolated schemas.
Regenerate the initial collections with `go generate ./internal/catalog`.

Set `CHROMIUM_EXECUTABLE_PATH` to an installed Chromium binary alongside
`TEST_DATABASE_URL` to run the browser checks. They exercise the production
templates, signed requests and real PostgreSQL saves in both color schemes.
