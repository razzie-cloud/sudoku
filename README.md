# Sudoku

Sudoku is a signed-in Razzie Cloud app for playing random 9×9 games and
numbered puzzle collections. It follows Chess's layout for New game,
Sessions, Archive and Puzzles. PostgreSQL stores each player's boards and
collection progress.

Solving a board plays a brief local fireworks effect after the server confirms
the save. It respects reduced-motion preferences and does not replay when
reopening a solved board. The embedded `fireworks.js` matches Chess's copy so
each service remains independently deployable.

See [the design](docs/design.md) for the difficulty levels, save behavior and
platform integration. This repository owns the Go module
`github.com/razzie-cloud/sudoku`.

## Run

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
The parent Compose stack starts Sudoku with Air at `http://sudoku.localhost:8080`
and provisions `postgres/sudoku` with a scoped development token. Run
`make local-auth` from the parent to generate the token. For a local stack
already running, recreate the broker to load the updated policy.

The Swarm stack uses two replicas at `sudoku.<DOMAIN>`. Preserve the Sudoku
PostgreSQL database in backups. Once the repository and commit are remotely
available, register `sudoku/` as a parent submodule. Select the published
`SUDOKU_IMAGE_TAG` and a Landing image with the Sudoku icon before rollout.

## Verification

Run `go build ./...`, `go vet ./...`, `go test ./...` and
`go test -race ./...`. Set `TEST_DATABASE_URL` to a disposable loopback
PostgreSQL database to include the persistence tests. They use isolated schemas.
Regenerate the initial collections with `go generate ./internal/catalog`.

Set `CHROMIUM_EXECUTABLE_PATH` to an installed Chromium binary alongside
`TEST_DATABASE_URL` to run the browser checks. They exercise the production
templates, signed requests and real PostgreSQL saves in both color schemes.
They include solved-board fireworks, navigation cleanup, reduced motion and
reopening completed puzzles. Set `SUDOKU_SCREENSHOT_DIR` to capture the previews.

The initial integration passed build and vet, and 117 tests with disposable
PostgreSQL and Chromium, including the race suite. Browser checks covered
320, 390, 768 and 1440 pixel widths, save recovery and archive transitions.
The catalog reproduced byte-for-byte. Runtime and Air images built locally,
along with the updated Landing image. Compose and Swarm passed offline config
checks. The browser fixture signs requests with a test Gateway key. Live
passkey sign-in, image publication and a Swarm rollout remain release checks.
