package util

import "strings"

// CapitalizeFirst returns s with the first letter uppercased.
func CapitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// RemoveFromCSV removes a value from a comma-separated list.
func RemoveFromCSV(list, value string) string {
	parts := strings.Split(list, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && p != value {
			result = append(result, p)
		}
	}
	return strings.Join(result, ",")
}
