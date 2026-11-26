package board

import (
	"context"
	"errors"
	"math/bits"
	"math/rand/v2"
)

var ErrGeneration = errors.New("unable to generate this difficulty")

// A limit bounds even searches started with a context without a deadline.
const searchBudget = 250_000

type search struct {
	ctx      context.Context
	random   *rand.Rand
	nodes    int
	limit    int
	count    int
	solution Board
}

func (s *search) visit(b Board, rows, cols, boxes [Size]uint16) error {
	s.nodes++
	if s.nodes > searchBudget {
		return ErrGeneration
	}
	if s.nodes&255 == 1 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	cell := -1
	width := Size + 1
	var choices uint16
	for i, digit := range b {
		if digit != 0 {
			continue
		}
		mask := allDigits &^ (rows[i/Size] | cols[i%Size] | boxes[box(i)])
		n := bits.OnesCount16(mask)
		if n == 0 {
			return nil
		}
		if n < width {
			cell, choices, width = i, mask, n
			if width == 1 {
				break
			}
		}
	}
	if cell < 0 {
		s.count++
		s.solution = b
		return nil
	}
	row, col, square := cell/Size, cell%Size, box(cell)
	for choices != 0 {
		choice := choices & -choices
		if s.random != nil {
			candidate := choices
			for range s.random.IntN(bits.OnesCount16(choices)) {
				candidate &= candidate - 1
			}
			choice = candidate & -candidate
		}
		choices &^= choice
		b[cell] = maskDigit(choice)
		rows[row] |= choice
		cols[col] |= choice
		boxes[square] |= choice
		if err := s.visit(b, rows, cols, boxes); err != nil || s.count >= s.limit {
			return err
		}
		rows[row] &^= choice
		cols[col] &^= choice
		boxes[square] &^= choice
	}
	return nil
}

func searchBoard(ctx context.Context, b Board, limit int, random *rand.Rand) (search, error) {
	s := search{ctx: ctx, limit: limit, random: random}
	var rows, cols, boxes [Size]uint16
	for i, digit := range b {
		if digit == 0 {
			continue
		}
		if digit > Size {
			return s, nil
		}
		mask := digitMask(digit)
		row, col, square := i/Size, i%Size, box(i)
		if (rows[row]|cols[col]|boxes[square])&mask != 0 {
			return s, nil
		}
		rows[row] |= mask
		cols[col] |= mask
		boxes[square] |= mask
	}
	err := s.visit(b, rows, cols, boxes)
	return s, err
}

// Unique proves a puzzle has exactly one solution.
func Unique(ctx context.Context, b Board) (bool, error) {
	s, err := searchBoard(ctx, b, 2, nil)
	return s.count == 1 && err == nil, err
}

type candidates [Cells]uint16

func (c *candidates) place(cell int, mask uint16) {
	c[cell] = 0
	for _, peer := range peers[cell] {
		c[peer] &^= mask
	}
}

// Rate classifies a valid, uniquely solvable puzzle by the techniques used.
// Restarting from the cheapest technique after every change avoids inflating
// the rating when an easier step becomes available.
func Rate(b Board) Difficulty {
	var c candidates
	for cell := range c {
		c[cell] = allDigits
	}
	remaining := Cells
	for cell, digit := range b {
		if digit != 0 {
			c.place(cell, digitMask(digit))
			remaining--
		}
	}
	rating := Easy
	for remaining > 0 {
		cell, mask := c.single()
		if cell >= 0 {
			c.place(cell, mask)
			remaining--
			continue
		}
		cell, mask = c.hiddenSingle()
		if cell >= 0 {
			if rating == Easy {
				rating = Medium
			}
			c.place(cell, mask)
			remaining--
			continue
		}
		if c.locked() {
			if rating != Expert {
				rating = Hard
			}
			continue
		}
		if c.subset() {
			rating = Expert
			continue
		}
		return Master
	}
	return rating
}

func (c *candidates) single() (int, uint16) {
	for i, mask := range c {
		if mask != 0 && mask&(mask-1) == 0 {
			return i, mask
		}
	}
	return -1, 0
}

func (c *candidates) hiddenSingle() (int, uint16) {
	for _, unit := range units {
		var seen, repeated uint16
		for _, cell := range unit {
			repeated |= seen & c[cell]
			seen |= c[cell]
		}
		unique := seen &^ repeated
		if unique != 0 {
			for _, cell := range unit {
				if found := unique & c[cell]; found != 0 {
					return cell, found & -found
				}
			}
		}
	}
	return -1, 0
}

func (c *candidates) locked() bool {
	for i, unit := range units {
		for digit := uint16(1); digit <= allDigits; digit <<= 1 {
			first := -1
			sameRow, sameCol, sameBox := true, true, true
			for _, cell := range unit {
				if c[cell]&digit == 0 {
					continue
				}
				if first < 0 {
					first = cell
				}
				sameRow = sameRow && cell/Size == first/Size
				sameCol = sameCol && cell%Size == first%Size
				sameBox = sameBox && box(cell) == box(first)
			}
			if first < 0 {
				continue
			}
			switch {
			case i >= 2*Size && sameRow:
				if c.remove(units[first/Size], unit, digit) {
					return true
				}
			case i >= 2*Size && sameCol:
				if c.remove(units[Size+first%Size], unit, digit) {
					return true
				}
			case i < 2*Size && sameBox:
				if c.remove(units[2*Size+box(first)], unit, digit) {
					return true
				}
			}
		}
	}
	return false
}

func (c *candidates) remove(target, excluded [Size]int, mask uint16) bool {
	for _, cell := range target {
		if c[cell]&mask == 0 {
			continue
		}
		found := false
		for _, keep := range excluded {
			if cell == keep {
				found = true
				break
			}
		}
		if !found {
			c[cell] &^= mask
			return true
		}
	}
	return false
}

func (c *candidates) subset() bool {
	for _, unit := range units {
		for i, a := range unit {
			if size := bits.OnesCount16(c[a]); size < 2 || size > 3 {
				continue
			}
			for j := i + 1; j < Size; j++ {
				b := unit[j]
				if c[b] == 0 {
					continue
				}
				mask := c[a] | c[b]
				size := bits.OnesCount16(mask)
				if size == 2 && c.removeSubset(unit, mask, a, b, -1) {
					return true
				}
				if size > 3 {
					continue
				}
				for _, cell := range unit[j+1:] {
					if c[cell] == 0 {
						continue
					}
					triple := mask | c[cell]
					if bits.OnesCount16(triple) == 3 && c.removeSubset(unit, triple, a, b, cell) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (c *candidates) removeSubset(unit [Size]int, mask uint16, a, b, third int) bool {
	for _, cell := range unit {
		if cell != a && cell != b && cell != third && c[cell]&mask != 0 {
			c[cell] &^= mask
			return true
		}
	}
	return false
}

// Generate removes randomly ordered clues from a freshly generated solution,
// retaining removals only when uniqueness is proven.
func Generate(ctx context.Context, difficulty Difficulty, random *rand.Rand) (Board, error) {
	if !difficulty.Valid() {
		return Board{}, ErrGeneration
	}
	for range 128 {
		s, err := searchBoard(ctx, Board{}, 1, random)
		if err != nil {
			return Board{}, err
		}
		givens := s.solution
		removed := 0
		for _, cell := range random.Perm(Cells) {
			digit := givens[cell]
			givens[cell] = 0
			unique, err := Unique(ctx, givens)
			if err != nil && !errors.Is(err, ErrGeneration) {
				return Board{}, err
			}
			if !unique {
				givens[cell] = digit
				continue
			}
			removed++
			if removed >= 39 && Rate(givens) == difficulty {
				return givens, nil
			}
		}
	}
	return Board{}, ErrGeneration
}
