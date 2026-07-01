package audit

import "path/filepath"

// AuditDirName is the subdirectory, inside a network's own folder, that
// holds its audit log. Kept as a named constant so the writer, the read
// path, the spec-watcher's ignore rule, and .gitignore all agree on the
// one location.
const AuditDirName = "audit"

// LogFileName is the audit log's filename within AuditDirName.
const LogFileName = "audit.log"

// Path returns the audit-log path for a network whose spec directory is
// specDir: <specDir>/audit/audit.log. The audit log is a durable,
// per-network record and lives in the network's own folder alongside its
// specs — one owner of "where does this network's audit live," called by
// both the writer (the api Server's per-network logger) and every reader
// (the HTTP handlers, `bin/newtron audit verify`). newFileLogger creates
// the audit/ directory on first write.
func Path(specDir string) string {
	return filepath.Join(specDir, AuditDirName, LogFileName)
}
