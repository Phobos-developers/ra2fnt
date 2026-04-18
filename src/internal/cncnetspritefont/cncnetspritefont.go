package cncnetspritefont

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/pierrec/lz4/v4"

	"ra2fnt/src/internal/fnt"
)

const (
	xnbMagic                      = "XNB"
	xnbPlatformWindows            = 'w'
	xnbVersion5                   = 5
	xnbFlagsCompressed            = 0x40
	xnbHeaderSize                 = 10
	xnbCompressedHeaderSize       = xnbHeaderSize + 4
	surfaceFormatDXT3       int32 = 5

	defaultCharacter rune = '?'

	// Match the spacing used by the client-side reference SpriteFont files.
	spriteFontGlyphGap float32 = 1

	// Reference SpriteFont atlases in the client use 2048px width.
	maxAtlasWidth = 2048
	// Keep a small gutter between glyphs inside the atlas texture.
	atlasPadding = 1
	// Start with at least four rows worth of width before packing more tightly.
	minAtlasWidthFactor = 4
	// Reference-like placeholder image for invisible glyphs with non-zero advance.
	placeholderGlyphSize = 1

	dxtBlockSize  = 4
	dxtBlockBytes = 16
	dxtColorWhite = 0xFFFF
	dxtColorBlack = 0x0000

	lz4CompressionLevel = lz4.Level9
)

const (
	typeReaderSpriteFont = "Microsoft.Xna.Framework.Content.SpriteFontReader, Microsoft.Xna.Framework.Graphics, Version=4.0.0.0, Culture=neutral, PublicKeyToken=842cf8be1de50553"
	typeReaderTexture2D  = "Microsoft.Xna.Framework.Content.Texture2DReader, Microsoft.Xna.Framework.Graphics, Version=4.0.0.0, Culture=neutral, PublicKeyToken=842cf8be1de50553"
	typeReaderListRect   = "Microsoft.Xna.Framework.Content.ListReader`1[[Microsoft.Xna.Framework.Rectangle, Microsoft.Xna.Framework, Version=4.0.0.0, Culture=neutral, PublicKeyToken=842cf8be1de50553]]"
	typeReaderRect       = "Microsoft.Xna.Framework.Content.RectangleReader"
	typeReaderListChar   = "Microsoft.Xna.Framework.Content.ListReader`1[[System.Char, mscorlib, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089]]"
	typeReaderChar       = "Microsoft.Xna.Framework.Content.CharReader"
	typeReaderListVec3   = "Microsoft.Xna.Framework.Content.ListReader`1[[Microsoft.Xna.Framework.Vector3, Microsoft.Xna.Framework, Version=4.0.0.0, Culture=neutral, PublicKeyToken=842cf8be1de50553]]"
	typeReaderVec3       = "Microsoft.Xna.Framework.Content.Vector3Reader"
)

const (
	rootReaderIndex     = 1
	textureReaderIndex  = 2
	rectListReaderIndex = 3
	charListReaderIndex = 5
	vec3ListReaderIndex = 7
)

var typeReaders = []string{
	typeReaderSpriteFont,
	typeReaderTexture2D,
	typeReaderListRect,
	typeReaderRect,
	typeReaderListChar,
	typeReaderChar,
	typeReaderListVec3,
	typeReaderVec3,
}

type glyph struct {
	codepoint uint16
	img       *image.Alpha
	glyphRect rect
	cropRect  rect
	kerning   vec3
}

type atlasLayout struct {
	glyphs []glyph
	width  int
	height int
}

type rect struct {
	X      int32
	Y      int32
	Width  int32
	Height int32
}

type vec3 struct {
	X float32
	Y float32
	Z float32
}

type writer struct {
	buf bytes.Buffer
}

func WriteFile(path string, font *fnt.Font) error {
	raw, err := MarshalBinary(font)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

func MarshalBinary(font *fnt.Font) ([]byte, error) {
	if err := validateFont(font); err != nil {
		return nil, err
	}

	glyphs, defaultChar, hasDefaultChar, err := buildGlyphs(font)
	if err != nil {
		return nil, err
	}

	layout, err := layoutGlyphs(glyphs, int(font.SymbolHeight))
	if err != nil {
		return nil, err
	}

	atlas := renderAtlas(layout)

	payload, err := marshalContent(layout.glyphs, atlas, spriteFontLineSpacing(font), defaultChar, hasDefaultChar)
	if err != nil {
		return nil, err
	}

	return marshalCompressedXNB(payload)
}

func validateFont(font *fnt.Font) error {
	if font == nil {
		return fmt.Errorf("font is nil")
	}
	if font.SymbolHeight == 0 {
		return fmt.Errorf("symbol_height must be > 0")
	}
	if font.FontHeight == 0 {
		return fmt.Errorf("font_height must be > 0")
	}
	if font.SymbolStride == 0 {
		return fmt.Errorf("symbol_stride must be > 0")
	}
	if len(font.Symbols) != int(font.SymbolsCount) {
		return fmt.Errorf("symbols count mismatch: header=%d actual=%d", font.SymbolsCount, len(font.Symbols))
	}

	rawSymbolSizeU64 := uint64(font.SymbolStride) * uint64(font.SymbolHeight)
	if rawSymbolSizeU64 > math.MaxInt {
		return fmt.Errorf("symbol data size is too large: %d bytes", rawSymbolSizeU64)
	}
	rawSymbolSize := int(rawSymbolSizeU64)
	expectedSymbolDataSize := uint32(rawSymbolSize + 1)
	if font.SymbolDataSize != expectedSymbolDataSize {
		return fmt.Errorf(
			"symbol_data_size mismatch: got %d want %d",
			font.SymbolDataSize,
			expectedSymbolDataSize,
		)
	}

	for i, symbol := range font.Symbols {
		if len(symbol.Data) != rawSymbolSize {
			return fmt.Errorf(
				"symbol %d has invalid data size: got %d want %d",
				i,
				len(symbol.Data),
				rawSymbolSize,
			)
		}
	}

	return nil
}

func spriteFontLineSpacing(font *fnt.Font) int32 {
	// Reference SpriteFonts used by xna-cncnet-client measure one pixel tighter
	// than the raw Westwood font cell height, which keeps checkbox labels centered.
	return int32(maxInt(int(font.SymbolHeight)-1, 1))
}

func marshalCompressedXNB(payload []byte) ([]byte, error) {
	compressedPayload, err := compressContent(payload)
	if err != nil {
		return nil, err
	}

	xnb := make([]byte, 0, xnbCompressedHeaderSize+len(compressedPayload))
	xnb = append(xnb, xnbMagic...)
	xnb = append(xnb, byte(xnbPlatformWindows))
	xnb = append(xnb, byte(xnbVersion5))
	xnb = append(xnb, byte(xnbFlagsCompressed))

	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(xnbCompressedHeaderSize+len(compressedPayload)))
	xnb = append(xnb, raw[:]...)
	binary.LittleEndian.PutUint32(raw[:], uint32(len(payload)))
	xnb = append(xnb, raw[:]...)
	xnb = append(xnb, compressedPayload...)

	return xnb, nil
}

func compressContent(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("content payload is empty")
	}

	dst := make([]byte, lz4.CompressBlockBound(len(payload)))
	n, err := lz4.CompressBlockHC(payload, dst, lz4CompressionLevel, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("compress SpriteFont XNB payload with LZ4: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("compress SpriteFont XNB payload with LZ4: compression returned %d bytes", n)
	}
	return dst[:n], nil
}

func buildGlyphs(font *fnt.Font) ([]glyph, rune, bool, error) {
	codepoints := make([]uint16, 0)
	for codepoint, symbolIndex := range font.UnicodeTable {
		if symbolIndex != 0 {
			codepoints = append(codepoints, uint16(codepoint))
		}
	}
	sort.Slice(codepoints, func(i, j int) bool {
		return codepoints[i] < codepoints[j]
	})
	if len(codepoints) == 0 {
		return nil, 0, false, fmt.Errorf("font has no mapped codepoints")
	}

	fullWidth := int(font.SymbolStride) * 8
	glyphs := make([]glyph, 0, len(codepoints))
	hasDefaultChar := false
	for _, codepoint := range codepoints {
		if codepoint >= 0xD800 && codepoint <= 0xDFFF {
			return nil, 0, false, fmt.Errorf("cncnet-spritefont does not support surrogate codepoint U+%04X", codepoint)
		}

		symbolIndex := int(font.UnicodeTable[codepoint]) - 1
		if symbolIndex < 0 || symbolIndex >= len(font.Symbols) {
			return nil, 0, false, fmt.Errorf(
				"unicode table maps U+%04X to invalid symbol index %d (symbols=%d)",
				codepoint,
				symbolIndex,
				len(font.Symbols),
			)
		}

		symbol := font.Symbols[symbolIndex]
		width := int(symbol.Width)
		if width > fullWidth {
			return nil, 0, false, fmt.Errorf("symbol %d width=%d exceeds stride width=%d", symbolIndex, width, fullWidth)
		}

		glyph, err := symbolToGlyph(codepoint, symbol.Data, int(font.SymbolStride), int(font.SymbolHeight), width)
		if err != nil {
			return nil, 0, false, fmt.Errorf("build SpriteFont glyph for U+%04X: %w", codepoint, err)
		}
		glyphs = append(glyphs, glyph)
		hasDefaultChar = hasDefaultChar || rune(codepoint) == defaultCharacter
	}

	return glyphs, defaultCharacter, hasDefaultChar, nil
}

func symbolToGlyph(codepoint uint16, data []byte, stride, height, width int) (glyph, error) {
	if stride <= 0 || height <= 0 {
		return glyph{}, fmt.Errorf("invalid symbol dimensions: stride=%d height=%d", stride, height)
	}
	if width < 0 || width > stride*8 {
		return glyph{}, fmt.Errorf("invalid symbol width=%d for stride=%d", width, stride)
	}
	if len(data) != stride*height {
		return glyph{}, fmt.Errorf("invalid symbol data size: got %d, expected %d", len(data), stride*height)
	}

	if width <= 0 {
		return glyph{
			codepoint: codepoint,
			cropRect: rect{
				Height: int32(height),
			},
		}, nil
	}

	bounds, hasPixels := symbolVisibleBounds(data, stride, height, width)
	if !hasPixels {
		return newInvisibleGlyphPlaceholder(codepoint, width, height), nil
	}

	return newVisibleGlyph(codepoint, data, stride, height, bounds), nil
}

// MonoGame reference SpriteFonts use a 1x1 placeholder texture region for glyphs that
// have advance width but no visible pixels, while keeping the logical width in crop/kerning.
func newInvisibleGlyphPlaceholder(codepoint uint16, width, height int) glyph {
	return glyph{
		codepoint: codepoint,
		img:       image.NewAlpha(image.Rect(0, 0, placeholderGlyphSize, placeholderGlyphSize)),
		cropRect: rect{
			X:      int32(maxInt(width-placeholderGlyphSize, 0)),
			Y:      int32(height),
			Width:  int32(width),
			Height: int32(height),
		},
		kerning: vec3{
			X: 0,
			Y: placeholderGlyphSize,
			Z: float32(maxInt(width-placeholderGlyphSize, 0)),
		},
	}
}

func newVisibleGlyph(codepoint uint16, data []byte, stride, height int, bounds image.Rectangle) glyph {
	img := rasterizeGlyph(data, stride, bounds)
	visibleWidth := bounds.Dx()

	return glyph{
		codepoint: codepoint,
		img:       img,
		cropRect: rect{
			X:      0,
			Y:      int32(bounds.Min.Y),
			Width:  int32(visibleWidth),
			Height: int32(height),
		},
		kerning: vec3{
			X: float32(bounds.Min.X),
			Y: float32(visibleWidth),
			Z: spriteFontGlyphGap,
		},
	}
}

func rasterizeGlyph(data []byte, stride int, bounds image.Rectangle) *image.Alpha {
	img := image.NewAlpha(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := data[y*stride : (y+1)*stride]
		dstOffset := (y - bounds.Min.Y) * img.Stride
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if row[x/8]&(1<<(7-uint(x%8))) != 0 {
				img.Pix[dstOffset+(x-bounds.Min.X)] = 0xFF
			}
		}
	}
	return img
}

func symbolVisibleBounds(data []byte, stride, height, width int) (image.Rectangle, bool) {
	minX := width
	minY := height
	maxX := -1
	maxY := -1

	for y := 0; y < height; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < width; x++ {
			if row[x/8]&(1<<(7-uint(x%8))) == 0 {
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < 0 || maxY < 0 {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

func layoutGlyphs(glyphs []glyph, symbolHeight int) (atlasLayout, error) {
	if len(glyphs) == 0 {
		return atlasLayout{}, fmt.Errorf("no glyphs to pack")
	}

	laidOut := append([]glyph(nil), glyphs...)
	atlasWidth := chooseAtlasWidth(laidOut, symbolHeight)
	x := 0
	y := 0
	rowHeight := 0
	atlasHeight := 0
	placedGlyphs := 0

	for i := range laidOut {
		if laidOut[i].img == nil {
			continue
		}
		w := laidOut[i].img.Bounds().Dx()
		h := laidOut[i].img.Bounds().Dy()
		if w <= 0 || h <= 0 {
			continue
		}
		if h > rowHeight {
			rowHeight = h
		}
		if x > 0 && x+w > atlasWidth {
			x = 0
			y += rowHeight + atlasPadding
			rowHeight = h
		}

		laidOut[i].glyphRect = rect{
			X:      int32(x),
			Y:      int32(y),
			Width:  int32(w),
			Height: int32(h),
		}

		x += w + atlasPadding
		placedGlyphs++
		if candidate := y + h; candidate > atlasHeight {
			atlasHeight = candidate
		}
	}

	if placedGlyphs == 0 {
		atlasWidth = dxtBlockSize
		atlasHeight = dxtBlockSize
	}

	return atlasLayout{
		glyphs: laidOut,
		width:  maxInt(atlasWidth, dxtBlockSize),
		height: maxInt(nextPowerOfTwo(maxInt(atlasHeight, 1)), dxtBlockSize),
	}, nil
}

func renderAtlas(layout atlasLayout) *image.Alpha {
	atlas := image.NewAlpha(image.Rect(0, 0, layout.width, layout.height))
	for _, glyph := range layout.glyphs {
		if glyph.img == nil {
			continue
		}
		minX := int(glyph.glyphRect.X)
		minY := int(glyph.glyphRect.Y)
		bounds := glyph.img.Bounds()
		for y := 0; y < bounds.Dy(); y++ {
			dstOffset := (minY+y)*atlas.Stride + minX
			srcOffset := y * glyph.img.Stride
			copy(atlas.Pix[dstOffset:dstOffset+bounds.Dx()], glyph.img.Pix[srcOffset:srcOffset+bounds.Dx()])
		}
	}
	return atlas
}

func chooseAtlasWidth(glyphs []glyph, symbolHeight int) int {
	maxWidth := dxtBlockSize
	totalArea := 0
	hasImages := false
	for _, glyph := range glyphs {
		if glyph.img == nil {
			continue
		}
		w := glyph.img.Bounds().Dx()
		h := glyph.img.Bounds().Dy()
		if w > maxWidth {
			maxWidth = w
		}
		totalArea += (w + atlasPadding) * (h + atlasPadding)
		hasImages = true
	}
	if !hasImages {
		return dxtBlockSize
	}

	target := int(math.Ceil(math.Sqrt(float64(totalArea))))
	if target < maxWidth {
		target = maxWidth
	}
	minTarget := symbolHeight * minAtlasWidthFactor
	if target < minTarget {
		target = minTarget
	}
	if target > maxAtlasWidth {
		target = maxAtlasWidth
	}
	return nextPowerOfTwo(target)
}

func nextPowerOfTwo(value int) int {
	if value <= 1 {
		return 1
	}
	result := 1
	for result < value {
		result <<= 1
	}
	return result
}

func marshalContent(glyphs []glyph, atlas *image.Alpha, lineSpacing int32, defaultChar rune, hasDefaultChar bool) ([]byte, error) {
	var w writer

	w.write7BitEncodedInt(len(typeReaders))
	for _, readerType := range typeReaders {
		w.writeString(readerType)
		w.writeInt32(0)
	}

	w.write7BitEncodedInt(0) // shared resources

	w.write7BitEncodedInt(rootReaderIndex)
	if err := w.writeTexture2D(atlas); err != nil {
		return nil, err
	}
	w.writeGlyphRectList(rectListReaderIndex, glyphs)
	w.writeGlyphCropList(rectListReaderIndex, glyphs)
	w.writeGlyphCharList(charListReaderIndex, glyphs)
	w.writeInt32(lineSpacing)
	w.writeFloat32(0)
	w.writeGlyphKerningList(vec3ListReaderIndex, glyphs)
	w.writeOptionalChar(hasDefaultChar, defaultChar)

	return w.buf.Bytes(), nil
}

func (w *writer) writeTexture2D(img *image.Alpha) error {
	raw, err := compressDXT3(img)
	if err != nil {
		return err
	}

	w.write7BitEncodedInt(textureReaderIndex)
	w.writeInt32(surfaceFormatDXT3)
	w.writeInt32(int32(img.Bounds().Dx()))
	w.writeInt32(int32(img.Bounds().Dy()))
	w.writeInt32(1)
	w.writeInt32(int32(len(raw)))
	w.writeBytes(raw)
	return nil
}

func compressDXT3(img *image.Alpha) ([]byte, error) {
	bounds := img.Bounds()
	if bounds.Dx()%dxtBlockSize != 0 || bounds.Dy()%dxtBlockSize != 0 {
		return nil, fmt.Errorf(
			"DXT3 image dimensions must be multiples of %d, got %dx%d",
			dxtBlockSize,
			bounds.Dx(),
			bounds.Dy(),
		)
	}

	blockCountX := bounds.Dx() / dxtBlockSize
	blockCountY := bounds.Dy() / dxtBlockSize
	raw := make([]byte, 0, blockCountX*blockCountY*dxtBlockBytes)

	for by := bounds.Min.Y; by < bounds.Max.Y; by += dxtBlockSize {
		for bx := bounds.Min.X; bx < bounds.Max.X; bx += dxtBlockSize {
			raw = append(raw, encodeDXT3Block(img, bx, by)...)
		}
	}
	return raw, nil
}

func encodeDXT3Block(img *image.Alpha, startX, startY int) []byte {
	var block [dxtBlockBytes]byte
	var colorIndices uint32

	for y := 0; y < dxtBlockSize; y++ {
		var row uint16
		offset := (startY+y)*img.Stride + startX
		for x := 0; x < dxtBlockSize; x++ {
			alpha := img.Pix[offset+x]
			nibble := uint16((uint32(alpha) * 15) / 255)
			row |= nibble << (x * 4)
			if alpha == 0 {
				colorIndices |= 1 << ((y*dxtBlockSize + x) * 2)
			}
		}
		binary.LittleEndian.PutUint16(block[y*2:], row)
	}

	binary.LittleEndian.PutUint16(block[8:], dxtColorWhite)
	binary.LittleEndian.PutUint16(block[10:], dxtColorBlack)
	binary.LittleEndian.PutUint32(block[12:], colorIndices)
	return block[:]
}

func (w *writer) writeGlyphRectList(readerIndex int, glyphs []glyph) {
	w.write7BitEncodedInt(readerIndex)
	w.writeInt32(int32(len(glyphs)))
	for _, glyph := range glyphs {
		w.writeRect(glyph.glyphRect)
	}
}

func (w *writer) writeGlyphCropList(readerIndex int, glyphs []glyph) {
	w.write7BitEncodedInt(readerIndex)
	w.writeInt32(int32(len(glyphs)))
	for _, glyph := range glyphs {
		w.writeRect(glyph.cropRect)
	}
}

func (w *writer) writeGlyphCharList(readerIndex int, glyphs []glyph) {
	w.write7BitEncodedInt(readerIndex)
	w.writeInt32(int32(len(glyphs)))
	for _, glyph := range glyphs {
		w.writeChar(rune(glyph.codepoint))
	}
}

func (w *writer) writeGlyphKerningList(readerIndex int, glyphs []glyph) {
	w.write7BitEncodedInt(readerIndex)
	w.writeInt32(int32(len(glyphs)))
	for _, glyph := range glyphs {
		w.writeVec3(glyph.kerning)
	}
}

func (w *writer) writeRect(value rect) {
	w.writeInt32(value.X)
	w.writeInt32(value.Y)
	w.writeInt32(value.Width)
	w.writeInt32(value.Height)
}

func (w *writer) writeVec3(value vec3) {
	w.writeFloat32(value.X)
	w.writeFloat32(value.Y)
	w.writeFloat32(value.Z)
}

func (w *writer) writeBytes(value []byte) {
	_, _ = w.buf.Write(value)
}

func (w *writer) writeOptionalChar(hasValue bool, value rune) {
	if hasValue {
		w.writeByte(1)
		w.writeChar(value)
		return
	}
	w.writeByte(0)
}

func (w *writer) writeByte(value byte) {
	w.buf.WriteByte(value)
}

func (w *writer) writeUInt32(value uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	w.writeBytes(raw[:])
}

func (w *writer) writeInt32(value int32) {
	w.writeUInt32(uint32(value))
}

func (w *writer) writeFloat32(value float32) {
	w.writeUInt32(math.Float32bits(value))
}

func (w *writer) writeString(value string) {
	raw := []byte(value)
	w.write7BitEncodedInt(len(raw))
	w.writeBytes(raw)
}

func (w *writer) writeChar(value rune) {
	var raw [utf8.UTFMax]byte
	size := utf8.EncodeRune(raw[:], value)
	w.writeBytes(raw[:size])
}

func (w *writer) write7BitEncodedInt(value int) {
	for value >= 0x80 {
		w.writeByte(byte(value) | 0x80)
		value >>= 7
	}
	w.writeByte(byte(value))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
