package util

import "strings"

// SplitCommaSeparated splits a comma-separated string and trims whitespace from each element.
// Empty input returns nil.
func SplitCommaSeparated(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// CapitalizeFirst returns s with the first letter uppercased.
func CapitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// SanitizeName replaces non-alphanumeric chars with hyphens for config key names.
func SanitizeName(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}

// AddToCSV adds a value to a comma-separated list if not already present.
// Returns the value itself if the list is empty.
func AddToCSV(list, value string) string {
	if list == "" {
		return value
	}
	parts := strings.Split(list, ",")
	for _, p := range parts {
		if strings.TrimSpace(p) == value {
			return list // Already in list
		}
	}
	return list + "," + value
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
