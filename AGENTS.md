# Sudoku development

This independent Go module is `github.com/razzie-cloud/sudoku`. Keep app code
here, parent Compose and coordination files in the parent, and Swarm files in
`infra`. Follow [the design](docs/design.md).

- Require signed Gateway UUID assertions on the application host. Verify
  mutation Origin and scope every player query by the verified owner. Keep
  startup, logs and private readiness consistent with Chess.
- Keep published collection keys, slot numbers and boards immutable. Validate
  each generated board for one solution and the advertised difficulty. Puzzle
  attempts survive navigation and restarts. Save with a revision check and
  make completion monotonic. Do not send solution boards before completion.
- Use embedded Go templates with local PicoCSS and HTMX. Keep the Chess layout
  and shared account header, support keyboard play and preserve unsaved edits
  across navigation and save errors.
- Format with `gofmt` and run `go build ./...`, `go vet ./...`, `go test ./...`
  and `go test -race ./...`. Use an isolated disposable loopback PostgreSQL
  database for database tests. Check both themes and mobile and desktop widths
  with production CSP and app navigation.

Remote creation, publishing and deployment require authorization.
