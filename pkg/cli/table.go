package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// Table wraps text/tabwriter with consistent column-aligned output.
// Headers and a dash divider are written lazily on first Row() or Flush(),
// so empty tables produce no output.
type Table struct {
	w       *tabwriter.Writer
	headers []string
	prefix  string
	written bool
}

// NewTable creates a table with the given column headers.
func NewTable(headers ...string) *Table {
	return &Table{
		w:       tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0),
		headers: headers,
	}
}

// WithPrefix sets a string prepended to each line (headers, divider, rows).
// Useful for indenting sub-tables within larger output.
func (t *Table) WithPrefix(prefix string) *Table {
	t.prefix = prefix
	return t
}

// Row writes a tab-separated row. On the first call, headers and divider
// are emitted before the row.
func (t *Table) Row(values ...string) {
	t.ensureHeaders()
	fmt.Fprintln(t.w, t.prefix+strings.Join(values, "\t"))
}

// Flush writes any buffered output. If no rows were written, nothing is printed.
func (t *Table) Flush() {
	if !t.written {
		return
	}
	t.w.Flush()
}

func (t *Table) ensureHeaders() {
	if t.written {
		return
	}
	t.written = true
	fmt.Fprintln(t.w, t.prefix+strings.Join(t.headers, "\t"))
	dividers := make([]string, len(t.headers))
	for i, h := range t.headers {
		dividers[i] = strings.Repeat("-", len(h))
	}
	fmt.Fprintln(t.w, t.prefix+strings.Join(dividers, "\t"))
}
