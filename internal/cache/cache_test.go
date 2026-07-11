package cache

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestKey_stableAndSensitive(t *testing.T) {
	base := Key([]string{"go", "test"}, "/w", "main")
	if base == "" {
		t.Fatal("Key returned empty")
	}
	if got := Key([]string{"go", "test"}, "/w", "main"); got != base {
		t.Errorf("Key is not stable: %q vs %q", got, base)
	}
	// Each field must change the key.
	if Key([]string{"go", "vet"}, "/w", "main") == base {
		t.Error("Key ignored argv change")
	}
	if Key([]string{"go", "test"}, "/other", "main") == base {
		t.Error("Key ignored cwd change")
	}
	if Key([]string{"go", "test"}, "/w", "dev") == base {
		t.Error("Key ignored branch change")
	}
}

func TestKey_noArgvBoundarySmuggling(t *testing.T) {
	// ["a","b"] must not collide with ["ab"] or ["a\x00b"].
	if Key([]string{"a", "b"}, "/w", "") == Key([]string{"ab"}, "/w", "") {
		t.Error("argv boundary collision: [a b] == [ab]")
	}
}

func TestSaveLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	key := Key([]string{"go", "test"}, "/w", "main")
	in := &Entry{
		Argv:      []string{"go", "test"},
		Cwd:       "/w",
		Branch:    "main",
		Exit:      1,
		Output:    []byte("FAIL\nok\n\x00binary"),
		CreatedAt: 1700000000,
	}
	if err := Save(dir, key, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(dir, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported absent after Save")
	}
	if got.Exit != in.Exit || !bytes.Equal(got.Output, in.Output) || got.Branch != in.Branch || got.CreatedAt != in.CreatedAt {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, in)
	}
}

func TestLoad_absentIsNotAnError(t *testing.T) {
	_, ok, err := Load(t.TempDir(), Key([]string{"nope"}, "/w", ""))
	if err != nil {
		t.Fatalf("Load of absent key errored: %v", err)
	}
	if ok {
		t.Error("Load reported present for a missing key")
	}
}

func TestSave_atomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	key := Key([]string{"x"}, "/w", "")
	if err := Save(dir, key, &Entry{Output: []byte("x")}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Only the final .json should remain; no .tmp leftovers.
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e) == ".tmp" {
			t.Errorf("temp file left behind: %s", e)
		}
	}
}

func TestDir_envOverride(t *testing.T) {
	t.Setenv("RUNDIFF_CACHE_DIR", "/tmp/rundiff-test-xyz")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/rundiff-test-xyz" {
		t.Errorf("Dir = %q, want the override verbatim", got)
	}
}

func TestDir_xdgWhenAbsolute(t *testing.T) {
	t.Setenv("RUNDIFF_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/xdg/cache/rundiff" {
		t.Errorf("Dir = %q, want /xdg/cache/rundiff", got)
	}
}
