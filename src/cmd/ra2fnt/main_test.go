package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ra2fnt/src/internal/fnt"
)

func TestEnsureExportOutDirNotExists(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")

	ok, err := ensureExportOutDir(outDir, strings.NewReader("\n"), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("ensureExportOutDir: %v", err)
	}
	if !ok {
		t.Fatalf("expected proceed=true when directory does not exist")
	}
}

func TestEnsureExportOutDirDecline(t *testing.T) {
	outDir := t.TempDir()

	ok, err := ensureExportOutDir(outDir, strings.NewReader("n\n"), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("ensureExportOutDir: %v", err)
	}
	if ok {
		t.Fatalf("expected proceed=false when user declines")
	}

	if _, err := os.Stat(outDir); err != nil {
		t.Fatalf("expected directory to remain after decline: %v", err)
	}
}

func TestEnsureExportOutDirAccept(t *testing.T) {
	outDir := t.TempDir()
	filePath := filepath.Join(outDir, "marker.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}

	ok, err := ensureExportOutDir(outDir, strings.NewReader("yes\n"), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("ensureExportOutDir: %v", err)
	}
	if !ok {
		t.Fatalf("expected proceed=true when user accepts")
	}

	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be removed, stat err=%v", err)
	}
}

func TestEnsureExportOutDirForce(t *testing.T) {
	outDir := t.TempDir()
	filePath := filepath.Join(outDir, "marker.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}

	ok, err := ensureExportOutDir(outDir, strings.NewReader(""), &bytes.Buffer{}, true)
	if err != nil {
		t.Fatalf("ensureExportOutDir force: %v", err)
	}
	if !ok {
		t.Fatalf("expected proceed=true when force is enabled")
	}

	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be removed in force mode, stat err=%v", err)
	}
}

func TestRunExportCancelledByPrompt(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out dir: %v", err)
	}
	markerPath := filepath.Join(outDir, "marker.txt")
	if err := os.WriteFile(markerPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	stdinFile := filepath.Join(root, "stdin.txt")
	if err := os.WriteFile(stdinFile, []byte("n\n"), 0o644); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}
	input, err := os.Open(stdinFile)
	if err != nil {
		t.Fatalf("open stdin file: %v", err)
	}
	defer input.Close()

	oldStdin := os.Stdin
	os.Stdin = input
	defer func() {
		os.Stdin = oldStdin
	}()

	if err := runExport([]string{"-in", inPath, "-out", outDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected existing directory to remain untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata should not be written when export is cancelled, err=%v", err)
	}
}

func TestRunExportForceSkipsPromptAndOverwritesOutDir(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out dir: %v", err)
	}
	markerPath := filepath.Join(outDir, "marker.txt")
	if err := os.WriteFile(markerPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	if err := runExport([]string{"-in", inPath, "-out", outDir, "-force"}); err != nil {
		t.Fatalf("runExport force: %v", err)
	}

	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("old marker should be removed by force export, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "metadata.json")); err != nil {
		t.Fatalf("expected metadata written after force export: %v", err)
	}
}

func TestRunExportScaleWritesScaledPNGAndMetadata(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	if err := runExport([]string{"-in", inPath, "-out", outDir, "-scale", "3"}); err != nil {
		t.Fatalf("runExport with scale: %v", err)
	}

	pngPath := filepath.Join(outDir, "0x0020-0x007F (Basic Latin)", "0x0041.png")
	file, err := os.Open(pngPath)
	if err != nil {
		t.Fatalf("open exported png: %v", err)
	}
	defer file.Close()

	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode exported png: %v", err)
	}
	if got, want := img.Bounds().Dx(), 2*3; got != want {
		t.Fatalf("scaled width mismatch: got=%d want=%d", got, want)
	}
	if got, want := img.Bounds().Dy(), 2*3; got != want {
		t.Fatalf("scaled height mismatch: got=%d want=%d", got, want)
	}

	metadataRaw, err := os.ReadFile(filepath.Join(outDir, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.Contains(string(metadataRaw), "\"scale\": 3") {
		t.Fatalf("metadata should contain scale=3, got:\n%s", string(metadataRaw))
	}
}

func TestRunExportRejectsInvalidScale(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	err := runExport([]string{"-in", inPath, "-out", outDir, "-scale", "0"})
	if err == nil || !strings.Contains(err.Error(), "scale") {
		t.Fatalf("expected scale validation error, got: %v", err)
	}
}

func TestRunValidateAcceptsNoDedupFlag(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	if err := runExport([]string{"-in", inPath, "-out", outDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	if err := runValidate([]string{"-in", outDir, "-no-dedup"}); err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func writeSampleFont(path string) error {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   1,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
		},
	}
	font.UnicodeTable['A'] = 1
	return fnt.WriteFile(path, font)
}

func TestVersionString(t *testing.T) {
	previous := version
	version = "1.2.3"
	defer func() {
		version = previous
	}()

	if got, want := versionString(), "ra2fnt version 1.2.3"; got != want {
		t.Fatalf("version string mismatch: got=%q want=%q", got, want)
	}
}
