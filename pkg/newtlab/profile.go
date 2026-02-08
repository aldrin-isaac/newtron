package newtlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PatchProfiles updates device profile JSON files with newtlab runtime values.
// Called after successful VM deployment.
func PatchProfiles(lab *Lab) error {
	for name, node := range lab.Nodes {
		profilePath := filepath.Join(lab.SpecDir, "profiles", name+".json")

		data, err := os.ReadFile(profilePath)
		if err != nil {
			return fmt.Errorf("newtlab: reading profile %s: %w", name, err)
		}

		var profile map[string]interface{}
		if err := json.Unmarshal(data, &profile); err != nil {
			return fmt.Errorf("newtlab: parsing profile %s: %w", name, err)
		}

		// Save original mgmt_ip for restore on destroy
		if nodeState, ok := lab.State.Nodes[name]; ok {
			if mgmt, ok := profile["mgmt_ip"].(string); ok {
				nodeState.OriginalMgmtIP = mgmt
			}
		}

		// Patch fields — use host IP for remote nodes, 127.0.0.1 for local
		mgmtIP := "127.0.0.1"
		if nodeState, ok2 := lab.State.Nodes[name]; ok2 && nodeState.HostIP != "" {
			mgmtIP = nodeState.HostIP
		}
		profile["mgmt_ip"] = mgmtIP
		profile["ssh_port"] = node.SSHPort
		profile["console_port"] = node.ConsolePort
		if node.SSHUser != "" {
			if _, exists := profile["ssh_user"]; !exists {
				profile["ssh_user"] = node.SSHUser
			}
		}
		if node.SSHPass != "" {
			if _, exists := profile["ssh_pass"]; !exists {
				profile["ssh_pass"] = node.SSHPass
			}
		}

		out, err := json.MarshalIndent(profile, "", "    ")
		if err != nil {
			return fmt.Errorf("newtlab: marshal profile %s: %w", name, err)
		}

		if err := os.WriteFile(profilePath, out, 0644); err != nil {
			return fmt.Errorf("newtlab: write profile %s: %w", name, err)
		}
	}
	return nil
}

// RestoreProfiles removes newtlab-written fields from profiles.
// Called during destroy to clean up.
func RestoreProfiles(lab *Lab) error {
	for name := range lab.State.Nodes {
		profilePath := filepath.Join(lab.SpecDir, "profiles", name+".json")

		data, err := os.ReadFile(profilePath)
		if err != nil {
			// Profile may have been manually removed; skip
			continue
		}

		var profile map[string]interface{}
		if err := json.Unmarshal(data, &profile); err != nil {
			return fmt.Errorf("newtlab: parsing profile %s: %w", name, err)
		}

		// Restore original mgmt_ip
		if nodeState, ok := lab.State.Nodes[name]; ok && nodeState.OriginalMgmtIP != "" {
			profile["mgmt_ip"] = nodeState.OriginalMgmtIP
		}

		// Remove newtlab-written fields
		delete(profile, "ssh_port")
		delete(profile, "console_port")

		out, err := json.MarshalIndent(profile, "", "    ")
		if err != nil {
			return fmt.Errorf("newtlab: marshal profile %s: %w", name, err)
		}

		if err := os.WriteFile(profilePath, out, 0644); err != nil {
			return fmt.Errorf("newtlab: write profile %s: %w", name, err)
		}
	}
	return nil
}
