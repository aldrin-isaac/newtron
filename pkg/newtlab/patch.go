package newtlab

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/newtron-network/newtron/pkg/spec"
	"golang.org/x/crypto/ssh"
)

//go:embed patches
var patchesFS embed.FS

// BootPatch defines a declarative patch to apply after VM boot.
// Patch descriptors are JSON files under patches/<dataplane>/.
type BootPatch struct {
	Description  string      `json:"description"`
	PreCommands  []string    `json:"pre_commands,omitempty"`
	DisableFiles []string    `json:"disable_files,omitempty"`
	Files        []FilePatch `json:"files,omitempty"`
	Redis        []RedisPatch `json:"redis,omitempty"`
	PostCommands []string    `json:"post_commands,omitempty"`

	// dir is the embedded FS directory containing this patch's templates.
	dir string
}

// FilePatch renders a Go template and writes the result to a path on the VM.
type FilePatch struct {
	Template string `json:"template"`
	Dest     string `json:"dest"`
}

// RedisPatch renders a Go template into redis-cli commands and executes them.
type RedisPatch struct {
	DB       int    `json:"db"`
	Template string `json:"template"`
}

// PatchVars holds the variables available to all boot patch templates.
type PatchVars struct {
	NumPorts   int
	PCIAddrs   []string
	PortStride int // 1 for sequential, 4 for stride-4 (default)
	HWSkuDir   string
	PortSpeed  int
	Platform   string
	Dataplane  string
	Release    string
}

// templateFuncs provides helper functions for boot patch templates.
var templateFuncs = template.FuncMap{
	"mul": func(a, b int) int { return a * b },
	"add": func(a, b int) int { return a + b },
}

// QEMUPCIAddrs returns deterministic PCI addresses for data NICs.
// QEMU assigns PCI slots sequentially on the i440FX bus:
//   slot 0: host bridge, slot 1: ISA/IDE/ACPI, slot 2: VGA
//   slot 3: first -device (our management NIC)
//   slot 4: second -device (first data NIC)
//   slot 5: third -device (second data NIC), etc.
// Data NICs start at slot 4 (slot 3 + 1, skipping mgmt).
func QEMUPCIAddrs(dataNICs int) []string {
	addrs := make([]string, dataNICs)
	for i := 0; i < dataNICs; i++ {
		addrs[i] = fmt.Sprintf("0000:00:%02x.0", 4+i)
	}
	return addrs
}

// ResolveBootPatches returns the ordered list of patches for a platform.
// Resolution order:
//  1. patches/<dataplane>/always/*.json (sorted by filename)
//  2. patches/<dataplane>/<release>/*.json (sorted, if release != "")
//
// Returns nil if no patches exist for the dataplane (no error).
func ResolveBootPatches(dataplane, release string) ([]*BootPatch, error) {
	if dataplane == "" {
		return nil, nil
	}

	var patches []*BootPatch

	// Load "always" patches
	alwaysDir := path.Join("patches", dataplane, "always")
	alwaysPatches, err := loadPatchDir(alwaysDir)
	if err != nil {
		return nil, err
	}
	patches = append(patches, alwaysPatches...)

	// Load release-specific patches
	if release != "" {
		releaseDir := path.Join("patches", dataplane, release)
		releasePatches, err := loadPatchDir(releaseDir)
		if err != nil {
			return nil, err
		}
		patches = append(patches, releasePatches...)
	}

	return patches, nil
}

// loadPatchDir reads all *.json patch descriptors from an embedded FS directory.
// Returns nil (no error) if the directory doesn't exist.
func loadPatchDir(dir string) ([]*BootPatch, error) {
	entries, err := fs.ReadDir(patchesFS, dir)
	if err != nil {
		// Directory doesn't exist — not an error
		return nil, nil
	}

	// Filter and sort JSON files
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	sort.Strings(jsonFiles)

	var patches []*BootPatch
	for _, name := range jsonFiles {
		data, err := patchesFS.ReadFile(path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("newtlab: read patch %s/%s: %w", dir, name, err)
		}
		var p BootPatch
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("newtlab: parse patch %s/%s: %w", dir, name, err)
		}
		p.dir = dir
		patches = append(patches, &p)
	}
	return patches, nil
}

// ApplyBootPatches applies all resolved patches to a VM via SSH.
// For each patch in order:
//  1. Execute pre_commands
//  2. Rename disable_files to .disabled
//  3. Render and upload file templates
//  4. Render and execute redis templates
//  5. Execute post_commands
//
// Returns on first error.
func ApplyBootPatches(host string, port int, user, pass string, patches []*BootPatch, vars *PatchVars) error {
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), config)
	if err != nil {
		return fmt.Errorf("newtlab: boot patches: connect: %w", err)
	}
	defer client.Close()

	run := func(cmd string) (string, error) {
		sess, err := client.NewSession()
		if err != nil {
			return "", err
		}
		defer sess.Close()
		out, err := sess.CombinedOutput(cmd)
		return strings.TrimSpace(string(out)), err
	}

	for _, p := range patches {
		// 1. Pre-commands
		for _, cmd := range p.PreCommands {
			if _, err := run(cmd); err != nil {
				return fmt.Errorf("newtlab: patch %q pre_command %q: %w", p.Description, cmd, err)
			}
		}

		// 2. Disable files
		for _, f := range p.DisableFiles {
			run(fmt.Sprintf("sudo mv %s %s.disabled 2>/dev/null", f, f))
		}

		// 3. File patches
		for _, fp := range p.Files {
			content, err := renderTemplate(fp.Template, p.dir, vars)
			if err != nil {
				return fmt.Errorf("newtlab: patch %q template %s: %w", p.Description, fp.Template, err)
			}

			// Render dest path (may contain template expressions like {{.HWSkuDir}})
			dest, err := renderString(fp.Dest, vars)
			if err != nil {
				return fmt.Errorf("newtlab: patch %q dest %s: %w", p.Description, fp.Dest, err)
			}

			// Write via printf | sudo tee (handles paths needing root)
			escaped := strings.ReplaceAll(content, "'", "'\"'\"'")
			cmd := fmt.Sprintf("printf '%%s' '%s' | sudo tee %s > /dev/null", escaped, dest)
			if _, err := run(cmd); err != nil {
				return fmt.Errorf("newtlab: patch %q write %s: %w", p.Description, dest, err)
			}
		}

		// 4. Redis patches
		for _, rp := range p.Redis {
			content, err := renderTemplate(rp.Template, p.dir, vars)
			if err != nil {
				return fmt.Errorf("newtlab: patch %q redis template %s: %w", p.Description, rp.Template, err)
			}

			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				cmd := fmt.Sprintf("redis-cli -n %d %s", rp.DB, line)
				if _, err := run(cmd); err != nil {
					return fmt.Errorf("newtlab: patch %q redis cmd %q: %w", p.Description, line, err)
				}
			}
		}

		// 5. Post-commands
		for _, cmd := range p.PostCommands {
			if _, err := run(cmd); err != nil {
				return fmt.Errorf("newtlab: patch %q post_command %q: %w", p.Description, cmd, err)
			}
		}
	}

	return nil
}

// buildPatchVars computes template variables from node config and platform spec.
func buildPatchVars(node *NodeConfig, platform *spec.PlatformSpec) *PatchVars {
	// Count data NICs (Index > 0)
	dataNICs := 0
	for _, nic := range node.NICs {
		if nic.Index > 0 {
			dataNICs++
		}
	}

	// Parse default speed (e.g. "25000" → 25000)
	speed, _ := strconv.Atoi(platform.DefaultSpeed)
	if speed == 0 {
		speed = 25000
	}

	portStride := 4 // default: stride-4 (e.g. Ethernet0, Ethernet4, Ethernet8)
	if platform.VMInterfaceMap == "sequential" {
		portStride = 1 // sequential: Ethernet0, Ethernet1, Ethernet2
	}

	return &PatchVars{
		NumPorts:   dataNICs,
		PCIAddrs:   QEMUPCIAddrs(dataNICs),
		PortStride: portStride,
		HWSkuDir:   fmt.Sprintf("/usr/share/sonic/device/x86_64-kvm_x86_64-r0/%s", platform.HWSKU),
		PortSpeed:  speed,
		Platform:   node.Platform,
		Dataplane:  platform.Dataplane,
		Release:    platform.VMImageRelease,
	}
}

// renderTemplate loads a template file from the embedded FS and renders it with vars.
func renderTemplate(name, dir string, vars *PatchVars) (string, error) {
	data, err := patchesFS.ReadFile(path.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", name, err)
	}

	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(string(data))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}

// renderString renders a string as a Go template with vars.
// Used for dest paths that may contain {{.HWSkuDir}} etc.
func renderString(s string, vars *PatchVars) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	tmpl, err := template.New("str").Funcs(templateFuncs).Parse(s)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
