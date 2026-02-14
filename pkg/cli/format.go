// Package cli provides shared formatting helpers for newtron CLI tools.
package cli

import "strings"

// ANSI color helpers

func Green(s string) string  { return "\033[32m" + s + "\033[0m" }
func Yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func Red(s string) string    { return "\033[31m" + s + "\033[0m" }
func Bold(s string) string   { return "\033[1m" + s + "\033[0m" }
func Dim(s string) string    { return "\033[2m" + s + "\033[0m" }

// DotPad pads name with dots to the given width.
// Example: DotPad("boot-ssh", 30) → "boot-ssh ......................"
func DotPad(name string, width int) string {
	if width <= 0 || len(name) >= width-1 {
		return name
	}
	dots := width - len(name) - 1
	return name + " " + strings.Repeat(".", dots)
}
