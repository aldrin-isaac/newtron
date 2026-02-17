package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiRe matches ANSI escape sequences for stripping when calculating visual width.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visualLen returns the display width of s, excluding ANSI escape codes
// and counting Unicode runes (not bytes) for correct multi-byte character width.
func visualLen(s string) int {
	return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, ""))
}

// Table produces column-aligned output with ANSI-aware width calculation.
// Headers and a dash divider are written lazily on first Row() or Flush(),
// so empty tables produce no output.
type Table struct {
	headers []string
	rows    [][]string
	prefix  string
}

// NewTable creates a table with the given column headers.
func NewTable(headers ...string) *Table {
	return &Table{headers: headers}
}

// WithPrefix sets a string prepended to each line (headers, divider, rows).
// Useful for indenting sub-tables within larger output.
func (t *Table) WithPrefix(prefix string) *Table {
	t.prefix = prefix
	return t
}

// Row appends a row to the table.
func (t *Table) Row(values ...string) {
	t.rows = append(t.rows, values)
}

// Flush writes all buffered output. If no rows were written, nothing is printed.
func (t *Table) Flush() {
	if len(t.rows) == 0 {
		return
	}

	// Calculate column widths from headers and all rows.
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

	// Print headers.
	t.printRow(t.headers, widths)

	// Print divider.
	dividers := make([]string, len(t.headers))
	for i := range t.headers {
		dividers[i] = strings.Repeat("-", widths[i])
	}
	t.printRow(dividers, widths)

	// Print data rows.
	for _, row := range t.rows {
		t.printRow(row, widths)
	}
}

// printRow prints a single row, padding each cell to the calculated column width.
func (t *Table) printRow(row []string, widths []int) {
	parts := make([]string, len(widths))
	for i := range widths {
		val := ""
		if i < len(row) {
			val = row[i]
		}
		// Pad using visual width: add enough spaces so visual length matches column width.
		pad := widths[i] - visualLen(val)
		if pad < 0 {
			pad = 0
		}
		parts[i] = val + strings.Repeat(" ", pad)
	}
	// Join with 2-space gap, trim trailing spaces.
	fmt.Fprintln(os.Stdout, t.prefix+strings.TrimRight(strings.Join(parts, "  "), " "))
}
