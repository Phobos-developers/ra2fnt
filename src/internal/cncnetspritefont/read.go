package cncnetspritefont

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"unicode/utf8"

	lzx "github.com/secDre4mer/lzx"

	"github.com/pierrec/lz4/v4"

	"ra2fnt/src/internal/fnt"
)

const (
	xnbFlagsCompressedLZ4 = xnbFlagsCompressed
	xnbFlagsCompressedLZX = 0x80

	xnbCompressedPayloadOffset = xnbHeaderSize + 4

	xnbLZXWindowSize   = 1 << 16
	xnbLZXFrameSize    = 0x8000
	dxtAlphaThreshold  = 0x80
)

type parser struct {
	raw []byte
	pos int
}

func ReadFile(path string) (*fnt.Font, error) {
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

func Parse(data []byte) (*fnt.Font, error) {
	payload, err := decompressXNBPayload(data)
	if err != nil {
		return nil, err
	}
	return parseSpriteFontPayload(payload)
}

func decompressXNBPayload(data []byte) ([]byte, error) {
	if len(data) < xnbHeaderSize {
		return nil, fmt.Errorf("xnb file too small: %d bytes", len(data))
	}
	if string(data[:3]) != xnbMagic {
		return nil, fmt.Errorf("unexpected xnb magic %q", data[:3])
	}
	if data[3] != xnbPlatformWindows {
		return nil, fmt.Errorf("unsupported xnb platform %q", data[3])
	}
	if data[4] != xnbVersion5 {
		return nil, fmt.Errorf("unsupported xnb version %d", data[4])
	}

	flags := data[5]
	hasLZ4 := flags&xnbFlagsCompressedLZ4 != 0
	hasLZX := flags&xnbFlagsCompressedLZX != 0
	if hasLZ4 && hasLZX {
		return nil, fmt.Errorf("xnb file sets both LZ4 and LZX compression flags")
	}

	xnbLength := int(binary.LittleEndian.Uint32(data[6:10]))
	if xnbLength != len(data) {
		return nil, fmt.Errorf("xnb file size mismatch: header=%d actual=%d", xnbLength, len(data))
	}

	if !hasLZ4 && !hasLZX {
		payload := make([]byte, len(data[xnbHeaderSize:]))
		copy(payload, data[xnbHeaderSize:])
		return payload, nil
	}
	if len(data) < xnbCompressedPayloadOffset {
		return nil, fmt.Errorf("compressed xnb file too small: %d bytes", len(data))
	}

	decompressedSize := int(binary.LittleEndian.Uint32(data[xnbHeaderSize:xnbCompressedPayloadOffset]))
	if decompressedSize <= 0 {
		return nil, fmt.Errorf("invalid decompressed payload size: %d", decompressedSize)
	}

	compressedPayload := data[xnbCompressedPayloadOffset:]
	switch {
	case hasLZ4:
		return decompressLZ4Payload(compressedPayload, decompressedSize)
	case hasLZX:
		return decompressLZXPayload(compressedPayload, decompressedSize)
	default:
		return nil, fmt.Errorf("unsupported xnb compression flags 0x%02X", flags)
	}
}

func decompressLZ4Payload(src []byte, size int) ([]byte, error) {
	dst := make([]byte, size)
	n, err := lz4.UncompressBlock(src, dst)
	if err != nil {
		return nil, fmt.Errorf("decompress LZ4 payload: %w", err)
	}
	if n != size {
		return nil, fmt.Errorf("decompressed LZ4 payload size mismatch: got %d want %d", n, size)
	}
	return dst, nil
}

func decompressLZXPayload(src []byte, size int) ([]byte, error) {
	stripped, err := stripLZXFrameHeaders(src)
	if err != nil {
		return nil, err
	}
	reader, err := lzx.New(bytes.NewReader(stripped), xnbLZXWindowSize, xnbLZXFrameSize)
	if err != nil {
		return nil, fmt.Errorf("initialize LZX decoder: %w", err)
	}

	payload, err := io.ReadAll(io.LimitReader(reader, int64(size)))
	if err != nil {
		return nil, fmt.Errorf("decompress LZX payload: %w", err)
	}
	if len(payload) != size {
		return nil, fmt.Errorf("decompressed LZX payload size mismatch: got %d want %d", len(payload), size)
	}
	return payload, nil
}

func stripLZXFrameHeaders(src []byte) ([]byte, error) {
	var raw bytes.Buffer
	pos := 0

	for pos < len(src) {
		if pos+2 > len(src) {
			return nil, fmt.Errorf("truncated LZX frame header at offset %d", pos)
		}

		hi := int(src[pos])
		lo := int(src[pos+1])
		pos += 2

		blockSize := (hi << 8) | lo
		frameSize := xnbLZXFrameSize
		if hi == 0xFF {
			if pos+3 > len(src) {
				return nil, fmt.Errorf("truncated custom LZX frame header at offset %d", pos-2)
			}
			frameSize = (lo << 8) | int(src[pos])
			pos++
			hi = int(src[pos])
			lo = int(src[pos+1])
			pos += 2
			blockSize = (hi << 8) | lo
		}

		if blockSize == 0 || frameSize == 0 {
			break
		}
		if pos+blockSize > len(src) {
			return nil, fmt.Errorf("truncated LZX frame at offset %d: block=%d remain=%d", pos, blockSize, len(src)-pos)
		}

		raw.Write(src[pos : pos+blockSize])
		pos += blockSize
	}

	if raw.Len() == 0 {
		return nil, fmt.Errorf("LZX payload has no compressed frames")
	}
	return raw.Bytes(), nil
}

func parseSpriteFontPayload(payload []byte) (*fnt.Font, error) {
	p := &parser{raw: payload}

	typeReaderCount, err := p.read7BitEncodedInt()
	if err != nil {
		return nil, err
	}
	if typeReaderCount <= 0 {
		return nil, fmt.Errorf("spritefont payload has invalid type reader count %d", typeReaderCount)
	}
	for i := 0; i < typeReaderCount; i++ {
		if _, err := p.readString(); err != nil {
			return nil, err
		}
		if _, err := p.readInt32(); err != nil {
			return nil, err
		}
	}

	sharedResources, err := p.read7BitEncodedInt()
	if err != nil {
		return nil, err
	}
	if sharedResources != 0 {
		return nil, fmt.Errorf("spritefont payload with shared resources is not supported")
	}

	if _, err := p.read7BitEncodedInt(); err != nil { // root reader index
		return nil, err
	}
	if _, err := p.read7BitEncodedInt(); err != nil { // texture reader index
		return nil, err
	}

	atlas, err := p.readTexture()
	if err != nil {
		return nil, err
	}

	if _, err := p.read7BitEncodedInt(); err != nil {
		return nil, err
	}
	glyphRects, err := p.readRects()
	if err != nil {
		return nil, err
	}

	if _, err := p.read7BitEncodedInt(); err != nil {
		return nil, err
	}
	cropRects, err := p.readRects()
	if err != nil {
		return nil, err
	}

	if _, err := p.read7BitEncodedInt(); err != nil {
		return nil, err
	}
	chars, err := p.readChars()
	if err != nil {
		return nil, err
	}

	lineSpacing, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if _, err := p.readFloat32(); err != nil { // spacing
		return nil, err
	}

	if _, err := p.read7BitEncodedInt(); err != nil {
		return nil, err
	}
	kernings, err := p.readVec3s()
	if err != nil {
		return nil, err
	}
	if _, _, err := p.readOptionalChar(); err != nil {
		return nil, err
	}

	if p.remaining() != 0 {
		return nil, fmt.Errorf("spritefont payload has %d trailing bytes", p.remaining())
	}
	if len(glyphRects) != len(cropRects) || len(glyphRects) != len(chars) || len(glyphRects) != len(kernings) {
		return nil, fmt.Errorf(
			"spritefont list length mismatch: glyph_rects=%d crop_rects=%d chars=%d kernings=%d",
			len(glyphRects),
			len(cropRects),
			len(chars),
			len(kernings),
		)
	}

	return reconstructFont(atlas, glyphRects, cropRects, chars, kernings, lineSpacing)
}

func reconstructFont(atlas *image.Alpha, glyphRects, cropRects []rect, chars []rune, kernings []vec3, lineSpacing int32) (*fnt.Font, error) {
	symbolHeight := 1
	for _, cropRect := range cropRects {
		if int(cropRect.Height) > symbolHeight {
			symbolHeight = int(cropRect.Height)
		}
	}
	if symbolHeight <= 0 {
		symbolHeight = 1
	}

	type reconstructedSymbol struct {
		codepoint uint16
		width     int
		data      []byte
	}

	reconstructed := make([]reconstructedSymbol, 0, len(chars))
	maxWidth := 0
	for i, codepointRune := range chars {
		if codepointRune < 0 || codepointRune > math.MaxUint16 {
			return nil, fmt.Errorf("spritefont rune %U is outside BMP", codepointRune)
		}
		codepoint := uint16(codepointRune)
		if codepoint >= 0xD800 && codepoint <= 0xDFFF {
			return nil, fmt.Errorf("spritefont rune U+%04X is a surrogate codepoint", codepoint)
		}

		symbolImage, width, err := reconstructSymbolImage(atlas, glyphRects[i], cropRects[i], kernings[i], symbolHeight)
		if err != nil {
			return nil, fmt.Errorf("reconstruct symbol for U+%04X: %w", codepoint, err)
		}
		if width > maxWidth {
			maxWidth = width
		}
		reconstructed = append(reconstructed, reconstructedSymbol{
			codepoint: codepoint,
			width:     width,
			data:      alphaImageToSymbolData(symbolImage, width, symbolHeight, symbolStrideForWidth(maxInt(width, 1))),
		})
	}

	symbolStride := symbolStrideForWidth(maxInt(maxWidth, 1))
	font := &fnt.Font{
		IdeographWidth: uint32(maxInt(maxWidth, 1)),
		SymbolStride:   uint32(symbolStride),
		SymbolHeight:   uint32(symbolHeight),
		FontHeight:     uint32(maxInt(symbolHeight+1, int(lineSpacing)+1)),
		SymbolsCount:   uint32(len(reconstructed)),
		SymbolDataSize: uint32(symbolStride*symbolHeight + 1),
		Symbols:        make([]fnt.Symbol, len(reconstructed)),
	}

	for i, symbol := range reconstructed {
		if symbol.width > math.MaxUint8 {
			return nil, fmt.Errorf("symbol for U+%04X has width %d, exceeds 255", symbol.codepoint, symbol.width)
		}
		raw := expandSymbolData(symbol.data, symbolHeight, symbolStride)
		font.Symbols[i] = fnt.Symbol{
			Width: uint8(symbol.width),
			Data:  raw,
		}
		font.UnicodeTable[symbol.codepoint] = uint16(i + 1)
	}

	return font, nil
}

func reconstructSymbolImage(atlas *image.Alpha, glyphRect, cropRect rect, kerning vec3, symbolHeight int) (*image.Alpha, int, error) {
	if cropRect.Width == 0 && glyphRect.Width == 0 {
		return image.NewAlpha(image.Rect(0, 0, 0, symbolHeight)), 0, nil
	}

	if cropRect.Height <= 0 {
		cropRect.Height = int32(symbolHeight)
	}

	width := int(cropRect.Width)
	if width < 0 {
		return nil, 0, fmt.Errorf("negative crop width %d", cropRect.Width)
	}

	hasVisiblePixels, err := atlasRectHasVisiblePixels(atlas, glyphRect)
	if err != nil {
		return nil, 0, err
	}
	if hasVisiblePixels {
		inferredWidth := int(math.Round(float64(kerning.X + kerning.Y + kerning.Z - spriteFontGlyphGap)))
		if inferredWidth > width {
			width = inferredWidth
		}
		minVisibleWidth := int(math.Round(float64(kerning.X + kerning.Y)))
		if minVisibleWidth > width {
			width = minVisibleWidth
		}
	}

	if width < 0 {
		return nil, 0, fmt.Errorf("invalid symbol width %d", width)
	}
	if width == 0 {
		return image.NewAlpha(image.Rect(0, 0, 0, symbolHeight)), 0, nil
	}

	symbolImage := image.NewAlpha(image.Rect(0, 0, width, symbolHeight))
	if !hasVisiblePixels {
		return symbolImage, width, nil
	}

	dstX := int(math.Round(float64(kerning.X)))
	dstY := int(cropRect.Y)
	if dstX < 0 || dstY < 0 {
		return nil, 0, fmt.Errorf("negative glyph placement x=%d y=%d", dstX, dstY)
	}

	for y := 0; y < int(glyphRect.Height); y++ {
		for x := 0; x < int(glyphRect.Width); x++ {
			srcX := int(glyphRect.X) + x
			srcY := int(glyphRect.Y) + y
			if !image.Pt(srcX, srcY).In(atlas.Bounds()) {
				return nil, 0, fmt.Errorf("glyph rect %+v exceeds atlas bounds %v", glyphRect, atlas.Bounds())
			}
			alpha := atlas.AlphaAt(srcX, srcY).A
			if alpha < dxtAlphaThreshold {
				continue
			}

			targetX := dstX + x
			targetY := dstY + y
			if targetX < 0 || targetX >= width || targetY < 0 || targetY >= symbolHeight {
				return nil, 0, fmt.Errorf(
					"glyph pixel at x=%d y=%d exceeds symbol bounds %dx%d",
					targetX,
					targetY,
					width,
					symbolHeight,
				)
			}
			symbolImage.SetAlpha(targetX, targetY, color.Alpha{A: 0xFF})
		}
	}

	return symbolImage, width, nil
}

func atlasRectHasVisiblePixels(atlas *image.Alpha, glyphRect rect) (bool, error) {
	if glyphRect.Width <= 0 || glyphRect.Height <= 0 {
		return false, nil
	}
	for y := 0; y < int(glyphRect.Height); y++ {
		for x := 0; x < int(glyphRect.Width); x++ {
			srcX := int(glyphRect.X) + x
			srcY := int(glyphRect.Y) + y
			if !image.Pt(srcX, srcY).In(atlas.Bounds()) {
				return false, fmt.Errorf("glyph rect %+v exceeds atlas bounds %v", glyphRect, atlas.Bounds())
			}
			if atlas.AlphaAt(srcX, srcY).A >= dxtAlphaThreshold {
				return true, nil
			}
		}
	}
	return false, nil
}

func alphaImageToSymbolData(img *image.Alpha, width, height, stride int) []byte {
	if width <= 0 {
		return make([]byte, stride*height)
	}

	raw := make([]byte, stride*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if img.AlphaAt(x, y).A >= dxtAlphaThreshold {
				raw[y*stride+x/8] |= 1 << (7 - uint(x%8))
			}
		}
	}
	return raw
}

func expandSymbolData(raw []byte, height, targetStride int) []byte {
	if targetStride <= 0 {
		return nil
	}
	if len(raw) == targetStride*height {
		return raw
	}

	stride := 0
	if height > 0 {
		stride = len(raw) / height
	}
	expanded := make([]byte, targetStride*height)
	for y := 0; y < height; y++ {
		copy(expanded[y*targetStride:y*targetStride+stride], raw[y*stride:(y+1)*stride])
	}
	return expanded
}

func symbolStrideForWidth(width int) int {
	if width <= 0 {
		return 1
	}
	return (width + 7) / 8
}

func (p *parser) remaining() int {
	return len(p.raw) - p.pos
}

func (p *parser) readByte() (byte, error) {
	if p.pos >= len(p.raw) {
		return 0, io.ErrUnexpectedEOF
	}
	value := p.raw[p.pos]
	p.pos++
	return value, nil
}

func (p *parser) read7BitEncodedInt() (int, error) {
	value := 0
	shift := 0
	for {
		b, err := p.readByte()
		if err != nil {
			return 0, err
		}
		value |= int(b&0x7F) << shift
		if b&0x80 == 0 {
			return value, nil
		}
		shift += 7
	}
}

func (p *parser) readString() (string, error) {
	size, err := p.read7BitEncodedInt()
	if err != nil {
		return "", err
	}
	if p.pos+size > len(p.raw) {
		return "", io.ErrUnexpectedEOF
	}
	value := string(p.raw[p.pos : p.pos+size])
	p.pos += size
	return value, nil
}

func (p *parser) readInt32() (int32, error) {
	if p.pos+4 > len(p.raw) {
		return 0, io.ErrUnexpectedEOF
	}
	value := int32(binary.LittleEndian.Uint32(p.raw[p.pos : p.pos+4]))
	p.pos += 4
	return value, nil
}

func (p *parser) readFloat32() (float32, error) {
	value, err := p.readInt32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(uint32(value)), nil
}

func (p *parser) readRect() (rect, error) {
	x, err := p.readInt32()
	if err != nil {
		return rect{}, err
	}
	y, err := p.readInt32()
	if err != nil {
		return rect{}, err
	}
	width, err := p.readInt32()
	if err != nil {
		return rect{}, err
	}
	height, err := p.readInt32()
	if err != nil {
		return rect{}, err
	}
	return rect{X: x, Y: y, Width: width, Height: height}, nil
}

func (p *parser) readRects() ([]rect, error) {
	count, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("negative rect count %d", count)
	}
	rects := make([]rect, count)
	for i := range rects {
		rects[i], err = p.readRect()
		if err != nil {
			return nil, err
		}
	}
	return rects, nil
}

func (p *parser) readChar() (rune, error) {
	if p.pos >= len(p.raw) {
		return 0, io.ErrUnexpectedEOF
	}
	value, size := utf8.DecodeRune(p.raw[p.pos:])
	if value == utf8.RuneError && size == 1 {
		return 0, fmt.Errorf("invalid utf-8 char at offset %d", p.pos)
	}
	p.pos += size
	return value, nil
}

func (p *parser) readChars() ([]rune, error) {
	count, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("negative char count %d", count)
	}
	out := make([]rune, count)
	for i := range out {
		out[i], err = p.readChar()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *parser) readVec3() (vec3, error) {
	x, err := p.readFloat32()
	if err != nil {
		return vec3{}, err
	}
	y, err := p.readFloat32()
	if err != nil {
		return vec3{}, err
	}
	z, err := p.readFloat32()
	if err != nil {
		return vec3{}, err
	}
	return vec3{X: x, Y: y, Z: z}, nil
}

func (p *parser) readVec3s() ([]vec3, error) {
	count, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("negative vec3 count %d", count)
	}
	out := make([]vec3, count)
	for i := range out {
		out[i], err = p.readVec3()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *parser) readOptionalChar() (bool, rune, error) {
	b, err := p.readByte()
	if err != nil {
		return false, 0, err
	}
	if b == 0 {
		return false, 0, nil
	}
	value, err := p.readChar()
	if err != nil {
		return false, 0, err
	}
	return true, value, nil
}

func (p *parser) readTexture() (*image.Alpha, error) {
	surfaceFormat, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if surfaceFormat != surfaceFormatDXT3 {
		return nil, fmt.Errorf("unsupported SpriteFont surface format %d", surfaceFormat)
	}

	width, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	height, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid texture size %dx%d", width, height)
	}
	if width%dxtBlockSize != 0 || height%dxtBlockSize != 0 {
		return nil, fmt.Errorf("DXT3 texture size must be multiple of %d, got %dx%d", dxtBlockSize, width, height)
	}

	mipCount, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if mipCount != 1 {
		return nil, fmt.Errorf("unsupported mip count %d", mipCount)
	}

	rawSize, err := p.readInt32()
	if err != nil {
		return nil, err
	}
	if rawSize < 0 {
		return nil, fmt.Errorf("negative texture data size %d", rawSize)
	}
	if p.pos+int(rawSize) > len(p.raw) {
		return nil, io.ErrUnexpectedEOF
	}
	raw := p.raw[p.pos : p.pos+int(rawSize)]
	p.pos += int(rawSize)

	return decodeDXT3Alpha(raw, int(width), int(height))
}

func decodeDXT3Alpha(raw []byte, width, height int) (*image.Alpha, error) {
	expectedSize := (width / dxtBlockSize) * (height / dxtBlockSize) * dxtBlockBytes
	if len(raw) != expectedSize {
		return nil, fmt.Errorf("invalid DXT3 data size: got %d want %d", len(raw), expectedSize)
	}

	img := image.NewAlpha(image.Rect(0, 0, width, height))
	offset := 0
	for by := 0; by < height; by += dxtBlockSize {
		for bx := 0; bx < width; bx += dxtBlockSize {
			block := raw[offset : offset+dxtBlockBytes]
			offset += dxtBlockBytes

			for y := 0; y < dxtBlockSize; y++ {
				row := binary.LittleEndian.Uint16(block[y*2 : y*2+2])
				for x := 0; x < dxtBlockSize; x++ {
					alphaNibble := byte((row >> (x * 4)) & 0x0F)
					img.Pix[(by+y)*img.Stride+(bx+x)] = alphaNibble * 0x11
				}
			}
		}
	}
	return img, nil
}
