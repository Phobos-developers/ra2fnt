package pngset

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ra2fnt/src/internal/fnt"
)

func TestExportWritesRangeDirectoriesAndGlyphWidth(t *testing.T) {
	font := sampleFont()
	dir := t.TempDir()

	if err := Export(font, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	paths, err := collectRelativePNGs(dir)
	if err != nil {
		t.Fatalf("collect png paths: %v", err)
	}

	expectedPaths := []string{
		filepath.Join("0x0020-0x007F (Basic Latin)", "0x0041.png"),
		filepath.Join("0x0020-0x007F (Basic Latin)", "0x0042.png"),
		filepath.Join("0x0020-0x007F (Basic Latin)", "0x0043.png"),
	}
	if !bytes.Equal([]byte(joinNames(paths)), []byte(joinNames(expectedPaths))) {
		t.Fatalf("unexpected exported png paths:\n got: %v\nwant: %v", paths, expectedPaths)
	}

	for _, relativePath := range expectedPaths {
		path := filepath.Join(dir, relativePath)
		img, err := readPNG(path)
		if err != nil {
			t.Fatalf("read exported png %q: %v", path, err)
		}

		codepoint, err := parsePNGBaseName(strings.TrimSuffix(filepath.Base(path), ".png"))
		if err != nil {
			t.Fatalf("parse file name: %v", err)
		}

		symbolIndex := int(font.UnicodeTable[codepoint]) - 1
		if symbolIndex < 0 {
			t.Fatalf("missing symbol mapping for codepoint U+%04X", codepoint)
		}

		if got, want := img.Bounds().Dx(), int(font.Symbols[symbolIndex].Width); got != want {
			t.Fatalf("U+%04X width mismatch: got=%d want=%d", codepoint, got, want)
		}
		if got, want := img.Bounds().Dy(), int(font.SymbolHeight); got != want {
			t.Fatalf("U+%04X height mismatch: got=%d want=%d", codepoint, got, want)
		}
	}

	metadata, err := readMetadata(filepath.Join(dir, metadataFileName))
	if err != nil {
		t.Fatalf("read metadata json: %v", err)
	}
	if metadata.symbolStride == nil || *metadata.symbolStride != font.SymbolStride {
		t.Fatalf("metadata symbol_stride mismatch: got=%v want=%d", metadata.symbolStride, font.SymbolStride)
	}
	if metadata.fontHeight == nil || *metadata.fontHeight != font.FontHeight {
		t.Fatalf("metadata font_height mismatch: got=%v want=%d", metadata.fontHeight, font.FontHeight)
	}
	if metadata.ideographWidth == nil || *metadata.ideographWidth != font.IdeographWidth {
		t.Fatalf("metadata ideograph_width mismatch: got=%v want=%d", metadata.ideographWidth, font.IdeographWidth)
	}
	if got := len(metadata.symbolWidth); got != 0 {
		t.Fatalf("metadata symbol_width should contain only zero-width glyphs, got entries=%d", got)
	}
}

func TestExportWithScaleWritesScaledPNGsAndMetadataScale(t *testing.T) {
	font := sampleFont()
	dir := t.TempDir()

	if err := ExportWithOptions(font, dir, ExportOptions{Scale: 3}); err != nil {
		t.Fatalf("export with scale: %v", err)
	}

	path := filepath.Join(dir, "0x0020-0x007F (Basic Latin)", "0x0042.png")
	img, err := readPNG(path)
	if err != nil {
		t.Fatalf("read scaled png %q: %v", path, err)
	}
	if got, want := img.Bounds().Dx(), 9*3; got != want {
		t.Fatalf("scaled width mismatch: got=%d want=%d", got, want)
	}
	if got, want := img.Bounds().Dy(), int(font.SymbolHeight)*3; got != want {
		t.Fatalf("scaled height mismatch: got=%d want=%d", got, want)
	}

	metadata, err := readMetadata(filepath.Join(dir, metadataFileName))
	if err != nil {
		t.Fatalf("read metadata json: %v", err)
	}
	if got, want := metadata.scale, uint32(3); got != want {
		t.Fatalf("metadata scale mismatch: got=%d want=%d", got, want)
	}
}

func TestExportImportKeepsVisibleGlyphs(t *testing.T) {
	original := sampleFont()
	dir := t.TempDir()

	if err := Export(original, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	imported, err := Import(dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got, want := imported.SymbolsCount, uint32(3); got != want {
		t.Fatalf("symbols count mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.SymbolHeight, original.SymbolHeight; got != want {
		t.Fatalf("symbol height mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.SymbolStride, original.SymbolStride; got != want {
		t.Fatalf("imported stride mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.FontHeight, original.FontHeight; got != want {
		t.Fatalf("font height mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.IdeographWidth, original.IdeographWidth; got != want {
		t.Fatalf("ideograph width mismatch: got=%d want=%d", got, want)
	}

	codepoints := []uint16{'A', 'B', 'C'}
	for i, codepoint := range codepoints {
		mapped := imported.UnicodeTable[codepoint]
		if mapped == 0 {
			t.Fatalf("missing imported mapping for U+%04X", codepoint)
		}

		importedSymbol := imported.Symbols[int(mapped)-1]
		originalSymbol := original.Symbols[i]

		if got, want := importedSymbol.Width, originalSymbol.Width; got != want {
			t.Fatalf("U+%04X width mismatch: got=%d want=%d", codepoint, got, want)
		}

		origPixels := visibleRaster(
			originalSymbol,
			int(original.SymbolStride),
			int(original.SymbolHeight),
			int(originalSymbol.Width),
		)
		importPixels := visibleRaster(
			importedSymbol,
			int(imported.SymbolStride),
			int(imported.SymbolHeight),
			int(importedSymbol.Width),
		)
		if !bytes.Equal(origPixels, importPixels) {
			t.Fatalf("U+%04X visible raster mismatch", codepoint)
		}
	}
}

func TestCreateFromScaledPNGsRestoresOriginalLogicalSize(t *testing.T) {
	original := sampleFont()
	dir := t.TempDir()

	if err := ExportWithOptions(original, dir, ExportOptions{Scale: 4}); err != nil {
		t.Fatalf("export with scale: %v", err)
	}

	imported, err := Import(dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got, want := imported.SymbolHeight, original.SymbolHeight; got != want {
		t.Fatalf("symbol height mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.SymbolStride, original.SymbolStride; got != want {
		t.Fatalf("symbol stride mismatch: got=%d want=%d", got, want)
	}

	codepoints := []uint16{'A', 'B', 'C'}
	for i, codepoint := range codepoints {
		mapped := imported.UnicodeTable[codepoint]
		if mapped == 0 {
			t.Fatalf("missing imported mapping for U+%04X", codepoint)
		}

		importedSymbol := imported.Symbols[int(mapped)-1]
		originalSymbol := original.Symbols[i]

		if got, want := importedSymbol.Width, originalSymbol.Width; got != want {
			t.Fatalf("U+%04X width mismatch: got=%d want=%d", codepoint, got, want)
		}

		origPixels := visibleRaster(
			originalSymbol,
			int(original.SymbolStride),
			int(original.SymbolHeight),
			int(originalSymbol.Width),
		)
		importPixels := visibleRaster(
			importedSymbol,
			int(imported.SymbolStride),
			int(imported.SymbolHeight),
			int(importedSymbol.Width),
		)
		if !bytes.Equal(origPixels, importPixels) {
			t.Fatalf("U+%04X visible raster mismatch after scaled roundtrip", codepoint)
		}
	}
}

func TestExportImportSupportsZeroWidthGlyph(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   2,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
			{Width: 0, Data: []byte{0b0000_0000, 0b0000_0000}},
		},
	}
	font.UnicodeTable['A'] = 1
	font.UnicodeTable[0x0401] = 2

	dir := t.TempDir()
	if err := Export(font, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	zeroPath := filepath.Join(dir, "0x0400-0x04FF (Cyrillic)", "0x0401.png")
	if _, err := os.Stat(zeroPath); !os.IsNotExist(err) {
		t.Fatalf("zero-width glyph png should not exist, stat err=%v", err)
	}

	metadata, err := readMetadata(filepath.Join(dir, metadataFileName))
	if err != nil {
		t.Fatalf("read metadata json: %v", err)
	}
	if got, ok := metadata.symbolWidth[0x0401]; !ok || got != 0 {
		t.Fatalf("U+0401 should have symbol_width=0 in metadata, got ok=%v value=%d", ok, got)
	}
	if got := len(metadata.symbolWidth); got != 1 {
		t.Fatalf("metadata symbol_width should contain only one zero-width entry, got=%d", got)
	}

	imported, err := Import(dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	mapped := imported.UnicodeTable[0x0401]
	if mapped == 0 {
		t.Fatalf("missing imported mapping for U+0401")
	}
	if got := imported.Symbols[int(mapped)-1].Width; got != 0 {
		t.Fatalf("zero-width glyph restored incorrectly: got=%d want=0", got)
	}
}

func TestImportWithOptionsOverridesHeaderFields(t *testing.T) {
	original := sampleFont()
	dir := t.TempDir()

	if err := Export(original, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	imported, err := ImportWithOptions(dir, ImportOptions{
		OverrideFontHeight:     true,
		FontHeight:             33,
		OverrideIdeographWidth: true,
		IdeographWidth:         44,
	})
	if err != nil {
		t.Fatalf("import with options: %v", err)
	}

	if got, want := imported.FontHeight, uint32(33); got != want {
		t.Fatalf("font height override mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.IdeographWidth, uint32(44); got != want {
		t.Fatalf("ideograph width override mismatch: got=%d want=%d", got, want)
	}
	if got, want := imported.SymbolHeight, original.SymbolHeight; got != want {
		t.Fatalf("symbol height should stay unchanged: got=%d want=%d", got, want)
	}
}

func TestCreateDeduplicatesIdenticalGlyphs(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   2,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
		},
	}
	font.UnicodeTable['A'] = 1
	font.UnicodeTable['B'] = 2

	dir := t.TempDir()
	if err := Export(font, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	created, err := Import(dir)
	if err != nil {
		t.Fatalf("create/import: %v", err)
	}

	if got, want := created.SymbolsCount, uint32(1); got != want {
		t.Fatalf("dedup symbols count mismatch: got=%d want=%d", got, want)
	}

	mapA := created.UnicodeTable['A']
	mapB := created.UnicodeTable['B']
	if mapA == 0 || mapB == 0 {
		t.Fatalf("missing unicode table mapping after dedup: A=%d B=%d", mapA, mapB)
	}
	if mapA != mapB {
		t.Fatalf("expected A and B to share symbol index after dedup: A=%d B=%d", mapA, mapB)
	}
}

func TestCreateWithNoDedupKeepsIdenticalGlyphsSeparate(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   2,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
		},
	}
	font.UnicodeTable['A'] = 1
	font.UnicodeTable['B'] = 2

	dir := t.TempDir()
	if err := Export(font, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	created, err := ImportWithOptions(dir, ImportOptions{
		DisableDedup: true,
	})
	if err != nil {
		t.Fatalf("create/import with no dedup: %v", err)
	}

	if got, want := created.SymbolsCount, uint32(2); got != want {
		t.Fatalf("symbols count mismatch with no dedup: got=%d want=%d", got, want)
	}
	if created.UnicodeTable['A'] == created.UnicodeTable['B'] {
		t.Fatalf("A and B should have different symbol indexes when no dedup is enabled")
	}
}

func TestValidateReportsCounts(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   3,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
			{Width: 2, Data: []byte{0b1100_0000, 0b0000_0000}},
			{Width: 0, Data: []byte{0b0000_0000, 0b0000_0000}},
		},
	}
	font.UnicodeTable['A'] = 1
	font.UnicodeTable['B'] = 2
	font.UnicodeTable['C'] = 3

	dir := t.TempDir()
	if err := Export(font, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	report, err := Validate(dir)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if got, want := report.Codepoints, 3; got != want {
		t.Fatalf("codepoints mismatch: got=%d want=%d", got, want)
	}
	if got, want := report.PNGFiles, 2; got != want {
		t.Fatalf("png files mismatch: got=%d want=%d", got, want)
	}
	if got, want := report.ZeroWidthCodepoints, 1; got != want {
		t.Fatalf("zero-width mismatch: got=%d want=%d", got, want)
	}
	if got, want := report.UniqueSymbols, 2; got != want {
		t.Fatalf("unique symbols mismatch: got=%d want=%d", got, want)
	}
	if got, want := report.DeduplicatedSymbols, 1; got != want {
		t.Fatalf("deduplicated symbols mismatch: got=%d want=%d", got, want)
	}
}

func TestExportWithOptionsReportsProgress(t *testing.T) {
	font := sampleFont()
	dir := t.TempDir()

	updates := make([]string, 0)
	err := ExportWithOptions(font, dir, ExportOptions{
		Progress: func(stage string, done, total int) {
			updates = append(updates, fmt.Sprintf("%s:%d/%d", stage, done, total))
		},
	})
	if err != nil {
		t.Fatalf("export with options: %v", err)
	}

	if got, want := len(updates), 3; got != want {
		t.Fatalf("unexpected progress updates count: got=%d want=%d", got, want)
	}

	expectedLast := fmt.Sprintf("%s:%d/%d", ProgressStageExportGlyphs, 3, 3)
	if got := updates[len(updates)-1]; got != expectedLast {
		t.Fatalf("unexpected last export progress update: got=%q want=%q", got, expectedLast)
	}
}

func TestImportWithOptionsReportsProgress(t *testing.T) {
	original := sampleFont()
	dir := t.TempDir()
	if err := Export(original, dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	type stageState struct {
		lastDone int
		total    int
		calls    int
	}
	stages := make(map[string]stageState)

	_, err := ImportWithOptions(dir, ImportOptions{
		Progress: func(stage string, done, total int) {
			state := stages[stage]
			if state.calls > 0 {
				if total != state.total {
					t.Fatalf("inconsistent total for stage %q: got=%d want=%d", stage, total, state.total)
				}
				if done < state.lastDone {
					t.Fatalf("progress is not monotonic for stage %q: got=%d last=%d", stage, done, state.lastDone)
				}
			} else {
				state.total = total
			}

			state.lastDone = done
			state.calls++
			stages[stage] = state
		},
	})
	if err != nil {
		t.Fatalf("import with options: %v", err)
	}

	readState, ok := stages[ProgressStageImportReadPNGs]
	if !ok {
		t.Fatalf("missing progress stage %q", ProgressStageImportReadPNGs)
	}
	if readState.lastDone != 3 || readState.total != 3 {
		t.Fatalf("unexpected read stage progress: done=%d total=%d", readState.lastDone, readState.total)
	}

	encodeState, ok := stages[ProgressStageImportEncodeSymbols]
	if !ok {
		t.Fatalf("missing progress stage %q", ProgressStageImportEncodeSymbols)
	}
	if encodeState.lastDone != 3 || encodeState.total != 3 {
		t.Fatalf("unexpected encode stage progress: done=%d total=%d", encodeState.lastDone, encodeState.total)
	}
}

func sampleFont() *fnt.Font {
	font := &fnt.Font{
		IdeographWidth: 16,
		SymbolStride:   3,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   3,
		SymbolDataSize: 7, // 1 byte width + stride*height (3*2)
		Symbols: []fnt.Symbol{
			{Width: 1, Data: []byte{
				0b1000_0000, 0b0000_0000, 0b1111_1111,
				0b0000_0000, 0b0000_0000, 0b1010_1010,
			}},
			{Width: 9, Data: []byte{
				0b1111_1111, 0b1000_0000, 0b0101_0101,
				0b1000_0000, 0b0000_0000, 0b1111_0000,
			}},
			{Width: 4, Data: []byte{
				0b1111_0000, 0b0000_0000, 0b0000_1111,
				0b0011_0000, 0b0000_0000, 0b1111_0000,
			}},
		},
	}

	font.UnicodeTable['A'] = 1
	font.UnicodeTable['B'] = 2
	font.UnicodeTable['C'] = 3
	return font
}

func visibleRaster(symbol fnt.Symbol, stride, height, width int) []byte {
	out := make([]byte, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if symbol.Data[y*stride+x/8]&(1<<(7-uint(x%8))) != 0 {
				out[y*width+x] = 1
			}
		}
	}
	return out
}

func collectRelativePNGs(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".png") {
			relativePath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, relativePath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, name := range names[1:] {
		out += "|" + name
	}
	return out
}
