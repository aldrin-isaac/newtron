package newtlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// PatchProfiles updates device profile JSON files with newtlab runtime values.
// Called after successful VM deployment.
//
// Uses spec.DeviceProfile for type-safe reading/patching instead of untyped
// map[string]interface{}, since the profile JSON structure matches DeviceProfile.
func PatchProfiles(lab *Lab) error {
	for name, node := range lab.Nodes {
		profilePath := filepath.Join(lab.SpecDir, "profiles", name+".json")

		data, err := os.ReadFile(profilePath)
		if err != nil {
			return fmt.Errorf("newtlab: reading profile %s: %w", name, err)
		}

		var profile spec.DeviceProfile
		if err := json.Unmarshal(data, &profile); err != nil {
			return fmt.Errorf("newtlab: parsing profile %s: %w", name, err)
		}

		// Save original mgmt_ip for restore on destroy
		if nodeState, ok := lab.State.Nodes[name]; ok {
			if profile.MgmtIP != "" {
				nodeState.OriginalMgmtIP = profile.MgmtIP
			}
		}

		// Patch fields — use host IP for remote nodes, 127.0.0.1 for local
		mgmtIP := "127.0.0.1"
		if nodeState, ok2 := lab.State.Nodes[name]; ok2 && nodeState.HostIP != "" {
			mgmtIP = nodeState.HostIP
		}
		profile.MgmtIP = mgmtIP
		profile.SSHPort = node.SSHPort
		profile.ConsolePort = node.ConsolePort
		if node.SSHUser != "" && profile.SSHUser == "" {
			profile.SSHUser = node.SSHUser
		}
		if node.SSHPass != "" && profile.SSHPass == "" {
			profile.SSHPass = node.SSHPass
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

		var profile spec.DeviceProfile
		if err := json.Unmarshal(data, &profile); err != nil {
			return fmt.Errorf("newtlab: parsing profile %s: %w", name, err)
		}

		// Restore original mgmt_ip
		if nodeState, ok := lab.State.Nodes[name]; ok && nodeState.OriginalMgmtIP != "" {
			profile.MgmtIP = nodeState.OriginalMgmtIP
		}

		// Remove newtlab-written fields by zeroing them.
		// ssh_user and ssh_pass are intentionally NOT removed: they may
		// have been present before PatchProfiles (user-configured) and
		// PatchProfiles only writes them when absent, so removing them
		// here could discard user-set credentials.
		profile.SSHPort = 0
		profile.ConsolePort = 0

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
