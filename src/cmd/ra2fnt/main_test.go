package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"

	"ra2fnt/src/internal/fnt"
	"ra2fnt/src/internal/fontout"
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

func TestRunCreateCnCNetSpriteFont(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	outPath := filepath.Join(root, "font.xnb")
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	if err := runExport([]string{"-in", inPath, "-out", outDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	stderr := captureStderr(t, func() error {
		return runCreate([]string{"-in", outDir, "-out", outPath, "-format", fontout.FormatCnCNetSpriteFont})
	})
	if !strings.Contains(stderr, experimentalCnCNetSpriteFontWarning) {
		t.Fatalf("stderr should contain experimental warning, got:\n%s", stderr)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected created xnb: %v", err)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read created xnb: %v", err)
	}
	if got, want := string(raw[:3]), "XNB"; got != want {
		t.Fatalf("xnb magic mismatch: got=%q want=%q", got, want)
	}
	if got, want := raw[3], byte('w'); got != want {
		t.Fatalf("xnb platform mismatch: got=%q want=%q", got, want)
	}
	if got, want := raw[4], byte(5); got != want {
		t.Fatalf("xnb version mismatch: got=%d want=%d", got, want)
	}
	if got, want := raw[5], byte(0x40); got != want {
		t.Fatalf("xnb flags mismatch: got=%d want=%d", got, want)
	}
	if got, want := int(binary.LittleEndian.Uint32(raw[10:14])), len(decompressLZ4Block(t, raw[14:], int(binary.LittleEndian.Uint32(raw[10:14])))); got != want {
		t.Fatalf("xnb decompressed size mismatch: got=%d want=%d", got, want)
	}
}

func TestRunCreateCnCNetSpriteFontHelpMentionsExperimental(t *testing.T) {
	stderr := captureStderr(t, func() error {
		return runCreate([]string{"-h"})
	})
	if !strings.Contains(stderr, "cncnet-spritefont (experimental)") {
		t.Fatalf("help should mention experimental format, got:\n%s", stderr)
	}
}

func TestColorizeErrorWithoutANSI(t *testing.T) {
	previous := stderrSupportsANSI
	stderrSupportsANSI = func() bool { return false }
	defer func() {
		stderrSupportsANSI = previous
	}()

	if got, want := colorizeError("error:"), "error:"; got != want {
		t.Fatalf("error label mismatch without ANSI: got=%q want=%q", got, want)
	}
}

func TestColorizeErrorWithANSI(t *testing.T) {
	previous := stderrSupportsANSI
	stderrSupportsANSI = func() bool { return true }
	defer func() {
		stderrSupportsANSI = previous
	}()

	if got, want := colorizeError("error:"), "\033[31merror:\033[0m"; got != want {
		t.Fatalf("error label mismatch with ANSI: got=%q want=%q", got, want)
	}
}

func captureStderr(t *testing.T, fn func() error) string {
	t.Helper()

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer readPipe.Close()

	oldStderr := os.Stderr
	os.Stderr = writePipe
	defer func() {
		os.Stderr = oldStderr
	}()

	if err := fn(); err != nil && err != flag.ErrHelp {
		t.Fatalf("captured stderr command failed: %v", err)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}

	output, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(output)
}

func TestRunCreateRejectsUnsupportedFormat(t *testing.T) {
	root := t.TempDir()
	inPath := filepath.Join(root, "in.fnt")
	outDir := filepath.Join(root, "out")
	outPath := filepath.Join(root, "font.bin")
	if err := writeSampleFont(inPath); err != nil {
		t.Fatalf("write sample font: %v", err)
	}

	if err := runExport([]string{"-in", inPath, "-out", outDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	err := runCreate([]string{"-in", outDir, "-out", outPath, "-format", "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported create format") {
		t.Fatalf("expected unsupported format error, got: %v", err)
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

func TestProgressBarFinishIsIdempotent(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer readPipe.Close()

	oldStderr := os.Stderr
	os.Stderr = writePipe
	defer func() {
		os.Stderr = oldStderr
	}()

	bar := newProgressBar("create")
	bar.printed = true
	bar.Finish()
	bar.Finish()

	if err := writePipe.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}

	output, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if got, want := string(output), "\n"; got != want {
		t.Fatalf("unexpected finish output: got=%q want=%q", got, want)
	}
}

func decompressLZ4Block(t *testing.T, src []byte, size int) []byte {
	t.Helper()
	dst := make([]byte, size)
	n, err := lz4.UncompressBlock(src, dst)
	if err != nil {
		t.Fatalf("lz4.UncompressBlock: %v", err)
	}
	if got, want := n, size; got != want {
		t.Fatalf("decompressed size mismatch: got=%d want=%d", got, want)
	}
	return dst
}
