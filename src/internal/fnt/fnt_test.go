package fnt

import (
	"bytes"
	"testing"
)

func TestMarshalParseRoundTrip(t *testing.T) {
	original := sampleFont()

	raw, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse marshaled data: %v", err)
	}

	rawAgain, err := parsed.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal parsed font: %v", err)
	}

	if !bytes.Equal(raw, rawAgain) {
		t.Fatalf("binary mismatch after parse+marshal")
	}
}

func sampleFont() *Font {
	font := &Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     2,
		SymbolsCount:   2,
		SymbolDataSize: 3, // 1 byte width + 1*2 bytes bitmap
		Symbols: []Symbol{
			{Width: 1, Data: []byte{0b1000_0000, 0b0000_0000}},
			{Width: 2, Data: []byte{0b0100_0000, 0b1000_0000}},
		},
		Tail: []byte{0xaa, 0xbb, 0xcc},
	}

	font.UnicodeTable['A'] = 1
	font.UnicodeTable['B'] = 2
	return font
}
