package fontout

import (
	"fmt"
	"strings"

	"ra2fnt/src/internal/cncnetspritefont"
	"ra2fnt/src/internal/fnt"
)

const (
	FormatFNT              = "fnt"
	FormatCnCNetSpriteFont = "cncnet-spritefont"
)

var supportedFormats = []string{
	FormatFNT,
	FormatCnCNetSpriteFont,
}

func SupportedFormats() []string {
	out := make([]string, len(supportedFormats))
	copy(out, supportedFormats)
	return out
}

func NormalizeFormat(value string) (string, error) {
	format := strings.TrimSpace(strings.ToLower(value))
	if format == "" {
		format = FormatFNT
	}

	switch format {
	case FormatFNT, FormatCnCNetSpriteFont:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported create format %q (supported: %s)", value, strings.Join(supportedFormats, ", "))
	}
}

func WriteFile(path string, font *fnt.Font, format string) error {
	resolvedFormat, err := NormalizeFormat(format)
	if err != nil {
		return err
	}

	switch resolvedFormat {
	case FormatFNT:
		return fnt.WriteFile(path, font)
	case FormatCnCNetSpriteFont:
		return cncnetspritefont.WriteFile(path, font)
	default:
		return fmt.Errorf("unsupported create format %q", resolvedFormat)
	}
}
