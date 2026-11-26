// Package board implements Sudoku boards, validation and puzzle generation.
package board

import (
	"errors"
	"math/bits"
	"strconv"
)

const (
	Size             = 9
	Cells            = Size * Size
	allDigits uint16 = (1 << Size) - 1
)

type Board [Cells]uint8
type Notes [Cells]uint16

func Parse(s string) (Board, error) {
	var b Board
	if len(s) != Cells {
		return b, errors.New("board must contain 81 digits")
	}
	for i := range b {
		if s[i] < '0' || s[i] > '9' {
			return Board{}, errors.New("board contains an invalid digit")
		}
		b[i] = s[i] - '0'
	}
	return b, nil
}

func (b Board) String() string {
	var out [Cells]byte
	for i, digit := range b {
		out[i] = '0' + digit
	}
	return string(out[:])
}

func (b Board) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

func (b *Board) UnmarshalText(data []byte) error {
	parsed, err := Parse(string(data))
	if err == nil {
		*b = parsed
	}
	return err
}

func (b Board) ValidEdits(givens Board, notes Notes) bool {
	for i, digit := range b {
		if digit > Size || givens[i] != 0 && digit != givens[i] || notes[i] > allDigits || digit != 0 && notes[i] != 0 {
			return false
		}
	}
	return true
}

// Solved requires all rows, columns and boxes to contain every digit once.
func (b Board) Solved() bool {
	for _, unit := range units {
		var seen uint16
		for _, cell := range unit {
			digit := b[cell]
			if digit < 1 || digit > Size {
				return false
			}
			seen |= digitMask(digit)
		}
		if seen != allDigits {
			return false
		}
	}
	return true
}

type Difficulty string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
	Expert Difficulty = "expert"
	Master Difficulty = "master"
)

func Difficulties() [5]Difficulty { return [5]Difficulty{Easy, Medium, Hard, Expert, Master} }

func (d Difficulty) Valid() bool {
	for _, difficulty := range Difficulties() {
		if d == difficulty {
			return true
		}
	}
	return false
}

func (d Difficulty) Title() string {
	if !d.Valid() {
		return ""
	}
	s := string(d)
	return string(s[0]-'a'+'A') + s[1:]
}

func (n Notes) MarshalText() ([]byte, error) {
	out := make([]byte, 0, Cells*2)
	for _, mask := range n {
		out = append(out, noteAlphabet[(mask>>6)&63], noteAlphabet[mask&63])
	}
	return out, nil
}

const noteAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_"

func (n *Notes) UnmarshalText(data []byte) error {
	if len(data) != Cells*2 {
		return errors.New("notes must contain 81 masks")
	}
	var parsed Notes
	for i := range parsed {
		hi, lo := noteIndex[data[i*2]], noteIndex[data[i*2+1]]
		if hi < 1 || hi > 8 || lo < 1 {
			return errors.New("invalid notes mask at cell " + strconv.Itoa(i))
		}
		parsed[i] = uint16(hi-1)<<6 | uint16(lo-1)
	}
	*n = parsed
	return nil
}

var noteIndex = func() (out [256]byte) {
	for i := range len(noteAlphabet) {
		out[noteAlphabet[i]] = byte(i + 1)
	}
	return out
}()

func digitMask(digit uint8) uint16 { return 1 << (digit - 1) }
func maskDigit(mask uint16) uint8  { return uint8(bits.TrailingZeros16(mask) + 1) }
func box(cell int) int             { return (cell/Size/3)*3 + (cell%Size)/3 }

var units = func() (out [27][Size]int) {
	for cell := range Cells {
		row, col := cell/Size, cell%Size
		out[row][col] = cell
		out[Size+col][row] = cell
		out[2*Size+box(cell)][row%3*3+col%3] = cell
	}
	return out
}()

var peers = func() (out [Cells][20]int) {
	for cell := range Cells {
		index := 0
		for other := range Cells {
			if cell != other && (cell/Size == other/Size || cell%Size == other%Size || box(cell) == box(other)) {
				out[cell][index] = other
				index++
			}
		}
	}
	return out
}()
