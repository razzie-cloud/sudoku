// Package catalog owns immutable bundled collection definitions.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/razzie-cloud/sudoku/internal/board"
)

const MaxPuzzles = 256

//go:generate sh -c "go run ../../cmd/sudokuprep > collections.next && mv collections.next collections.json"
//go:embed collections.json
var bundled []byte

type Collection struct {
	Key        string           `json:"key"`
	Title      string           `json:"title"`
	Difficulty board.Difficulty `json:"difficulty"`
	Puzzles    []board.Board    `json:"puzzles"`
}

func Load() ([]Collection, error) {
	var definitions map[board.Difficulty][]board.Board
	if err := json.Unmarshal(bundled, &definitions); err != nil {
		return nil, err
	}
	difficulties := board.Difficulties()
	if len(definitions) != len(difficulties) {
		return nil, fmt.Errorf("unexpected collection count %d", len(definitions))
	}
	collections := make([]Collection, 0, len(difficulties))
	seen := make(map[board.Board]bool)
	for _, difficulty := range difficulties {
		puzzles := definitions[difficulty]
		if len(puzzles) == 0 || len(puzzles) > MaxPuzzles {
			return nil, fmt.Errorf("invalid collection size for %s", difficulty)
		}
		for _, puzzle := range puzzles {
			if seen[puzzle] {
				return nil, fmt.Errorf("duplicate board in %s", difficulty)
			}
			seen[puzzle] = true
		}
		collections = append(collections, Collection{
			Key: string(difficulty) + "-1", Title: difficulty.Title(), Difficulty: difficulty, Puzzles: puzzles,
		})
	}
	return collections, nil
}
