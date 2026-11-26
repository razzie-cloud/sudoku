package catalog

import (
	"testing"

	"github.com/razzie-cloud/sudoku/internal/board"
)

func TestBundledPuzzles(t *testing.T) {
	collections, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range collections {
		for slot, puzzle := range collection.Puzzles {
			unique, err := board.Unique(t.Context(), puzzle)
			if err != nil || !unique || puzzle.Solved() || board.Rate(puzzle) != collection.Difficulty {
				t.Fatalf("%s slot %d: unique=%v, rate=%v, err=%v", collection.Key, slot+1, unique, board.Rate(puzzle), err)
			}
		}
	}
}
