package cli

import (
	"reflect"
	"testing"
)

func TestCapWidths_NoConstraint(t *testing.T) {
	widths := []int{5, 20, 10}
	headers := []string{"COL1", "COL2", "COL3"}
	// Total: 5+20+10 + 2*2 + prefix 0 = 39; fits in 80-col terminal.
	got := capWidths(widths, headers, 80, 0)
	if !reflect.DeepEqual(got, widths) {
		t.Errorf("expected no change: got %v, want %v", got, widths)
	}
}

func TestCapWidths_ReducesWidest(t *testing.T) {
	// 5 + 60 + 10 + 2*2 = 79 → just over 78
	widths := []int{5, 60, 10}
	headers := []string{"NUM", "SCENARIO", "STATUS"}
	got := capWidths(widths, headers, 78, 0)
	// Total should now be <= 78
	total := 0
	for _, w := range got {
		total += w
	}
	total += 2 * (len(got) - 1)
	if total > 78 {
		t.Errorf("total %d still exceeds 78; widths=%v", total, got)
	}
	// Widest column (index 1) should have been reduced; others unchanged.
	if got[0] != widths[0] {
		t.Errorf("column 0 should be unchanged: got %d, want %d", got[0], widths[0])
	}
	if got[2] != widths[2] {
		t.Errorf("column 2 should be unchanged: got %d, want %d", got[2], widths[2])
	}
}

func TestCapWidths_RespectsHeaderMinimum(t *testing.T) {
	widths := []int{4, 60}
	headers := []string{"NUM", "A-VERY-LONG-HEADER-NAME"}
	// minWidths = [3, 23]; terminal is tiny at 30 cols.
	got := capWidths(widths, headers, 30, 2) // prefix=2
	// Column 1 must not go below len("A-VERY-LONG-HEADER-NAME")=23.
	if got[1] < visualLen("A-VERY-LONG-HEADER-NAME") {
		t.Errorf("column 1 reduced below header minimum: got %d", got[1])
	}
}

func TestCapWidths_CannotReduceFurther(t *testing.T) {
	// All columns already at their header minimum; terminal too narrow.
	widths := []int{3, 8}
	headers := []string{"NUM", "SCENARIO"}
	// 3+8+2 = 13; terminal width = 5 (impossibly narrow).
	got := capWidths(widths, headers, 5, 0)
	// Should not go below minimums, even if that means exceeding terminal.
	if got[0] < visualLen("NUM") {
		t.Errorf("column 0 below header minimum: %d", got[0])
	}
	if got[1] < visualLen("SCENARIO") {
		t.Errorf("column 1 below header minimum: %d", got[1])
	}
}

func TestWrapCell_FitsUnchanged(t *testing.T) {
	got := wrapCell("hello", 10)
	if !reflect.DeepEqual(got, []string{"hello"}) {
		t.Errorf("got %v, want [hello]", got)
	}
}

func TestWrapCell_ExactFit(t *testing.T) {
	got := wrapCell("hello", 5)
	if !reflect.DeepEqual(got, []string{"hello"}) {
		t.Errorf("got %v, want [hello]", got)
	}
}

func TestWrapCell_WordWrap(t *testing.T) {
	// "hello world foo" wrapped at 11: "hello world" (11), "foo" (3)
	got := wrapCell("hello world foo", 11)
	want := []string{"hello world", "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrapCell_HardBreakLongWord(t *testing.T) {
	// Single word longer than width — hard-break at width.
	got := wrapCell("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrapCell_StepProgress(t *testing.T) {
	// Typical DURATION cell: "step 2/12: verify-bgp-sessions"
	got := wrapCell("step 2/12: verify-bgp-sessions", 20)
	// "step 2/12: verify-b" is 20, but word wrap splits at spaces:
	// line1: "step 2/12:" (10), then "verify-bgp-sessions" (19) fits on line2.
	if len(got) < 2 {
		t.Fatalf("expected wrapping: got %v", got)
	}
	for _, line := range got {
		if visualLen(line) > 20 {
			t.Errorf("line %q exceeds width 20 (len=%d)", line, visualLen(line))
		}
	}
}

func TestWrapCell_ANSIPreservedWhenFits(t *testing.T) {
	colored := "\x1b[32mPASS\x1b[0m" // green PASS
	got := wrapCell(colored, 10)
	if !reflect.DeepEqual(got, []string{colored}) {
		t.Errorf("ANSI string should be returned unchanged when it fits: got %v", got)
	}
}

func TestWrapCell_EmptyString(t *testing.T) {
	got := wrapCell("", 10)
	if !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("got %v, want [\"\"]", got)
	}
}

func TestWrapCell_MultiWordExactBoundary(t *testing.T) {
	// "aa bb cc" at width 5: "aa bb" (5), "cc" (2)
	got := wrapCell("aa bb cc", 5)
	want := []string{"aa bb", "cc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
