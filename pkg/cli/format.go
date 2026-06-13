// Package cli provides shared formatting helpers for newtron CLI tools.
package cli

import (
	"os"
)

// colorEnabled is false when NO_COLOR env var is set (per no-color.org).
var colorEnabled = os.Getenv("NO_COLOR") == ""

// Green wraps s in ANSI green. Returns s unchanged when NO_COLOR is set.
func Green(s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

// Yellow wraps s in ANSI yellow. Returns s unchanged when NO_COLOR is set.
func Yellow(s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

// Red wraps s in ANSI red. Returns s unchanged when NO_COLOR is set.
func Red(s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

// Bold wraps s in ANSI bold. Returns s unchanged when NO_COLOR is set.
func Bold(s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

// Dim wraps s in ANSI dim. Returns s unchanged when NO_COLOR is set.
func Dim(s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

