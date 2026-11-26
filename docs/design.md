# Sudoku design

## Play and navigation

The signed-in app follows Chess's centered cloud header, app navigation,
responsive board-and-sidebar layout and overview grids. The first navigation
item opens the random game setup. The other items open Sessions, Archive and
Puzzles. Ordinary links support direct loads, while HTMX keeps the shell mounted
during in-app navigation. Personal pages are no-store and HTMX history snapshots
are disabled.

- **New game** offers Easy, Medium, Hard, Expert and Master. Creating a game
  generates a fresh board and opens it for play.
- **Sessions** lists active random games. **Archive** holds solved games and
  games the player explicitly archives. Unsaved moves are flushed before
  archiving. An unfinished archived game can be resumed. Both views show the
  latest saved board and sort the newest activity first.
- **Puzzles** groups numbered tiles into collections of at most 256 boards.
  Completed tiles follow Chess's green border and checkmark. In-progress tiles
  have a distinct amber border and expose the state in the accessible label.
  The grid retains the same tile dimensions in each state. A shared board
  tooltip shows the givens on hover or keyboard focus. Tiles open the saved
  attempt or the initial board if no moves have been saved.
- **Play** uses an editable 9×9 board next to digit, clear and pencil controls.
  Players can enter digits and move between cells with the keyboard. Clues are
  immutable. Completed boards are read-only. Collection puzzles offer a link
  to the next unfinished slot.

## Generation and collections

Generate a solution with randomized backtracking, then remove clues in shuffled
order while a solver limited to two results confirms uniqueness. Rate by the
lowest technique tier that solves the board. Easy uses naked singles, Medium
adds hidden singles, Hard adds locked candidates, and Expert adds small
candidate subsets. Master requires a search after those techniques stall.
Bound generation work and honor cancellation. Return a retryable error if the
requested tier cannot be generated within the budget.

The initial collections contain 32 boards per tier. The preparation command
uses PCG seed 20261126 and the zero-based tier index, and the catalog test
verifies uniqueness and rating for every board. Startup records a SHA-256
digest for each edition's ordered boards in PostgreSQL and rejects a changed
edition. Keep collection keys and numbered slots stable across upgrades.
Changing a published board requires a new collection edition. Derive
completion by validating the full submitted board against its immutable clues.

## Persistence and HTTP

PostgreSQL owns random games and per-player puzzle attempts. A random game
stores its givens, difficulty, entries, pencil marks, revision, timestamps and
archive state. A puzzle attempt uses owner UUID, collection key and slot as its
identity and stores entries, pencil marks, revision and completion time. The
first saved edit starts an attempt. A completed attempt is durable and counts
toward the collection total. The immutable definitions ship with the app and
require no separate storage service.

Validate board shape, digits, notes and givens server-side. Save only against
the expected revision. An identical retry can acknowledge the saved board.
Competing edits receive the current revision and preserve the local draft for
the player to resolve. Use one transaction or conditional write for a change
and the associated completion or archive state. Browser saves are serialized,
retain the draft on errors and guard navigation while a draft is pending.

Serve `/`, `/sessions`, `/archive`, `/games/new`, `/games/{id}`, `/puzzles`
and `/puzzles/{collection}/{slot}` on the Sudoku host. Use POST for creation,
saves and archive changes. Require Gateway's signed UUID on the app host and
validate the configured Origin on writes. Use owner-scoped statements even for
administrators. Expose private GET/HEAD `/healthz` and `/readyz`, with readiness
checking PostgreSQL in the background.

## Implementation order

1. Commit the design and independent Go module.
2. Implement and verify the generator, collections, PostgreSQL persistence and
   authenticated HTTP service. Reuse Chess's Gateway verifier, broker discovery,
   health/logging conventions and migration locking.
3. Implement and verify the embedded UI. Reuse the shared branded header,
   PicoCSS and HTMX assets, and adapt Chess's navigation and board preview layout.
4. Add the container and CI workflow, local Compose and Swarm wiring, broker
   scope, image-tag selection, and then the Landing card and Sudoku icon.
   Check both stacks, service suites and browser flows. Register the parent
   gitlink once the independent repository commit is available on its remote.
