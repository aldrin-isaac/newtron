package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadPlatformsFromDir reads every <name>.json file under dir and
// returns a map keyed by the platform name. Each file holds a single
// PlatformSpec (no file wrapper), and the file's Name field must equal
// the basename — this invariant ensures the on-disk layout and the
// in-memory identity stay in sync, so accidental renames (git mv,
// editor save-as) surface as load errors rather than silent drift.
//
// Empty dir is not an error — returns an empty map. Missing dir is
// not an error either — operators may run pre-global setups during
// transition. Per-file parse errors and invariant violations DO
// error fail-closed.
func LoadPlatformsFromDir(dir string) (map[string]*PlatformSpec, error) {
	out := make(map[string]*PlatformSpec)
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read platforms dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		basename := strings.TrimSuffix(e.Name(), ".json")
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var p PlatformSpec
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if p.Name == "" {
			return nil, fmt.Errorf("%s: missing name field (must equal basename %q)", path, basename)
		}
		if p.Name != basename {
			return nil, fmt.Errorf("%s: name field %q must equal filename basename %q — rename one to match", path, p.Name, basename)
		}
		if _, dup := out[p.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate platform name %q already loaded (case-sensitive)", path, p.Name)
		}
		out[p.Name] = &p
	}
	return out, nil
}
