// cmd_platform_generate.go — `newtron platform generate
// <sonic-platform.json>` (issue #185). Wraps
// spec.FromSONiCPlatformJSON with operator-friendly flag handling
// and three output modes:
//
//   - stdout (default): emit the derived PlatformSpec as a one-key
//     map ready to paste into a platforms.json `platforms` block.
//   - --output FILE.json (file doesn't exist): write a fresh
//     PlatformSpecFile containing only this entry.
//   - --output FILE.json (file exists): merge the entry into the
//     existing `platforms` map. Same-name overwrite refuses
//     unless --force.
//
// The translation itself lives in pkg/newtron/spec — this file is
// the CLI I/O layer only (per DPN §28 file-level feature cohesion).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

var (
	platformGenerateName         string
	platformGenerateHWSKU        string
	platformGenerateDescription  string
	platformGenerateOutput       string
	platformGenerateDataplane    string
	platformGenerateForce        bool
	platformGeneratePortConfigINI string
)

var platformGenerateCmd = &cobra.Command{
	Use:   "generate <path-to-sonic-platform.json>",
	Short: "Generate a newtron PlatformSpec from a SONiC platform.json",
	Long: `Generate a newtron PlatformSpec by parsing a SONiC device-tree platform.json
(typically /usr/share/sonic/device/<vendor>-<platform>/platform.json on a real
switch). The translator reads the file's "interfaces" map and derives:

  - port_count    — number of "interfaces" entries
  - default_speed — the highest-rate 1xN mode across every port's breakout_modes
                    (the "headline" speed of the platform — the speed each port
                    runs at without a breakout split)
  - breakouts     — sorted union of every breakout_modes key across every port

HWSKU is required via --hwsku because SONiC platform.json does not carry it
(HWSKU lives in the sibling <hwsku>/ directory under the SONiC device tree).

What the generator does NOT derive (operator fills these in afterward):

  - vm_image / vm_memory / vm_cpus and the other VM/lab fields. Omit them for
    real-hardware platforms; add them by hand for simulator platforms (sonic-vs,
    ciscovs) per the per-image conventions documented in the platform's HOWTO.
  - dataplane unless supplied via --dataplane.
  - unsupported_features. This is a runtime-discovered property — populate it
    from suite outcomes when a feature reliably fails on the target.

Examples:

  # emit to stdout (default) — paste into platforms.json or POST to /create-platform
  newtron platform generate ./platform.json \
      --name cisco-8101-32x100g --hwsku Cisco-8101-32x100

  # write/merge into an existing platforms.json
  newtron platform generate ./platform.json \
      --name cisco-8101-32x100g --hwsku Cisco-8101-32x100 \
      --output specs/platforms.json

  # overwrite an existing same-named entry
  newtron platform generate ./platform.json \
      --name cisco-8101-32x100g --hwsku Cisco-8101-32x100 \
      --output specs/platforms.json --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if platformGenerateName == "" {
			return fmt.Errorf("--name is required")
		}
		if platformGenerateHWSKU == "" {
			return fmt.Errorf("--hwsku is required (SONiC platform.json does not carry HWSKU)")
		}
		ps, err := derivePlatformSpec(args[0], platformGeneratePortConfigINI, spec.SONiCImportOptions{
			HWSKU:       platformGenerateHWSKU,
			Description: platformGenerateDescription,
			Dataplane:   platformGenerateDataplane,
		})
		if err != nil {
			return err
		}
		if platformGenerateOutput == "" {
			return emitPlatformToStdout(platformGenerateName, ps)
		}
		return mergePlatformIntoFile(platformGenerateOutput, platformGenerateName, ps, platformGenerateForce)
	},
}

// derivePlatformSpec runs the two-step translation: try
// platform.json first, fall through to port_config.ini when the
// JSON is the older per-HWSKU convention (empty `interfaces` map,
// signaled by spec.ErrEmptyInterfaces).
//
// port_config.ini source priority:
//
//  1. explicit --port-config-ini PATH (always wins)
//  2. auto-discovery: <dir-of-platform.json>/<hwsku>/port_config.ini
//
// If both paths miss when the platform.json signals
// ErrEmptyInterfaces, the operator gets an error citing every
// path checked so the next move is obvious (supply the explicit
// flag, or download the missing fixture).
func derivePlatformSpec(jsonPath, explicitINIPath string, opts spec.SONiCImportOptions) (*spec.PlatformSpec, error) {
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", jsonPath, err)
	}
	ps, jsonErr := spec.FromSONiCPlatformJSON(jsonData, opts)
	if jsonErr == nil {
		return ps, nil
	}
	if !errors.Is(jsonErr, spec.ErrEmptyInterfaces) {
		return nil, jsonErr
	}
	// platform.json says "I'm the older convention — look at
	// port_config.ini." Find it.
	iniPath, err := resolvePortConfigINIPath(jsonPath, opts.HWSKU, explicitINIPath)
	if err != nil {
		return nil, err
	}
	iniData, err := os.ReadFile(iniPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", iniPath, err)
	}
	ps, err = spec.FromPortConfigINI(iniData, opts)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", iniPath, err)
	}
	fmt.Fprintf(os.Stderr, "(derived from %s — breakouts unavailable in this convention)\n", iniPath)
	return ps, nil
}

// resolvePortConfigINIPath picks the port_config.ini source. The
// explicit --port-config-ini flag wins outright; otherwise the
// auto-discovery probe is <dir-of-platform.json>/<hwsku>/port_config.ini.
// If neither resolves to an existing file, the error names both
// paths checked (or just the auto-discovery path if no flag was
// passed) so the operator's next action is obvious.
func resolvePortConfigINIPath(jsonPath, hwsku, explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("--port-config-ini %s: %w", explicit, err)
		}
		return explicit, nil
	}
	auto := filepath.Join(filepath.Dir(jsonPath), hwsku, "port_config.ini")
	if _, err := os.Stat(auto); err == nil {
		return auto, nil
	}
	return "", fmt.Errorf("platform.json signals the older per-HWSKU convention but no port_config.ini was found. "+
		"Auto-discovery looked at %s (sibling-of-platform.json + --hwsku + port_config.ini). "+
		"Pass --port-config-ini PATH to override the location.", auto)
}

// emitPlatformToStdout writes the generated PlatformSpec as a
// one-key map under "platforms" so the output can be pasted into a
// platforms.json file as-is. PlatformSpecFile.Platforms uses the
// same shape, so the wire-form is consistent (DPN §46).
func emitPlatformToStdout(name string, ps *spec.PlatformSpec) error {
	wrapper := map[string]*spec.PlatformSpec{name: ps}
	out, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding platform: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// mergePlatformIntoFile writes the generated PlatformSpec into the
// `platforms` map of platforms.json at path. Three cases:
//
//   - path doesn't exist → create a fresh PlatformSpecFile with
//     this one entry.
//   - path exists and parses as a PlatformSpecFile → merge the new
//     entry. Same-key conflict refuses unless force=true.
//   - path exists but doesn't parse → return the parse error to the
//     operator (don't silently overwrite a hand-authored file).
//
// Persistence goes through a temp+rename to match the SavePlatforms
// pattern (atomicity: a crash mid-write leaves either old or new in
// place, never half-written).
func mergePlatformIntoFile(path, name string, ps *spec.PlatformSpec, force bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	var pf spec.PlatformSpecFile
	if existing, readErr := os.ReadFile(abs); readErr == nil {
		if err := json.Unmarshal(existing, &pf); err != nil {
			return fmt.Errorf("parsing existing %s: %w (refusing to overwrite a non-platforms.json file)", abs, err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("reading %s: %w", abs, readErr)
	}
	if pf.Platforms == nil {
		pf.Platforms = make(map[string]*spec.PlatformSpec)
	}
	if pf.Version == "" {
		pf.Version = "1.0"
	}
	if _, exists := pf.Platforms[name]; exists && !force {
		return fmt.Errorf("platform %q already exists in %s; pass --force to overwrite", name, abs)
	}
	pf.Platforms[name] = ps
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding platforms file: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(abs), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), "platforms-*.json.tmp")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", filepath.Dir(abs), err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp: %w", err)
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, abs, err)
	}
	fmt.Fprintf(os.Stderr, "platform %q written to %s\n", name, abs)
	return nil
}

func init() {
	platformGenerateCmd.Flags().StringVar(&platformGenerateName, "name", "",
		"Platform key under the `platforms` map (e.g. \"cisco-8101-32x100g\"). Required.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateHWSKU, "hwsku", "",
		"HWSKU name (e.g. \"Cisco-8101-32x100\"). Required — SONiC platform.json doesn't carry HWSKU.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateDescription, "description", "",
		"Optional description (set on PlatformSpec.Description).")
	platformGenerateCmd.Flags().StringVar(&platformGenerateOutput, "output", "",
		"Write to this path instead of stdout. Merges into existing platforms.json or creates fresh.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateDataplane, "dataplane", "",
		"Optional dataplane hint (\"vpp\", \"barefoot\", \"\" for none).")
	platformGenerateCmd.Flags().BoolVar(&platformGenerateForce, "force", false,
		"With --output, overwrite a same-named platform entry. Without --force, conflicts refuse.")
	platformGenerateCmd.Flags().StringVar(&platformGeneratePortConfigINI, "port-config-ini", "",
		"Path to a SONiC port_config.ini (the older per-HWSKU convention). When set, used directly. "+
			"When unset, the CLI auto-discovers a sibling <hwsku>/port_config.ini iff platform.json's "+
			"interfaces map is empty.")
	platformCmd.AddCommand(platformGenerateCmd)
}
