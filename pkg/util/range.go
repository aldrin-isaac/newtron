package util

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ExpandRange expands a range specification into individual values
// Supports formats like:
//   - "1-5" -> [1, 2, 3, 4, 5]
//   - "1,3,5" -> [1, 3, 5]
//   - "1-3,5,7-9" -> [1, 2, 3, 5, 7, 8, 9]
//   - "0-1:1-40" -> [(0,1), (0,2), ..., (1,40)] for slot:port notation
func ExpandRange(spec string) ([]int, error) {
	if spec == "" {
		return nil, nil
	}

	var result []int
	parts := strings.Split(spec, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			// Range: "1-5"
			rangeParts := strings.SplitN(part, "-", 2)
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range format: %s", part)
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid start value in range %s: %v", part, err)
			}

			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid end value in range %s: %v", part, err)
			}

			if start > end {
				return nil, fmt.Errorf("start value %d greater than end value %d in range %s", start, end, part)
			}

			for i := start; i <= end; i++ {
				result = append(result, i)
			}
		} else {
			// Single value
			val, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %s", part)
			}
			result = append(result, val)
		}
	}

	// Sort and deduplicate
	sort.Ints(result)
	return dedupInts(result), nil
}

// ExpandSlotPortRange expands a slot:port range specification
// Format: "slot-range:port-range" e.g., "0-1:1-40"
// Returns pairs of (slot, port)
func ExpandSlotPortRange(spec string) ([][2]int, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid slot:port format: %s (expected 'slot-range:port-range')", spec)
	}

	slots, err := ExpandRange(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid slot range: %v", err)
	}

	ports, err := ExpandRange(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid port range: %v", err)
	}

	var result [][2]int
	for _, slot := range slots {
		for _, port := range ports {
			result = append(result, [2]int{slot, port})
		}
	}

	return result, nil
}

// CompactRange compacts a list of integers into range notation
// [1, 2, 3, 5, 7, 8, 9] -> "1-3,5,7-9"
func CompactRange(values []int) string {
	if len(values) == 0 {
		return ""
	}

	// Sort and deduplicate
	sorted := make([]int, len(values))
	copy(sorted, values)
	sort.Ints(sorted)
	sorted = dedupInts(sorted)

	var parts []string
	start := sorted[0]
	end := sorted[0]

	for i := 1; i < len(sorted); i++ {
		if sorted[i] == end+1 {
			end = sorted[i]
		} else {
			parts = append(parts, formatRange(start, end))
			start = sorted[i]
			end = sorted[i]
		}
	}
	parts = append(parts, formatRange(start, end))

	return strings.Join(parts, ",")
}

func formatRange(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func dedupInts(sorted []int) []int {
	if len(sorted) == 0 {
		return sorted
	}
	result := []int{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			result = append(result, sorted[i])
		}
	}
	return result
}

// ExpandInterfaceRange expands interface range notation
// "Ethernet0-4" -> ["Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3", "Ethernet4"]
// "Ethernet0,4,8" -> ["Ethernet0", "Ethernet4", "Ethernet8"]
func ExpandInterfaceRange(spec string) ([]string, error) {
	// Find where the prefix ends and numbers begin
	prefixEnd := 0
	for i, c := range spec {
		if c >= '0' && c <= '9' {
			prefixEnd = i
			break
		}
	}

	if prefixEnd == 0 {
		return nil, fmt.Errorf("invalid interface range: %s (no prefix found)", spec)
	}

	prefix := spec[:prefixEnd]
	numPart := spec[prefixEnd:]

	nums, err := ExpandRange(numPart)
	if err != nil {
		return nil, fmt.Errorf("invalid interface range %s: %v", spec, err)
	}

	result := make([]string, len(nums))
	for i, n := range nums {
		result[i] = fmt.Sprintf("%s%d", prefix, n)
	}

	return result, nil
}

// ExpandVLANRange expands VLAN range notation
// "100-105,200" -> [100, 101, 102, 103, 104, 105, 200]
func ExpandVLANRange(spec string) ([]int, error) {
	vlans, err := ExpandRange(spec)
	if err != nil {
		return nil, err
	}

	// Validate VLAN IDs
	for _, vlan := range vlans {
		if err := ValidateVLANID(vlan); err != nil {
			return nil, err
		}
	}

	return vlans, nil
}
