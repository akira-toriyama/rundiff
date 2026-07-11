// Package cache persists one baseline run per key so a later invocation can diff
// against it. It is the on-disk adapter: the key is a hash of what makes two runs
// "the same command" (argv + cwd + git branch), and the entry stores the raw
// output so the diff can re-normalize with the current rules (making a rule
// change take effect on the next run, not silently comparing stale normalized
// text). No diff logic lives here.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Entry is a stored baseline run. Output is the raw combined stdout+stderr;
// encoding/json base64-encodes the []byte on disk.
type Entry struct {
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
	Branch    string   `json:"branch,omitempty"`
	Exit      int      `json:"exit"`
	Output    []byte   `json:"output"`
	CreatedAt int64    `json:"created_at"` // unix seconds, set by the caller (keeps this package clock-free)
}

// Key derives the cache filename stem from what makes two runs comparable. NUL
// separators keep the fields unambiguous (no argv element can smuggle a boundary).
func Key(argv []string, cwd, branch string) string {
	h := sha256.New()
	for _, a := range argv {
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	h.Write([]byte{0, 1})
	h.Write([]byte(cwd))
	h.Write([]byte{0, 2})
	h.Write([]byte(branch))
	return hex.EncodeToString(h.Sum(nil))
}

// Dir resolves rundiff's cache directory without creating it. Resolution order:
// RUNDIFF_CACHE_DIR (explicit override, e.g. for tests) → $XDG_CACHE_HOME (only
// when absolute, per the spec) → ~/.cache, then a /rundiff suffix. os.UserCacheDir
// is deliberately not used: on darwin it returns ~/Library/Caches, breaking the
// XDG contract this family follows.
func Dir() (string, error) {
	if v := os.Getenv("RUNDIFF_CACHE_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, "rundiff"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "rundiff"), nil
}

// Load reads the entry for key from dir. A missing entry is (nil, false, nil):
// absence is not an error — it just means "no baseline yet". A malformed entry is
// also treated as absent (with the error surfaced) so a corrupt cache file never
// wedges a run; the caller re-establishes the baseline.
func Load(dir, key string) (*Entry, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, false, err
	}
	return &e, true, nil
}

// Save writes the entry for key into dir atomically (temp file + rename), so a
// concurrent reader never sees a half-written file. It creates dir as needed.
func Save(dir, key string, e *Entry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, key+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, key+".json"))
}
