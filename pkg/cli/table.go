package cli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ansiRe matches ANSI escape sequences for stripping when calculating visual width.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visualLen returns the display width of s, excluding ANSI escape codes
// and counting Unicode runes (not bytes) for correct multi-byte character width.
func visualLen(s string) int {
	return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, ""))
}

// terminalWidth returns the terminal column count for stdout.
// COLUMNS environment variable overrides the detected width.
// Returns 0 if stdout is not a terminal and COLUMNS is unset,
// which signals that no width constraint should be applied.
func terminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0 // not a terminal — no constraint
	}
	return w
}

// Table produces column-aligned output with ANSI-aware width calculation.
// Headers and a dash divider are written lazily on Flush(),
// so empty tables produce no output.
//
// When stdout is a terminal (or COLUMNS is set), output is constrained to
// the terminal width. Columns that would exceed their share are word-wrapped
// within the same column across multiple physical lines.
type Table struct {
	headers []string
	rows    [][]string
	prefix  string
}

// NewTable creates a table with the given column headers.
func NewTable(headers ...string) *Table {
	return &Table{headers: headers}
}

// WithPrefix sets a string prepended to every line (headers, divider, rows).
// Useful for indenting sub-tables within larger output.
func (t *Table) WithPrefix(prefix string) *Table {
	t.prefix = prefix
	return t
}

// Row appends a row to the table.
func (t *Table) Row(values ...string) {
	t.rows = append(t.rows, values)
}

// Flush writes all buffered output. If no rows were added, nothing is printed.
func (t *Table) Flush() {
	if len(t.rows) == 0 {
		return
	}

	// Compute natural column widths from headers and all rows.
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = visualLen(h)
	}
	for _, row := range t.rows {
		for i, v := range row {
			if i < len(widths) {
				if vl := visualLen(v); vl > widths[i] {
					widths[i] = vl
				}
			}
		}
	}

	// Constrain to terminal width when applicable.
	if tw := terminalWidth(); tw > 0 {
		widths = capWidths(widths, t.headers, tw, visualLen(t.prefix))
	}

	// Print headers.
	t.printRow(t.headers, widths)

	// Print divider (exactly widths[i] dashes per column — never wraps).
	dividers := make([]string, len(t.headers))
	for i := range t.headers {
		dividers[i] = strings.Repeat("-", widths[i])
	}
	t.printRow(dividers, widths)

	// Print data rows (with wrapping).
	for _, row := range t.rows {
		t.printRow(row, widths)
	}
}

// capWidths reduces column widths so that the total line length fits within
// termWidth. Columns are never shrunk below their header width.
// prefixLen is the visual length of the per-row prefix string.
func capWidths(widths []int, headers []string, termWidth, prefixLen int) []int {
	result := make([]int, len(widths))
	copy(result, widths)

	minWidths := make([]int, len(headers))
	for i, h := range headers {
		minWidths[i] = visualLen(h)
	}

	const colGap = 2 // 2-space gap between adjacent columns

	for {
		// Compute current total line width.
		lineWidth := prefixLen
		for _, w := range result {
			lineWidth += w
		}
		if len(result) > 1 {
			lineWidth += colGap * (len(result) - 1)
		}
		if lineWidth <= termWidth {
			break
		}

		// Find the widest column that can still be reduced.
		maxW, maxI := -1, -1
		for i, w := range result {
			if w > minWidths[i] && w > maxW {
				maxW = w
				maxI = i
			}
		}
		if maxI < 0 {
			break // every column is at its minimum — cannot reduce further
		}

		// Reduce that column by the minimum of: excess needed and available reduction.
		excess := lineWidth - termWidth
		available := result[maxI] - minWidths[maxI]
		if excess > available {
			excess = available
		}
		result[maxI] -= excess
	}

	return result
}

// wrapCell splits s into lines no wider than width visual characters.
// If s fits within width, it is returned unchanged (ANSI codes preserved).
// Otherwise ANSI codes are stripped and the plain text is word-wrapped,
// hard-breaking any single word that exceeds width on its own.
func wrapCell(s string, width int) []string {
	if width <= 0 || visualLen(s) <= width {
		return []string{s}
	}

	// Strip ANSI codes for wrapping. In practice only cells that are plain
	// text (REQUIRES, DURATION, SCENARIO) ever need wrapping.
	plain := ansiRe.ReplaceAllString(s, "")

	var lines []string
	var cur []rune
	curLen := 0

	flush := func() {
		lines = append(lines, string(cur))
		cur = cur[:0]
		curLen = 0
	}

	for _, word := range strings.Fields(plain) {
		wRunes := []rune(word)
		wLen := len(wRunes)

		if curLen == 0 {
			// Place this word at the start of the current line,
			// hard-breaking if the word itself exceeds width.
			for len(wRunes) > 0 {
				take := len(wRunes)
				if take > width {
					take = width
				}
				cur = append(cur, wRunes[:take]...)
				curLen += take
				wRunes = wRunes[take:]
				if len(wRunes) > 0 {
					flush()
				}
			}
		} else if curLen+1+wLen <= width {
			// Word fits on the current line.
			cur = append(cur, ' ')
			cur = append(cur, wRunes...)
			curLen += 1 + wLen
		} else {
			// Word doesn't fit — start a new line and retry.
			flush()
			for len(wRunes) > 0 {
				take := len(wRunes)
				if take > width {
					take = width
				}
				cur = append(cur, wRunes[:take]...)
				curLen += take
				wRunes = wRunes[take:]
				if len(wRunes) > 0 {
					flush()
				}
			}
		}
	}
	if curLen > 0 {
		flush()
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// printRow prints a logical row. If any cell exceeds its column width after
// word-wrapping, the row spans multiple physical output lines.
func (t *Table) printRow(row []string, widths []int) {
	// Wrap each cell into one or more lines.
	allLines := make([][]string, len(widths))
	maxLines := 1
	for i := range widths {
		val := ""
		if i < len(row) {
			val = row[i]
		}
		wrapped := wrapCell(val, widths[i])
		allLines[i] = wrapped
		if len(wrapped) > maxLines {
			maxLines = len(wrapped)
		}
	}

	for l := 0; l < maxLines; l++ {
		parts := make([]string, len(widths))
		for i := range widths {
			val := ""
			if l < len(allLines[i]) {
				val = allLines[i][l]
			}
			pad := widths[i] - visualLen(val)
			if pad < 0 {
				pad = 0
			}
			parts[i] = val + strings.Repeat(" ", pad)
		}
		fmt.Fprintln(os.Stdout, t.prefix+strings.TrimRight(strings.Join(parts, "  "), " "))
	}
}
