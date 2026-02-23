package pngset

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"ra2fnt/src/internal/fnt"
)

const (
	hexIndexDigits      = 4
	pngChannelThreshold = 0x8000

	pngForegroundRGBA = uint32(0x000000FF)
	pngBackgroundRGBA = uint32(0xFFFFFFFF)

	metadataFileName = "metadata.json"

	ProgressStageExportGlyphs        = "export_glyphs"
	ProgressStageImportReadPNGs      = "import_read_pngs"
	ProgressStageImportEncodeSymbols = "import_encode_symbols"
)

var (
	pngForegroundColor = color.NRGBA{
		R: uint8((pngForegroundRGBA >> 24) & 0xFF),
		G: uint8((pngForegroundRGBA >> 16) & 0xFF),
		B: uint8((pngForegroundRGBA >> 8) & 0xFF),
		A: uint8(pngForegroundRGBA & 0xFF),
	}
	pngBackgroundColor = color.NRGBA{
		R: uint8((pngBackgroundRGBA >> 24) & 0xFF),
		G: uint8((pngBackgroundRGBA >> 16) & 0xFF),
		B: uint8((pngBackgroundRGBA >> 8) & 0xFF),
		A: uint8(pngBackgroundRGBA & 0xFF),
	}
)

//go:embed unicode_ranges.json
var unicodeRangesJSON []byte

var bmpRanges = func() []unicodeRange {
	var parsed unicodeRangesFile
	if err := json.Unmarshal(unicodeRangesJSON, &parsed); err != nil {
		panic(fmt.Sprintf("parse unicode_ranges.json: %v", err))
	}
	if len(parsed.Ranges) == 0 {
		panic("unicode_ranges.json has no ranges")
	}

	for i, unicodeRange := range parsed.Ranges {
		if unicodeRange.Start > unicodeRange.End {
			panic(fmt.Sprintf("unicode range #%d has start > end", i))
		}
		if strings.TrimSpace(unicodeRange.Label) == "" {
			panic(fmt.Sprintf("unicode range #%d has empty label", i))
		}
	}

	sort.Slice(parsed.Ranges, func(i, j int) bool {
		return parsed.Ranges[i].Start < parsed.Ranges[j].Start
	})

	return parsed.Ranges
}()

type unicodeRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
	Label string `json:"label"`
}

type unicodeRangesFile struct {
	Ranges []unicodeRange `json:"ranges"`
}

type codepointPNG struct {
	codepoint uint16
	path      string
}

type metadataFile struct {
	SymbolStride   *uint32          `json:"symbol_stride,omitempty"`
	FontHeight     *uint32          `json:"font_height,omitempty"`
	IdeographWidth *uint32          `json:"ideograph_width,omitempty"`
	Scale          *uint32          `json:"scale,omitempty"`
	SymbolWidth    map[string]uint8 `json:"symbol_width,omitempty"`
}

type parsedMetadata struct {
	symbolWidth    map[uint16]uint8
	symbolStride   *uint32
	fontHeight     *uint32
	ideographWidth *uint32
	scale          uint32
}

type glyphImage struct {
	width int
	img   image.Image
}

type pngDecodeTask struct {
	glyphIndex int
	codepoint  uint16
	path       string
}

type decodedPNG struct {
	glyphIndex int
	codepoint  uint16
	path       string
	width      int
	height     int
	img        image.Image
}

type ProgressFunc func(stage string, done, total int)

type ExportOptions struct {
	Progress ProgressFunc
	Scale    int
}

type ImportOptions struct {
	OverrideFontHeight     bool
	FontHeight             uint32
	OverrideIdeographWidth bool
	IdeographWidth         uint32
	DisableDedup           bool
	Progress               ProgressFunc
}

type CreateReport struct {
	Codepoints          int
	UniqueSymbols       int
	DeduplicatedSymbols int
	PNGFiles            int
	ZeroWidthCodepoints int
}

func Export(font *fnt.Font, outDir string) error {
	return ExportWithOptions(font, outDir, ExportOptions{})
}

func ExportWithOptions(font *fnt.Font, outDir string, options ExportOptions) error {
	if outDir == "" {
		return fmt.Errorf("output directory is empty")
	}
	if font == nil {
		return fmt.Errorf("font is nil")
	}
	if _, err := font.MarshalBinary(); err != nil {
		return fmt.Errorf("invalid font data: %w", err)
	}
	if len(font.Symbols) > 65535 {
		return fmt.Errorf("too many symbols: %d (max supported: 65535)", len(font.Symbols))
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", outDir, err)
	}

	scale := options.Scale
	if scale == 0 {
		scale = 1
	}
	if scale < 1 {
		return fmt.Errorf("scale must be >= 1")
	}

	fullWidth := int(font.SymbolStride) * 8
	createdDirs := make(map[string]struct{})
	symbolWidthByCodepoint := make(map[string]uint8)
	mappedCodepoints := 0
	for _, mappedSymbol := range font.UnicodeTable {
		if mappedSymbol != 0 {
			mappedCodepoints++
		}
	}
	processedCodepoints := 0

	for codepoint, mappedSymbol := range font.UnicodeTable {
		if mappedSymbol == 0 {
			continue
		}

		symbolIndex := int(mappedSymbol) - 1
		if symbolIndex < 0 || symbolIndex >= len(font.Symbols) {
			return fmt.Errorf(
				"unicode table maps U+%04X to invalid symbol index %d (symbols=%d)",
				codepoint,
				symbolIndex,
				len(font.Symbols),
			)
		}

		symbol := font.Symbols[symbolIndex]
		width := int(symbol.Width)
		if width > fullWidth {
			return fmt.Errorf("symbol %d width=%d exceeds stride width=%d", symbolIndex, width, fullWidth)
		}

		renderedWidth := width
		if renderedWidth == 0 {
			// Width=0 glyphs are represented only in metadata and are not exported to PNG.
			symbolWidthByCodepoint[fmt.Sprintf("0x%0*X", hexIndexDigits, uint16(codepoint))] = 0
			processedCodepoints++
			reportProgress(options.Progress, ProgressStageExportGlyphs, processedCodepoints, mappedCodepoints)
			continue
		}

		img, err := symbolDataToImage(symbol.Data, int(font.SymbolStride), int(font.SymbolHeight), renderedWidth)
		if err != nil {
			return fmt.Errorf("build image for U+%04X (symbol %d): %w", codepoint, symbolIndex, err)
		}
		if scale > 1 {
			img = scaleImageInteger(img, scale)
		}

		subDir := directoryForCodepoint(uint16(codepoint))
		subDirPath := filepath.Join(outDir, subDir)
		if _, exists := createdDirs[subDirPath]; !exists {
			if err := os.MkdirAll(subDirPath, 0o755); err != nil {
				return fmt.Errorf("create unicode range directory %q: %w", subDirPath, err)
			}
			createdDirs[subDirPath] = struct{}{}
		}

		fileName := formatCodepointFileName(uint16(codepoint))
		if err := writePNG(filepath.Join(subDirPath, fileName), img); err != nil {
			return fmt.Errorf("write U+%04X: %w", codepoint, err)
		}

		processedCodepoints++
		reportProgress(options.Progress, ProgressStageExportGlyphs, processedCodepoints, mappedCodepoints)
	}

	metadataPath := filepath.Join(outDir, metadataFileName)
	if err := writeMetadata(
		metadataPath,
		metadataFile{
			SymbolWidth:    symbolWidthByCodepoint,
			SymbolStride:   ptrUint32(font.SymbolStride),
			FontHeight:     ptrUint32(font.FontHeight),
			IdeographWidth: ptrUint32(font.IdeographWidth),
			Scale:          ptrUint32(uint32(scale)),
		},
	); err != nil {
		return err
	}

	return nil
}

func Import(inDir string) (*fnt.Font, error) {
	return ImportWithOptions(inDir, ImportOptions{})
}

func ImportWithOptions(inDir string, options ImportOptions) (*fnt.Font, error) {
	font, _, err := ImportWithReport(inDir, options)
	if err != nil {
		return nil, err
	}
	return font, nil
}

func ImportWithReport(inDir string, options ImportOptions) (*fnt.Font, CreateReport, error) {
	if inDir == "" {
		return nil, CreateReport{}, fmt.Errorf("input directory is empty")
	}

	pngFiles, err := collectCodepointPNGs(inDir)
	if err != nil {
		return nil, CreateReport{}, err
	}
	metadataPath := filepath.Join(inDir, metadataFileName)
	metadata, err := readMetadata(metadataPath)
	if err != nil {
		return nil, CreateReport{}, err
	}

	pngByCodepoint := make(map[uint16]codepointPNG, len(pngFiles))
	for _, file := range pngFiles {
		pngByCodepoint[file.codepoint] = file
	}

	allCodepointsMap := make(map[uint16]struct{}, len(pngFiles)+len(metadata.symbolWidth))
	for _, file := range pngFiles {
		allCodepointsMap[file.codepoint] = struct{}{}
	}
	for codepoint := range metadata.symbolWidth {
		allCodepointsMap[codepoint] = struct{}{}
	}

	allCodepoints := make([]uint16, 0, len(allCodepointsMap))
	for codepoint := range allCodepointsMap {
		allCodepoints = append(allCodepoints, codepoint)
	}
	sort.Slice(allCodepoints, func(i, j int) bool {
		return allCodepoints[i] < allCodepoints[j]
	})

	if len(allCodepoints) == 0 {
		return nil, CreateReport{}, fmt.Errorf("no PNG files or symbol_width metadata found in %q", inDir)
	}
	if len(allCodepoints) > 65535 {
		return nil, CreateReport{}, fmt.Errorf("too many symbols: %d (max supported: 65535)", len(allCodepoints))
	}

	symbolHeight := -1
	maxWidth := 0
	glyphs := make([]glyphImage, len(allCodepoints))
	pngTasks := make([]pngDecodeTask, 0, len(pngFiles))
	zeroWidthCodepoints := 0
	for i, codepoint := range allCodepoints {
		file, hasPNG := pngByCodepoint[codepoint]
		if !hasPNG {
			width, hasMetadataWidth := metadata.symbolWidth[codepoint]
			if !hasMetadataWidth || width != 0 {
				return nil, CreateReport{}, fmt.Errorf(
					"missing PNG for U+%04X (only width=0 symbols may be omitted and must be present in %q)",
					codepoint,
					metadataPath,
				)
			}
			zeroWidthCodepoints++
			glyphs[i] = glyphImage{
				width: 0,
				img:   nil,
			}
			continue
		}

		pngTasks = append(pngTasks, pngDecodeTask{
			glyphIndex: i,
			codepoint:  codepoint,
			path:       file.path,
		})
	}

	decodedPNGs, err := decodePNGsParallel(pngTasks, metadata.symbolWidth, int(metadata.scale), options.Progress)
	if err != nil {
		return nil, CreateReport{}, err
	}
	for _, decoded := range decodedPNGs {
		if symbolHeight == -1 {
			symbolHeight = decoded.height
		} else if decoded.height != symbolHeight {
			return nil, CreateReport{}, fmt.Errorf(
				"all PNGs must have equal height: first=%d, U+%04X in %q=%d",
				symbolHeight,
				decoded.codepoint,
				decoded.path,
				decoded.height,
			)
		}

		if decoded.width > maxWidth {
			maxWidth = decoded.width
		}
		glyphs[decoded.glyphIndex] = glyphImage{
			width: decoded.width,
			img:   decoded.img,
		}
	}

	if symbolHeight == -1 {
		return nil, CreateReport{}, fmt.Errorf("cannot infer symbol_height: at least one PNG is required")
	}

	symbolStride := symbolStrideForWidth(maxWidth)
	if metadata.symbolStride != nil {
		if *metadata.symbolStride == 0 {
			return nil, CreateReport{}, fmt.Errorf("metadata symbol_stride must be > 0")
		}
		if uint64(*metadata.symbolStride) > uint64(int(^uint(0)>>1)) {
			return nil, CreateReport{}, fmt.Errorf("metadata symbol_stride is too large: %d", *metadata.symbolStride)
		}
		symbolStride = int(*metadata.symbolStride)
	}
	if symbolStride < symbolStrideForWidth(maxWidth) {
		return nil, CreateReport{}, fmt.Errorf(
			"symbol_stride=%d is too small for max symbol width=%d",
			symbolStride,
			maxWidth,
		)
	}

	symbols := make([]fnt.Symbol, 0, len(glyphs))
	codepointToSymbol := make(map[uint16]uint16, len(allCodepoints))
	symbolIndexByKey := make(map[string]uint16, len(glyphs))
	for i, glyph := range glyphs {
		rawData := make([]byte, symbolStride*symbolHeight)
		if glyph.width > 0 {
			rawData, err = imageToSymbolData(glyph.img, symbolStride, symbolHeight)
			if err != nil {
				return nil, CreateReport{}, fmt.Errorf("decode U+%04X: %w", allCodepoints[i], err)
			}
		}

		width := uint8(glyph.width)
		if options.DisableDedup {
			symbols = append(symbols, fnt.Symbol{
				Width: width,
				Data:  rawData,
			})
			codepointToSymbol[allCodepoints[i]] = uint16(len(symbols))
		} else {
			key := symbolDedupKey(width, rawData)
			if symbolIndex, exists := symbolIndexByKey[key]; exists {
				codepointToSymbol[allCodepoints[i]] = symbolIndex
			} else {
				symbols = append(symbols, fnt.Symbol{
					Width: width,
					Data:  rawData,
				})
				symbolIndex := uint16(len(symbols)) // Unicode table uses 1-based symbol indices.
				symbolIndexByKey[key] = symbolIndex
				codepointToSymbol[allCodepoints[i]] = symbolIndex
			}
		}

		reportProgress(options.Progress, ProgressStageImportEncodeSymbols, i+1, len(glyphs))
	}

	ideographWidth := uint32(maxWidth)
	if metadata.ideographWidth != nil {
		ideographWidth = *metadata.ideographWidth
	}
	if options.OverrideIdeographWidth {
		ideographWidth = options.IdeographWidth
	}

	fontHeight := uint32(symbolHeight)
	if metadata.fontHeight != nil {
		if *metadata.fontHeight == 0 {
			return nil, CreateReport{}, fmt.Errorf("metadata font_height must be > 0")
		}
		fontHeight = *metadata.fontHeight
	}
	if options.OverrideFontHeight {
		if options.FontHeight == 0 {
			return nil, CreateReport{}, fmt.Errorf("font height override must be > 0")
		}
		fontHeight = options.FontHeight
	}

	font := &fnt.Font{
		IdeographWidth: ideographWidth,
		SymbolStride:   uint32(symbolStride),
		SymbolHeight:   uint32(symbolHeight),
		FontHeight:     fontHeight,
		SymbolsCount:   uint32(len(symbols)),
		SymbolDataSize: uint32(1 + symbolStride*symbolHeight),
		Symbols:        symbols,
	}

	for _, codepoint := range allCodepoints {
		symbolIndex, ok := codepointToSymbol[codepoint]
		if !ok {
			return nil, CreateReport{}, fmt.Errorf("internal error: missing symbol mapping for U+%04X", codepoint)
		}
		font.UnicodeTable[codepoint] = symbolIndex
	}

	if _, err := font.MarshalBinary(); err != nil {
		return nil, CreateReport{}, fmt.Errorf("imported data is invalid: %w", err)
	}

	report := CreateReport{
		Codepoints:          len(allCodepoints),
		UniqueSymbols:       len(symbols),
		DeduplicatedSymbols: len(allCodepoints) - len(symbols),
		PNGFiles:            len(pngFiles),
		ZeroWidthCodepoints: zeroWidthCodepoints,
	}
	if options.DisableDedup {
		report.DeduplicatedSymbols = 0
	}

	return font, report, nil
}

func Validate(inDir string) (CreateReport, error) {
	return ValidateWithOptions(inDir, ImportOptions{})
}

func ValidateWithOptions(inDir string, options ImportOptions) (CreateReport, error) {
	_, report, err := ImportWithReport(inDir, options)
	return report, err
}

func decodePNGsParallel(
	tasks []pngDecodeTask,
	metadataWidth map[uint16]uint8,
	scale int,
	progress ProgressFunc,
) ([]decodedPNG, error) {
	if len(tasks) == 0 {
		return []decodedPNG{}, nil
	}

	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}

	results := make([]decodedPNG, len(tasks))
	jobs := make(chan int)
	outcomes := make(chan pngDecodeOutcome, len(tasks))
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jobIndex := range jobs {
				task := tasks[jobIndex]
				decoded, err := decodePNGTask(task, metadataWidth, scale)
				outcomes <- pngDecodeOutcome{
					jobIndex: jobIndex,
					decoded:  decoded,
					err:      err,
				}
			}
		}()
	}

	for i := range tasks {
		jobs <- i
	}
	close(jobs)

	var firstErr error
	doneCount := 0
	for i := 0; i < len(tasks); i++ {
		outcome := <-outcomes
		if outcome.err != nil {
			if firstErr == nil {
				firstErr = outcome.err
			}
			continue
		}
		results[outcome.jobIndex] = outcome.decoded
		doneCount++
		reportProgress(progress, ProgressStageImportReadPNGs, doneCount, len(tasks))
	}
	wg.Wait()
	close(outcomes)

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

type pngDecodeOutcome struct {
	jobIndex int
	decoded  decodedPNG
	err      error
}

func decodePNGTask(task pngDecodeTask, metadataWidth map[uint16]uint8, scale int) (decodedPNG, error) {
	img, err := readPNG(task.path)
	if err != nil {
		return decodedPNG{}, fmt.Errorf("read U+%04X from %q: %w", task.codepoint, task.path, err)
	}

	normalized, width, height, err := normalizeScaledPNG(img, scale, task.codepoint, task.path)
	if err != nil {
		return decodedPNG{}, err
	}
	if width <= 0 || width > 255 {
		return decodedPNG{}, fmt.Errorf(
			"U+%04X in %q has invalid logical PNG width=%d (allowed: 1..255)",
			task.codepoint,
			task.path,
			width,
		)
	}
	if height <= 0 {
		return decodedPNG{}, fmt.Errorf(
			"U+%04X in %q has invalid logical height=%d (allowed: >=1)",
			task.codepoint,
			task.path,
			height,
		)
	}

	if metadataGlyphWidth, hasMetadataWidth := metadataWidth[task.codepoint]; hasMetadataWidth {
		width = int(metadataGlyphWidth)
		if width == 0 {
			return decodedPNG{}, fmt.Errorf("U+%04X has metadata width=0 but PNG %q is present", task.codepoint, task.path)
		}
		if normalized.Bounds().Dx() != width {
			return decodedPNG{}, fmt.Errorf(
				"U+%04X width mismatch in %q: png=%d metadata=%d",
				task.codepoint,
				task.path,
				normalized.Bounds().Dx(),
				width,
			)
		}
	}

	return decodedPNG{
		glyphIndex: task.glyphIndex,
		codepoint:  task.codepoint,
		path:       task.path,
		width:      width,
		height:     height,
		img:        normalized,
	}, nil
}

func normalizeScaledPNG(img image.Image, scale int, codepoint uint16, path string) (image.Image, int, int, error) {
	if scale <= 1 {
		bounds := img.Bounds()
		return img, bounds.Dx(), bounds.Dy(), nil
	}

	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW%scale != 0 || srcH%scale != 0 {
		return nil, 0, 0, fmt.Errorf(
			"U+%04X in %q has dimensions %dx%d not divisible by metadata scale=%d",
			codepoint,
			path,
			srcW,
			srcH,
			scale,
		)
	}

	dstW := srcW / scale
	dstH := srcH / scale
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			blockMinX := bounds.Min.X + x*scale
			blockMinY := bounds.Min.Y + y*scale
			baseForeground := isForegroundPixel(img.At(blockMinX, blockMinY))

			for oy := 0; oy < scale; oy++ {
				for ox := 0; ox < scale; ox++ {
					px := blockMinX + ox
					py := blockMinY + oy
					if isForegroundPixel(img.At(px, py)) != baseForeground {
						return nil, 0, 0, fmt.Errorf(
							"U+%04X in %q has mixed pixels in scale block at x=%d y=%d (scale=%d)",
							codepoint,
							path,
							x,
							y,
							scale,
						)
					}
				}
			}

			if baseForeground {
				dst.SetNRGBA(x, y, pngForegroundColor)
			} else {
				dst.SetNRGBA(x, y, pngBackgroundColor)
			}
		}
	}

	return dst, dstW, dstH, nil
}

func collectCodepointPNGs(dir string) ([]codepointPNG, error) {
	files := make([]codepointPNG, 0)
	seen := make(map[uint16]string)

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if !strings.EqualFold(ext, ".png") {
			return nil
		}

		base := strings.TrimSuffix(name, ext)
		codepoint, err := parsePNGBaseName(base)
		if err != nil {
			return fmt.Errorf("invalid file name %q: %w", path, err)
		}

		if previous, exists := seen[codepoint]; exists {
			return fmt.Errorf("duplicate codepoint U+%04X in %q and %q", codepoint, previous, path)
		}
		seen[codepoint] = path

		files = append(files, codepointPNG{
			codepoint: codepoint,
			path:      path,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan input directory %q: %w", dir, err)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].codepoint < files[j].codepoint
	})

	return files, nil
}

func symbolDataToImage(data []byte, stride, height, width int) (*image.NRGBA, error) {
	if stride <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid symbol dimensions: stride=%d height=%d", stride, height)
	}
	if width <= 0 || width > stride*8 {
		return nil, fmt.Errorf("invalid symbol width=%d for stride=%d", width, stride)
	}
	if len(data) != stride*height {
		return nil, fmt.Errorf("invalid symbol data size: got %d, expected %d", len(data), stride*height)
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, pngBackgroundColor)
		}
	}

	for y := 0; y < height; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < width; x++ {
			if row[x/8]&(1<<(7-uint(x%8))) != 0 {
				img.SetNRGBA(x, y, pngForegroundColor)
			}
		}
	}

	return img, nil
}

func imageToSymbolData(img image.Image, stride, height int) ([]byte, error) {
	if stride <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid symbol dimensions: stride=%d height=%d", stride, height)
	}

	bounds := img.Bounds()
	actualWidth := bounds.Dx()
	actualHeight := bounds.Dy()
	if actualHeight != height {
		return nil, fmt.Errorf("invalid PNG height: got %d, expected %d", actualHeight, height)
	}
	if actualWidth <= 0 || actualWidth > stride*8 {
		return nil, fmt.Errorf("invalid PNG width: got %d, expected 1..%d", actualWidth, stride*8)
	}

	raw := make([]byte, stride*height)
	for y := 0; y < height; y++ {
		for x := 0; x < actualWidth; x++ {
			if isForegroundPixel(img.At(bounds.Min.X+x, bounds.Min.Y+y)) {
				rowOffset := y * stride
				raw[rowOffset+x/8] |= 1 << (7 - uint(x%8))
			}
		}
	}

	return raw, nil
}

func readPNG(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	img, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode png %q: %w", path, err)
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("encode png %q: %w", path, err)
	}
	return nil
}

func isForegroundPixel(pixel color.Color) bool {
	r, g, b, a := pixel.RGBA()
	fr, fg, fb, fa := color.NRGBAModel.Convert(pngForegroundColor).RGBA()

	return channelDiff(r, fr) <= pngChannelThreshold &&
		channelDiff(g, fg) <= pngChannelThreshold &&
		channelDiff(b, fb) <= pngChannelThreshold &&
		channelDiff(a, fa) <= pngChannelThreshold
}

func channelDiff(a, b uint32) uint32 {
	if a >= b {
		return a - b
	}
	return b - a
}

func symbolStrideForWidth(width int) int {
	if width <= 0 {
		return 1
	}
	return (width + 7) / 8
}

func parsePNGBaseName(base string) (codepoint uint16, err error) {
	parsed, err := parseHexCodepoint(base)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func parseHexCodepoint(value string) (uint16, error) {
	if len(value) != 2+hexIndexDigits {
		return 0, fmt.Errorf("expected codepoint format like 0x0000")
	}
	if value[0] != '0' || (value[1] != 'x' && value[1] != 'X') {
		return 0, fmt.Errorf("expected hex codepoint prefix 0x")
	}

	parsed, err := strconv.ParseUint(value[2:], 16, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid hex codepoint %q", value)
	}

	return uint16(parsed), nil
}

func formatCodepointFileName(codepoint uint16) string {
	return fmt.Sprintf("0x%0*X.png", hexIndexDigits, codepoint)
}

func directoryForCodepoint(codepoint uint16) string {
	for _, unicodeRange := range bmpRanges {
		if codepoint >= unicodeRange.Start && codepoint <= unicodeRange.End {
			return unicodeRange.Label
		}
	}
	return fmt.Sprintf("0x%04X-0x%04X (Unassigned)", codepoint, codepoint)
}

func writeMetadata(path string, payload metadataFile) error {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata json: %w", err)
	}
	raw = append(raw, '\n')

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write metadata file %q: %w", path, err)
	}

	return nil
}

func readMetadata(path string) (parsedMetadata, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return parsedMetadata{
				symbolWidth: make(map[uint16]uint8),
				scale:       1,
			}, nil
		}
		return parsedMetadata{}, fmt.Errorf("read metadata file %q: %w", path, err)
	}

	var payload metadataFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return parsedMetadata{}, fmt.Errorf("parse metadata file %q: %w", path, err)
	}

	parsed := parsedMetadata{
		symbolWidth:    make(map[uint16]uint8, len(payload.SymbolWidth)),
		symbolStride:   payload.SymbolStride,
		fontHeight:     payload.FontHeight,
		ideographWidth: payload.IdeographWidth,
		scale:          1,
	}
	if payload.Scale != nil {
		if *payload.Scale == 0 {
			return parsedMetadata{}, fmt.Errorf("metadata scale must be >= 1")
		}
		parsed.scale = *payload.Scale
	}
	for codepointKey, width := range payload.SymbolWidth {
		codepoint, err := parseHexCodepoint(strings.TrimSpace(codepointKey))
		if err != nil {
			return parsedMetadata{}, fmt.Errorf("invalid symbol_width codepoint %q in %q: %w", codepointKey, path, err)
		}
		if _, exists := parsed.symbolWidth[codepoint]; exists {
			return parsedMetadata{}, fmt.Errorf("duplicate symbol_width entry for U+%04X in %q", codepoint, path)
		}
		parsed.symbolWidth[codepoint] = width
	}

	return parsed, nil
}

func ptrUint32(v uint32) *uint32 {
	return &v
}

func symbolDedupKey(width uint8, data []byte) string {
	key := make([]byte, 1+len(data))
	key[0] = width
	copy(key[1:], data)
	return string(key)
}

func scaleImageInteger(src *image.NRGBA, scale int) *image.NRGBA {
	if scale <= 1 {
		return src
	}

	srcBounds := src.Bounds()
	dstW := srcBounds.Dx() * scale
	dstH := srcBounds.Dy() * scale
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < srcBounds.Dy(); y++ {
		for x := 0; x < srcBounds.Dx(); x++ {
			pixel := src.NRGBAAt(srcBounds.Min.X+x, srcBounds.Min.Y+y)
			dstMinX := x * scale
			dstMinY := y * scale
			for oy := 0; oy < scale; oy++ {
				for ox := 0; ox < scale; ox++ {
					dst.SetNRGBA(dstMinX+ox, dstMinY+oy, pixel)
				}
			}
		}
	}

	return dst
}

func reportProgress(progress ProgressFunc, stage string, done, total int) {
	if progress == nil || total <= 0 {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	progress(stage, done, total)
}
