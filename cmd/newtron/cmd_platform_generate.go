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
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

var (
	platformGenerateName        string
	platformGenerateHWSKU       string
	platformGenerateDescription string
	platformGenerateOutput      string
	platformGenerateDataplane   string
	platformGenerateForce       bool
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
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", args[0], err)
		}
		ps, err := spec.FromSONiCPlatformJSON(data, spec.SONiCImportOptions{
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
	platformCmd.AddCommand(platformGenerateCmd)
}
