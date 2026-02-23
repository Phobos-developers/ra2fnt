package fnt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// Westwood Unicode BitFont format reference:
// https://moddingwiki.shikadi.net/wiki/Westwood_Unicode_BitFont_Format
const (
	magic              = "fonT"
	headerSize         = 0x1c
	unicodeTableSize   = 65536
	unicodeTableBytes  = unicodeTableSize * 2
	magicSize          = 4
	computedHeaderSize = magicSize + 6*4
)

type Symbol struct {
	Width uint8
	Data  []byte
}

type Font struct {
	IdeographWidth uint32
	SymbolStride   uint32
	SymbolHeight   uint32
	FontHeight     uint32
	SymbolsCount   uint32
	SymbolDataSize uint32

	UnicodeTable [unicodeTableSize]uint16
	Symbols      []Symbol
	Tail         []byte
}

func ReadFile(path string) (*Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	font, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}

	return font, nil
}

func WriteFile(path string, font *Font) error {
	data, err := font.MarshalBinary()
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}

	return nil
}

func Parse(data []byte) (*Font, error) {
	minFileSize := headerSize + unicodeTableBytes
	if len(data) < minFileSize {
		return nil, fmt.Errorf("file too small: got %d bytes, need at least %d", len(data), minFileSize)
	}

	if string(data[:magicSize]) != magic {
		return nil, fmt.Errorf("unexpected magic %q, expected %q", data[:magicSize], magic)
	}

	font := &Font{
		IdeographWidth: binary.LittleEndian.Uint32(data[0x04:0x08]),
		SymbolStride:   binary.LittleEndian.Uint32(data[0x08:0x0c]),
		SymbolHeight:   binary.LittleEndian.Uint32(data[0x0c:0x10]),
		FontHeight:     binary.LittleEndian.Uint32(data[0x10:0x14]),
		SymbolsCount:   binary.LittleEndian.Uint32(data[0x14:0x18]),
		SymbolDataSize: binary.LittleEndian.Uint32(data[0x18:0x1c]),
	}

	if font.SymbolDataSize == 0 {
		return nil, fmt.Errorf("symbol_data_size is zero")
	}

	symbolsBytesU64 := uint64(font.SymbolsCount) * uint64(font.SymbolDataSize)
	if symbolsBytesU64 > math.MaxInt {
		return nil, fmt.Errorf("symbols block is too large: %d bytes", symbolsBytesU64)
	}

	symbolsBytes := int(symbolsBytesU64)
	symbolsOffset := headerSize + unicodeTableBytes
	if len(data) < symbolsOffset+symbolsBytes {
		return nil, fmt.Errorf(
			"file truncated: symbols need %d bytes, only %d remain",
			symbolsBytes,
			len(data)-symbolsOffset,
		)
	}

	tableOffset := headerSize
	for i := range font.UnicodeTable {
		off := tableOffset + i*2
		font.UnicodeTable[i] = binary.LittleEndian.Uint16(data[off : off+2])
	}

	font.Symbols = make([]Symbol, font.SymbolsCount)
	blockSize := int(font.SymbolDataSize)
	rawDataLen := blockSize - 1
	for i := range font.Symbols {
		blockStart := symbolsOffset + i*blockSize
		block := data[blockStart : blockStart+blockSize]
		raw := make([]byte, rawDataLen)
		copy(raw, block[1:])
		font.Symbols[i] = Symbol{
			Width: block[0],
			Data:  raw,
		}
	}

	tailOffset := symbolsOffset + symbolsBytes
	if tailOffset < len(data) {
		font.Tail = append([]byte(nil), data[tailOffset:]...)
	}

	return font, nil
}

func (font *Font) MarshalBinary() ([]byte, error) {
	if font == nil {
		return nil, fmt.Errorf("font is nil")
	}

	if err := font.validate(); err != nil {
		return nil, err
	}

	symbolsBytesU64 := uint64(font.SymbolsCount) * uint64(font.SymbolDataSize)
	if symbolsBytesU64 > math.MaxInt {
		return nil, fmt.Errorf("symbols block is too large: %d bytes", symbolsBytesU64)
	}

	totalSizeU64 := uint64(headerSize + unicodeTableBytes)
	totalSizeU64 += symbolsBytesU64
	totalSizeU64 += uint64(len(font.Tail))
	if totalSizeU64 > math.MaxInt {
		return nil, fmt.Errorf("font file is too large: %d bytes", totalSizeU64)
	}

	out := make([]byte, int(totalSizeU64))
	copy(out[:magicSize], []byte(magic))

	binary.LittleEndian.PutUint32(out[0x04:0x08], font.IdeographWidth)
	binary.LittleEndian.PutUint32(out[0x08:0x0c], font.SymbolStride)
	binary.LittleEndian.PutUint32(out[0x0c:0x10], font.SymbolHeight)
	binary.LittleEndian.PutUint32(out[0x10:0x14], font.FontHeight)
	binary.LittleEndian.PutUint32(out[0x14:0x18], font.SymbolsCount)
	binary.LittleEndian.PutUint32(out[0x18:0x1c], font.SymbolDataSize)

	tableOffset := headerSize
	for i, value := range font.UnicodeTable {
		off := tableOffset + i*2
		binary.LittleEndian.PutUint16(out[off:off+2], value)
	}

	symbolsOffset := headerSize + unicodeTableBytes
	blockSize := int(font.SymbolDataSize)
	for i, symbol := range font.Symbols {
		blockStart := symbolsOffset + i*blockSize
		block := out[blockStart : blockStart+blockSize]
		block[0] = symbol.Width
		copy(block[1:], symbol.Data)
	}

	copy(out[symbolsOffset+int(symbolsBytesU64):], font.Tail)
	return out, nil
}

func (font *Font) validate() error {
	if font.SymbolDataSize == 0 {
		return fmt.Errorf("symbol_data_size is zero")
	}
	if font.SymbolDataSize < 1 {
		return fmt.Errorf("symbol_data_size is invalid: %d", font.SymbolDataSize)
	}

	if len(font.Symbols) != int(font.SymbolsCount) {
		return fmt.Errorf("symbols count mismatch: header=%d, actual=%d", font.SymbolsCount, len(font.Symbols))
	}

	rawLen := int(font.SymbolDataSize) - 1
	for i, symbol := range font.Symbols {
		if len(symbol.Data) != rawLen {
			return fmt.Errorf(
				"symbol %d has invalid data size: got %d, expected %d",
				i,
				len(symbol.Data),
				rawLen,
			)
		}
	}

	// Header in this format is fixed-size and should stay 0x1c bytes.
	if computedHeaderSize != headerSize {
		return fmt.Errorf("internal header layout mismatch")
	}

	return nil
}
