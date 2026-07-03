package spec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ArchiveDirName is the reserved subdirectory of a networks-base tree that holds
// soft-deleted networks. It is NOT a network: auto-discovery skips it and
// network creation rejects it as an id (see IsReservedNetworkName), so a real
// network can never collide with the archive store. Kept as one named constant
// so the writer (ArchiveNetwork), the discovery scan, and the create-time
// validator all agree — §13 (Same Concept = Same Name).
const ArchiveDirName = "archives"

// ErrNotFound is returned by ArchiveNetwork when the network's spec directory
// does not exist. ErrArchiveExists is returned when the destination archive path
// is already taken (a same-second, same-id re-archive — effectively impossible
// once the id is unregistered, but surfaced rather than silently overwriting a
// prior archive).
var (
	ErrNotFound      = errors.New("network spec directory not found")
	ErrArchiveExists = errors.New("archive destination already exists")
)

// IsReservedNetworkName reports whether name is a reserved directory under a
// networks-base tree that must never be treated as a network. Today that is only
// the archive store. The create path rejects it and the discovery scan skips it,
// so the reservation is enforced symmetrically (§15: the writer rejects what the
// loader would skip).
func IsReservedNetworkName(name string) bool {
	return name == ArchiveDirName
}

// ArchiveNetwork soft-deletes the network with id `id` under networksBase by
// MOVING its spec directory to <networksBase>/archives/<id>-<timestamp>/, the
// reverse of CreateEmpty's scaffold (§15). It does not delete anything: the
// directory and all its contents (specs, secrets.json, audit/) travel intact to
// the archive, so the delete is undoable — manually, by moving the archived
// directory back. Nothing in newtron lists or reads the archive; it exists only
// for out-of-band recovery.
//
// timestamp is supplied by the caller (a UTC stamp like "20060102T150405Z") so
// this stays clock-free and testable. Returns the absolute-ish archive path so
// the caller can report where the network went.
//
// The move is os.Rename — atomic within one filesystem, which the archive store
// always is (a subdirectory of the same networks-base tree). Fails closed:
// ErrNotFound if the source is absent, ErrArchiveExists if the destination is
// taken — never a partial or overwriting move.
func ArchiveNetwork(networksBase, id, timestamp string) (string, error) {
	if networksBase == "" {
		return "", fmt.Errorf("networks-base is required")
	}
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if timestamp == "" {
		return "", fmt.Errorf("timestamp is required")
	}
	src := filepath.Join(networksBase, id)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotFound, src)
	}

	archiveRoot := filepath.Join(networksBase, ArchiveDirName)
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		return "", fmt.Errorf("create archive root %s: %w", archiveRoot, err)
	}
	dst := filepath.Join(archiveRoot, id+"-"+timestamp)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("%w: %s", ErrArchiveExists, dst)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("archive %s → %s: %w", src, dst, err)
	}
	return dst, nil
}
