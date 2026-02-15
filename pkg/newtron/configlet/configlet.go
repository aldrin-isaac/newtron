// Package configlet handles loading and processing baseline configuration templates.
package configlet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Configlet represents a baseline configuration template.
type Configlet struct {
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	Version     string                            `json:"version"`
	ConfigDB    map[string]map[string]interface{} `json:"config_db"`
	Variables   []string                          `json:"variables"`
}

// LoadConfiglet loads and parses a configlet JSON file from the given directory.
func LoadConfiglet(dir, name string) (*Configlet, error) {
	path := filepath.Join(dir, name+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading configlet %s: %w", name, err)
	}

	var c Configlet
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing configlet %s: %w", name, err)
	}

	return &c, nil
}

// ListConfiglets returns the names of all configlet JSON files in the directory.
func ListConfiglets(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading configlet directory %s: %w", dir, err)
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return names, nil
}
