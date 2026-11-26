// Package web renders Sudoku's embedded pages and local assets.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/razzie-cloud/sudoku/internal/board"
)

//go:embed templates.html static
var assets embed.FS

var templates = template.Must(template.New("sudoku").Funcs(template.FuncMap{
	"noteDigits": noteDigits,
	"cellLabel":  cellLabel,
	"tileStatus": tileStatus,
}).ParseFS(assets, "templates.html"))

func Render(out io.Writer, view any) error { return templates.ExecuteTemplate(out, "page", view) }
func Static() fs.FS                        { f, _ := fs.Sub(assets, "static"); return f }

func noteDigits(mask uint16) [board.Size]string {
	var digits [board.Size]string
	for i := range digits {
		if mask&(1<<i) != 0 {
			digits[i] = strconv.Itoa(i + 1)
		}
	}
	return digits
}

func cellLabel(cell int, clue, digit uint8, notes uint16) string {
	label := fmt.Sprintf("Row %d, column %d", cell/board.Size+1, cell%board.Size+1)
	switch {
	case clue != 0:
		return fmt.Sprintf("%s, clue %d", label, clue)
	case digit != 0:
		return fmt.Sprintf("%s, %d", label, digit)
	case notes != 0:
		var marked []string
		for _, digit := range noteDigits(notes) {
			if digit != "" {
				marked = append(marked, digit)
			}
		}
		return label + ", pencil marks " + strings.Join(marked, ", ")
	default:
		return label + ", empty"
	}
}

func tileStatus(status string) string {
	switch status {
	case "completed":
		return "Completed"
	case "in-progress":
		return "In progress"
	default:
		return "Unstarted"
	}
}
