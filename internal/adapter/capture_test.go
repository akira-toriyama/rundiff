package adapter

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loadCapture reads one real captured transcript (testdata/captures/<tool>/
// <scenario>.out + .exit). Captures are raw bytes from actual tool runs — see
// each tool dir's VERSIONS for provenance ("transcribed" = hand-written from
// the documented format because the tool was absent on the capture machine).
func loadCapture(t testing.TB, tool, scenario string) Run {
	t.Helper()
	dir := filepath.Join("testdata", "captures", tool)
	out, err := os.ReadFile(filepath.Join(dir, scenario+".out"))
	if err != nil {
		t.Fatalf("capture %s/%s: %v", tool, scenario, err)
	}
	exitRaw, err := os.ReadFile(filepath.Join(dir, scenario+".exit"))
	if err != nil {
		t.Fatalf("capture %s/%s exit: %v", tool, scenario, err)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(string(exitRaw)))
	if err != nil {
		t.Fatalf("capture %s/%s exit parse: %v", tool, scenario, err)
	}
	return Run{Output: out, Exit: exit}
}

// captureScenarios lists every committed capture as (tool, scenario) pairs for
// the cross-tool invariant sweeps.
func captureScenarios(t testing.TB) [][2]string {
	t.Helper()
	var out [][2]string
	tools, err := os.ReadDir(filepath.Join("testdata", "captures"))
	if err != nil {
		t.Fatalf("captures dir: %v", err)
	}
	for _, tool := range tools {
		if !tool.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join("testdata", "captures", tool.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if name, ok := strings.CutSuffix(f.Name(), ".out"); ok {
				out = append(out, [2]string{tool.Name(), name})
			}
		}
	}
	return out
}
