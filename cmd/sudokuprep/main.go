// Command sudokuprep regenerates the initial collections on stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"

	"github.com/razzie-cloud/sudoku/internal/board"
)

const (
	seed = 20261126
	size = 32
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	collections := make(map[board.Difficulty][]board.Board)
	seen := make(map[board.Board]bool)
	for i, difficulty := range board.Difficulties() {
		random := rand.New(rand.NewPCG(seed, uint64(i)))
		for len(collections[difficulty]) < size {
			puzzle, err := board.Generate(context.Background(), difficulty, random)
			if err != nil {
				return fmt.Errorf("%s: %w", difficulty, err)
			}
			if !seen[puzzle] {
				seen[puzzle] = true
				collections[difficulty] = append(collections[difficulty], puzzle)
			}
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(collections)
}
