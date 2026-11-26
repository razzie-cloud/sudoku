package board

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"testing"
)

func TestGenerateAllDifficulties(t *testing.T) {
	for _, difficulty := range Difficulties() {
		t.Run(string(difficulty), func(t *testing.T) {
			t.Parallel()
			for seed := range uint64(8) {
				puzzle, err := Generate(t.Context(), difficulty, rand.New(rand.NewPCG(7, seed)))
				if err != nil {
					t.Fatal(err)
				}
				unique, err := Unique(t.Context(), puzzle)
				if err != nil || !unique || puzzle.Solved() || Rate(puzzle) != difficulty {
					t.Fatalf("bad %s puzzle %s, unique %v, err %v, rating %s", difficulty, puzzle, unique, err, Rate(puzzle))
				}
			}
		})
	}
}

func TestUniqueAndCancellation(t *testing.T) {
	var b Board
	unique, err := Unique(t.Context(), b)
	if unique || err != nil {
		t.Fatalf("empty board unique=%v err=%v", unique, err)
	}
	b[0], b[1] = 7, 7
	unique, err = Unique(t.Context(), b)
	if unique || err != nil {
		t.Fatalf("contradictory board unique=%v err=%v", unique, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Generate(ctx, Master, rand.New(rand.NewPCG(7, 2)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate canceled, got %v", err)
	}
}

func TestBoardAndNotesEncoding(t *testing.T) {
	givens, err := Parse("800000000003600000070090200050007000000045700000100030001000068008500010090000400")
	if err != nil {
		t.Fatal(err)
	}
	var notes Notes
	notes[1], notes[80] = allDigits, 1<<8
	type state struct {
		Board Board
		Notes Notes
	}
	data, err := json.Marshal(state{givens, notes})
	var got state
	if err != nil || json.Unmarshal(data, &got) != nil || got != (state{givens, notes}) {
		t.Fatalf("round trip=%+v, err=%v, json=%s", got, err, data)
	}
	if !givens.ValidEdits(givens, notes) {
		t.Fatal("valid notes rejected")
	}
	b := givens
	b[0] = 9
	if b.ValidEdits(givens, notes) {
		t.Fatal("changed clue accepted")
	}
	b = givens
	b[1] = 1
	if b.ValidEdits(givens, notes) {
		t.Fatal("notes under a placed digit accepted")
	}
}
