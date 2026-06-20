// cmd_platform_generate.go — `newtron platform generate
// <sonic-platform.json>` (issue #185; updated for #257's global
// platforms registry). Wraps spec.FromSONiCPlatformJSON with
// operator-friendly flag handling and two output modes:
//
//   - stdout (default): emit a single PlatformSpec with its name
//     field set, ready to redirect into <--platforms-base>/<name>.json.
//   - --output-dir DIR: write DIR/<name>.json (one file per platform —
//     the global-registry layout post-#257). Refuses on existing
//     same-named file unless --force.
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
	platformGenerateName          string
	platformGenerateHWSKU         string
	platformGenerateDescription   string
	platformGenerateOutputDir     string
	platformGenerateDataplane     string
	platformGenerateForce         bool
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

Output shape matches the global-platforms layout (#257): one file per
platform, filename basename equal to the name field. Convention:

  - Single deployment variant for a HWSKU:  --name <HWSKU>          (e.g. --name Cisco-8101-32x100)
  - Multiple variants share a HWSKU:        --name <HWSKU>_<variant> (e.g. --name Force10-S6000_vs)
  - Non-SONiC platforms (no HWSKU):         --name <descriptive>     (e.g. --name vjunos-router)

What the generator does NOT derive (operator fills these in afterward):

  - vm_image / vm_memory / vm_cpus and the other VM/lab fields. Omit them for
    real-hardware platforms; add them by hand for simulator platforms
    (Force10-S6000_vs, cisco-p200-32x100-vs) per the per-image conventions
    documented in the platform's HOWTO.
  - dataplane unless supplied via --dataplane.
  - unsupported_features. This is a runtime-discovered property — populate it
    from suite outcomes when a feature reliably fails on the target.

Examples:

  # emit to stdout (default) — redirect into <--platforms-base>/<name>.json
  newtron platform generate ./platform.json \
      --name Cisco-8101-32x100 --hwsku Cisco-8101-32x100 \
      > platforms/Cisco-8101-32x100.json

  # write directly into the global platforms directory
  newtron platform generate ./platform.json \
      --name Cisco-8101-32x100 --hwsku Cisco-8101-32x100 \
      --output-dir platforms

  # overwrite an existing same-named file
  newtron platform generate ./platform.json \
      --name Cisco-8101-32x100 --hwsku Cisco-8101-32x100 \
      --output-dir platforms --force`,
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
		ps.Name = platformGenerateName
		if platformGenerateOutputDir == "" {
			return emitPlatformToStdout(ps)
		}
		return writePlatformToDir(platformGenerateOutputDir, ps, platformGenerateForce)
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
// standalone JSON document — the same shape <--platforms-base>/<name>.json
// expects on disk. Operators redirect to a file in that directory.
func emitPlatformToStdout(ps *spec.PlatformSpec) error {
	out, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding platform: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// writePlatformToDir writes the generated PlatformSpec to
// <dir>/<ps.Name>.json. Refuses to overwrite an existing file
// unless force=true (atomic temp+rename so a crash mid-write leaves
// either old or new in place, never half-written).
//
// The dir-as-target shape mirrors the global-platforms layout from
// #257: one file per platform identity, filename basename equal to
// the name field. cmd/newt-server's --platforms-base points at this
// same directory; the loader enforces the filename invariant.
func writePlatformToDir(dir string, ps *spec.PlatformSpec, force bool) error {
	if ps.Name == "" {
		return fmt.Errorf("internal: PlatformSpec.Name is empty before write")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	target := filepath.Join(dir, ps.Name+".json")
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", target, err)
	}
	if _, err := os.Stat(abs); err == nil && !force {
		return fmt.Errorf("%s already exists; pass --force to overwrite", abs)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", abs, err)
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding platform: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(abs), "platform-*.json.tmp")
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
	fmt.Fprintf(os.Stderr, "platform %q written to %s\n", ps.Name, abs)
	return nil
}

func init() {
	platformGenerateCmd.Flags().StringVar(&platformGenerateName, "name", "",
		"Platform identity (becomes the filename basename and the spec's name field). "+
			"Convention: <HWSKU> for single-variant SONiC, <HWSKU>_<variant> for multi-variant, "+
			"or a descriptive name for non-SONiC platforms. Required.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateHWSKU, "hwsku", "",
		"HWSKU name (e.g. \"Cisco-8101-32x100\"). Required — SONiC platform.json doesn't carry HWSKU.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateDescription, "description", "",
		"Optional description (set on PlatformSpec.Description).")
	platformGenerateCmd.Flags().StringVar(&platformGenerateOutputDir, "output-dir", "",
		"Write to <dir>/<name>.json instead of stdout. Pair with cmd/newt-server's "+
			"--platforms-base value to land in the live global registry.")
	platformGenerateCmd.Flags().StringVar(&platformGenerateDataplane, "dataplane", "",
		"Optional dataplane hint (\"vpp\", \"barefoot\", \"\" for none).")
	platformGenerateCmd.Flags().BoolVar(&platformGenerateForce, "force", false,
		"With --output-dir, overwrite an existing same-named file. Without --force, conflicts refuse.")
	platformGenerateCmd.Flags().StringVar(&platformGeneratePortConfigINI, "port-config-ini", "",
		"Path to a SONiC port_config.ini (the older per-HWSKU convention). When set, used directly. "+
			"When unset, the CLI auto-discovers a sibling <hwsku>/port_config.ini iff platform.json's "+
			"interfaces map is empty.")
	platformCmd.AddCommand(platformGenerateCmd)
}
