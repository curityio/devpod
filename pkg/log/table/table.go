package table

import (
	"runtime"
	"strings"
	"unicode"

	"github.com/loft-sh/devpod/pkg/log"
	"github.com/mattn/go-runewidth"
	"github.com/sirupsen/logrus"
)

// maxCellWidth is the width cells are wrapped at, unless they have longer words
const maxCellWidth = 30

// PrintTable prints a table with header columns and string values
func PrintTable(s log.Logger, header []string, values [][]string) {
	colorHeader := runtime.GOOS == "darwin" || runtime.GOOS == "linux"

	s.WriteString(logrus.InfoLevel, "  \n")
	for _, line := range render(header, values, colorHeader) {
		s.WriteString(logrus.InfoLevel, "  "+line+"\n")
	}
	s.WriteString(logrus.InfoLevel, "  \n")
}

// render lays out a table without outer borders, with columns separated by "|" and the
// header, which is centered and in upper case, separated from the values by a line.
// Values are left aligned and wrapped at maxCellWidth.
func render(header []string, values [][]string, colorHeader bool) []string {
	widths := make([]int, len(header))
	headerCells := make([]string, len(header))
	for i, h := range header {
		headerCells[i] = title(h)
		widths[i] = runewidth.StringWidth(headerCells[i])
	}

	rows := make([][][]string, 0, len(values))
	for _, row := range values {
		cells := make([][]string, len(header))
		for i := range header {
			if i < len(row) {
				cells[i] = wrap(row[i])
			} else {
				cells[i] = []string{""}
			}
			for _, line := range cells[i] {
				widths[i] = max(widths[i], runewidth.StringWidth(line))
			}
		}
		rows = append(rows, cells)
	}

	lines := []string{}
	columns := make([]string, len(header))
	for i, h := range headerCells {
		columns[i] = center(h, widths[i])
		if colorHeader {
			columns[i] = "\x1b[32m" + columns[i] + "\x1b[0m"
		}
	}
	lines = append(lines, joinColumns(columns))

	for i, w := range widths {
		columns[i] = strings.Repeat("-", w+2)
	}
	lines = append(lines, "-"+strings.Join(columns, "+")+"-")

	for _, cells := range rows {
		height := 0
		for _, cell := range cells {
			height = max(height, len(cell))
		}
		for l := 0; l < height; l++ {
			for i, cell := range cells {
				line := ""
				if l < len(cell) {
					line = cell[l]
				}
				columns[i] = runewidth.FillRight(line, widths[i])
			}
			lines = append(lines, joinColumns(columns))
		}
	}
	return lines
}

func joinColumns(columns []string) string {
	return "  " + strings.Join(columns, " | ") + "  "
}

func center(s string, width int) string {
	gap := width - runewidth.StringWidth(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
}

// title turns a header into upper case, replacing "_" and "." between words with spaces.
func title(s string) string {
	rs := []rune(s)
	for i, r := range rs {
		switch r {
		case '_':
			rs[i] = ' '
		case '.':
			// keep the dot of numbers like 1.2
			if (i > 0 && !isNumOrSpace(rs[i-1])) || (i < len(rs)-1 && !isNumOrSpace(rs[i+1])) {
				rs[i] = ' '
			}
		}
	}
	return strings.ToUpper(strings.TrimSpace(string(rs)))
}

func isNumOrSpace(r rune) bool {
	return unicode.IsDigit(r) || unicode.IsSpace(r)
}

// wrap splits a cell into lines of up to maxCellWidth columns, or the width of the cell's longest
// line if shorter, breaking at spaces. Words longer than that get a line of their own.
func wrap(s string) []string {
	paragraphs := strings.Split(s, "\n")
	limit := 0
	for _, p := range paragraphs {
		limit = max(limit, runewidth.StringWidth(p))
	}
	limit = min(limit, maxCellWidth)

	lines := []string{}
	line, lineWidth := "", 0
	for _, word := range strings.Split(strings.Join(paragraphs, " "), " ") {
		wordWidth := runewidth.StringWidth(word)
		switch {
		case lineWidth == 0 && line == "":
			line, lineWidth = word, wordWidth
		case lineWidth+1+wordWidth <= limit:
			line, lineWidth = line+" "+word, lineWidth+1+wordWidth
		default:
			lines = append(lines, line)
			line, lineWidth = word, wordWidth
		}
	}
	return append(lines, line)
}
