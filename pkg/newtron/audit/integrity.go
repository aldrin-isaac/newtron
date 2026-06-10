package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// canonicalEventBytes serializes an Event for hashing. The ID field
// is zeroed in the copy because it IS the hash; including itself in
// its own hash would be self-referential. Everything else — including
// PrevHash — is included so a verifier can reproduce the hash from
// stored fields alone.
//
// json.Marshal is deterministic enough for L6 because Go's encoder
// emits struct fields in declaration order and map keys in sorted
// order. Two Events with identical field values produce byte-for-byte
// identical canonical JSON.
func canonicalEventBytes(e *Event) ([]byte, error) {
	clone := *e
	clone.ID = ""
	return json.Marshal(&clone)
}

// computeEventHash returns the hex-encoded SHA256 hash linking an
// event to the chain head. The hash covers (prev_hash || canonical
// JSON of the event), so any modification to an entry's content
// — including its prev_hash — breaks the link, and any modification
// to a previous entry breaks every subsequent link.
func computeEventHash(prevHash string, eventBytes []byte) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(eventBytes)
	return hex.EncodeToString(h.Sum(nil))
}

// readChainHead returns the ID field of the last well-formed entry
// in path. Used by NewFileLoggerWithIntegrity at startup to recover
// the chain head across server restarts so a restart doesn't reset
// the chain visually (the file remains internally verifiable end to
// end after multiple server lifecycles).
//
// Missing file → empty head (the chain starts fresh). Malformed
// entries are skipped but the last well-formed entry wins as the
// head; that's the same forgiving behavior as Query's scanner.
func readChainHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	var lastID string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		lastID = e.ID
	}
	return lastID, scanner.Err()
}

// VerifyResult is the outcome of Verify. When BrokenAt is 0 the log
// verifies clean; the chain head is captured in Head for an
// operator that wants to mirror it elsewhere. When BrokenAt is
// non-zero it carries the 1-indexed line number of the first entry
// whose link doesn't reproduce, plus a Reason describing whether the
// PrevHash didn't match the running head or the ID didn't reproduce
// from the entry's content.
type VerifyResult struct {
	BrokenAt int
	Reason   string
	Head     string // chain head ID after the last verified entry
	Entries  int    // count of entries scanned
}

// Verify walks a JSON-lines audit log and confirms the hash chain
// holds end to end (auth-design.md L6). It is the operator's
// post-hoc tamper-evidence check; run periodically (cron, daily) or
// after a suspected intrusion.
//
// Two failure shapes:
//   - PrevHash mismatch — an entry was inserted or its predecessor
//     was removed/reordered.
//   - ID hash mismatch — an entry's content was modified in place.
//
// On any mismatch, Verify returns immediately with the first broken
// position. Operators inspect the surrounding entries to learn what
// changed.
//
// Empty logs verify clean (Entries=0, BrokenAt=0). Logs that mix
// integrity entries (with non-empty ID) and pre-integrity entries
// (with empty ID) verify only the integrity entries — empty IDs
// short-circuit the chain expectation for that line.
func Verify(path string) (VerifyResult, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return VerifyResult{}, nil
		}
		return VerifyResult{}, err
	}
	defer f.Close()

	var prevHash string
	var result VerifyResult
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		result.Entries = line
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return VerifyResult{}, fmt.Errorf("line %d: malformed JSON: %w", line, err)
		}
		// Pre-L6 entries have empty ID — skip chain verification for
		// them so a mixed log (operator upgraded mid-stream) still
		// parses without spurious failures.
		if e.ID == "" {
			continue
		}
		if e.PrevHash != prevHash {
			result.BrokenAt = line
			result.Reason = fmt.Sprintf("prev_hash mismatch (got %q, expected %q)", e.PrevHash, prevHash)
			return result, nil
		}
		content, err := canonicalEventBytes(&e)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("line %d: canonical JSON: %w", line, err)
		}
		want := computeEventHash(e.PrevHash, content)
		if e.ID != want {
			result.BrokenAt = line
			result.Reason = "id hash mismatch (entry content modified)"
			return result, nil
		}
		prevHash = e.ID
	}
	if err := scanner.Err(); err != nil {
		return VerifyResult{}, err
	}
	result.Head = prevHash
	return result, nil
}
