package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ra2fnt/src/internal/cncnetspritefont"
	"ra2fnt/src/internal/fnt"
	"ra2fnt/src/internal/fontout"
	"ra2fnt/src/internal/pngset"
)

const progressBarWidth = 30
const experimentalCnCNetSpriteFontWarning = "warning: cncnet-spritefont is experimental"
const (
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiReset  = "\033[0m"
)

var version = "dev"

type progressBar struct {
	operation   string
	lastStage   string
	lastPercent int
	printed     bool
}

func newProgressBar(operation string) *progressBar {
	return &progressBar{
		operation:   operation,
		lastPercent: -1,
	}
}

func (bar *progressBar) Update(stage string, done, total int) {
	if total <= 0 {
		return
	}

	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}

	percent := done * 100 / total
	if stage == bar.lastStage && percent == bar.lastPercent && done != total {
		return
	}

	filled := done * progressBarWidth / total
	if filled < 0 {
		filled = 0
	}
	if filled > progressBarWidth {
		filled = progressBarWidth
	}

	barLine := strings.Repeat("#", filled) + strings.Repeat("-", progressBarWidth-filled)
	fmt.Fprintf(
		os.Stderr,
		"\r%s: %s [%s] %3d%% (%d/%d)",
		bar.operation,
		progressStageLabel(stage),
		barLine,
		percent,
		done,
		total,
	)

	bar.lastStage = stage
	bar.lastPercent = percent
	bar.printed = true
}

func (bar *progressBar) Finish() {
	if bar.printed {
		fmt.Fprintln(os.Stderr)
		bar.printed = false
	}
}

func progressStageLabel(stage string) string {
	switch stage {
	case pngset.ProgressStageExportGlyphs:
		return "writing PNG"
	case pngset.ProgressStageImportReadPNGs:
		return "reading PNG"
	case pngset.ProgressStageImportEncodeSymbols:
		return "building symbols"
	default:
		return stage
	}
}

var stderrSupportsANSI = detectStderrANSI

func detectStderrANSI() bool {
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func colorizeWarning(text string) string {
	if !stderrSupportsANSI() {
		return text
	}
	return ansiYellow + text + ansiReset
}

func colorizeError(text string) string {
	if !stderrSupportsANSI() {
		return text
	}
	return ansiRed + text + ansiReset
}

func experimentalFormatLabel(format string) string {
	return fmt.Sprintf("%s (%s)", format, colorizeWarning("experimental"))
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		printVersion()
		return
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "export":
		err = runExport(os.Args[2:])
	case "create":
		err = runCreate(os.Args[2:])
	case "validate":
		err = runValidate(os.Args[2:])
	case "version":
		printVersion()
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, colorizeError("error:"), err)
		os.Exit(1)
	}
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	inPath := fs.String("in", "", "input .fnt or cncnet-spritefont .xnb file")
	outDir := fs.String("out", "", "output directory for png set")
	scale := fs.Int("scale", 1, "integer export scale (>=1)")
	force := fs.Bool("force", false, "delete existing output directory without confirmation")
	fs.SetOutput(os.Stderr)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" || *outDir == "" {
		fs.Usage()
		return fmt.Errorf("-in and -out are required")
	}
	if *scale < 1 {
		return fmt.Errorf("-scale must be >= 1")
	}

	shouldContinue, err := ensureExportOutDir(*outDir, os.Stdin, os.Stderr, *force)
	if err != nil {
		return err
	}
	if !shouldContinue {
		fmt.Fprintln(os.Stderr, "export cancelled")
		return nil
	}

	font, err := readFontForExport(*inPath)
	if err != nil {
		return err
	}
	progress := newProgressBar("export")
	defer progress.Finish()

	if err := pngset.ExportWithOptions(font, *outDir, pngset.ExportOptions{
		Progress: progress.Update,
		Scale:    *scale,
	}); err != nil {
		return err
	}
	progress.Finish()

	mappedCodepoints := 0
	for _, symbolIndex := range font.UnicodeTable {
		if symbolIndex != 0 {
			mappedCodepoints++
		}
	}

	fmt.Fprintf(
		os.Stderr,
		"exported %d codepoints to %s (source symbols: %d)\n",
		mappedCodepoints,
		*outDir,
		font.SymbolsCount,
	)
	return nil
}

func readFontForExport(path string) (*fnt.Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	switch {
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("fonT")):
		font, err := fnt.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %q as .fnt: %w", path, err)
		}
		return font, nil
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("XNB")):
		font, err := cncnetspritefont.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %q as cncnet-spritefont: %w", path, err)
		}
		return font, nil
	default:
		return nil, fmt.Errorf("unsupported input format for %q", path)
	}
}

func ensureExportOutDir(outDir string, input io.Reader, output io.Writer, force bool) (bool, error) {
	info, err := os.Stat(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("check output directory %q: %w", outDir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("output path %q exists and is not a directory", outDir)
	}
	if force {
		if err := os.RemoveAll(outDir); err != nil {
			return false, fmt.Errorf("remove output directory %q: %w", outDir, err)
		}
		return true, nil
	}

	fmt.Fprintf(output, "output directory %q already exists. Delete it and continue? [y/N]: ", outDir)
	reader := bufio.NewReader(input)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", readErr)
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return false, nil
	}

	if err := os.RemoveAll(outDir); err != nil {
		return false, fmt.Errorf("remove output directory %q: %w", outDir, err)
	}
	return true, nil
}

func runCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	inDir := fs.String("in", "", "input directory created by export")
	outPath := fs.String("out", "", "output font file")
	format := fs.String("format", fontout.FormatFNT, "create output format: fnt, "+experimentalFormatLabel(fontout.FormatCnCNetSpriteFont))
	noDedup := fs.Bool("no-dedup", false, "disable glyph deduplication")
	fs.SetOutput(os.Stderr)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inDir == "" || *outPath == "" {
		fs.Usage()
		return fmt.Errorf("-in and -out are required")
	}
	outputFormat, err := fontout.NormalizeFormat(*format)
	if err != nil {
		return err
	}
	if outputFormat == fontout.FormatCnCNetSpriteFont {
		fmt.Fprintf(os.Stderr, "%s\n", colorizeWarning(experimentalCnCNetSpriteFontWarning))
	}

	options := pngset.ImportOptions{}
	options.DisableDedup = *noDedup

	progress := newProgressBar("create")
	defer progress.Finish()

	options.Progress = progress.Update
	font, report, err := pngset.ImportWithReport(*inDir, options)
	if err != nil {
		return err
	}
	if err := fontout.WriteFile(*outPath, font, outputFormat); err != nil {
		return err
	}
	progress.Finish()

	fmt.Fprintf(
		os.Stderr,
		"created %d symbols from %d codepoints in %s (%s, deduplicated: %d)\n",
		report.UniqueSymbols,
		report.Codepoints,
		*outPath,
		outputFormat,
		report.DeduplicatedSymbols,
	)
	return nil
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	inDir := fs.String("in", "", "input directory created by export")
	noDedup := fs.Bool("no-dedup", false, "simulate create without glyph deduplication")
	fs.SetOutput(os.Stderr)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inDir == "" {
		fs.Usage()
		return fmt.Errorf("-in is required")
	}

	report, err := pngset.ValidateWithOptions(*inDir, pngset.ImportOptions{
		DisableDedup: *noDedup,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stderr,
		"validation passed: codepoints=%d, png=%d, zero_width=%d, symbols=%d, deduplicated=%d\n",
		report.Codepoints,
		report.PNGFiles,
		report.ZeroWidthCodepoints,
		report.UniqueSymbols,
		report.DeduplicatedSymbols,
	)
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s export -in input_font -out out_dir [--scale N] [--force]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s create -in out_dir -out output_file [--format fnt|cncnet-spritefont]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "    %s\n", colorizeWarning("cncnet-spritefont is experimental"))
	fmt.Fprintf(os.Stderr, "  %s validate -in out_dir\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s version\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s --version\n", os.Args[0])
}

func printVersion() {
	fmt.Println(versionString())
}

func versionString() string {
	return fmt.Sprintf("ra2fnt version %s", version)
}
