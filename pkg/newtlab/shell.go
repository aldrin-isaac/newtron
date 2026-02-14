package newtlab

import "strings"

// shellQuote quotes a path for safe use in remote shell commands.
// Paths starting with ~/ preserve tilde expansion while quoting the rest.
// Other paths are fully single-quoted.
func shellQuote(path string) string {
	if strings.HasPrefix(path, "~/") {
		return "~/" + singleQuote(path[2:])
	}
	return singleQuote(path)
}

// singleQuote wraps a string in single quotes, escaping any embedded single quotes.
func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// quoteArgs shell-quotes each argument using singleQuote.
func quoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = singleQuote(arg)
	}
	return quoted
}
