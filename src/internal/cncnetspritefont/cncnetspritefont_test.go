package cncnetspritefont

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"testing"
	"unicode/utf8"

	"github.com/pierrec/lz4/v4"

	"ra2fnt/src/internal/fnt"
)

func TestBuildGlyphsMatchesReferenceModel(t *testing.T) {
	font := sampleReferenceLikeFont()

	glyphs, defaultChar, hasDefaultChar, err := buildGlyphs(font)
	if err != nil {
		t.Fatalf("buildGlyphs: %v", err)
	}

	if got, want := len(glyphs), 4; got != want {
		t.Fatalf("glyph count mismatch: got=%d want=%d", got, want)
	}
	if !hasDefaultChar {
		t.Fatalf("expected default character to be present")
	}
	if got, want := defaultChar, rune('?'); got != want {
		t.Fatalf("default character mismatch: got=%q want=%q", got, want)
	}

	space := glyphs[0]
	if got, want := rune(space.codepoint), rune(' '); got != want {
		t.Fatalf("space codepoint mismatch: got=%q want=%q", got, want)
	}
	if space.img == nil {
		t.Fatalf("space placeholder glyph image is nil")
	}
	if got, want := space.img.Bounds().Dx(), 1; got != want {
		t.Fatalf("space placeholder width mismatch: got=%d want=%d", got, want)
	}
	if got, want := space.img.Bounds().Dy(), 1; got != want {
		t.Fatalf("space placeholder height mismatch: got=%d want=%d", got, want)
	}
	if got, want := space.cropRect, (rect{X: 2, Y: 4, Width: 3, Height: 4}); got != want {
		t.Fatalf("space crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := space.kerning, (vec3{X: 0, Y: 1, Z: 2}); got != want {
		t.Fatalf("space kerning mismatch: got=%+v want=%+v", got, want)
	}

	question := glyphs[1]
	if got, want := rune(question.codepoint), rune('?'); got != want {
		t.Fatalf("question codepoint mismatch: got=%q want=%q", got, want)
	}
	if question.img == nil {
		t.Fatalf("question glyph image is nil")
	}
	if got, want := question.img.Bounds().Dx(), 2; got != want {
		t.Fatalf("question tight width mismatch: got=%d want=%d", got, want)
	}
	if got, want := question.img.Bounds().Dy(), 2; got != want {
		t.Fatalf("question tight height mismatch: got=%d want=%d", got, want)
	}
	if got, want := question.cropRect, (rect{X: 0, Y: 0, Width: 2, Height: 4}); got != want {
		t.Fatalf("question crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := question.kerning, (vec3{X: 0, Y: 2, Z: 1}); got != want {
		t.Fatalf("question kerning mismatch: got=%+v want=%+v", got, want)
	}

	letterA := glyphs[2]
	if got, want := rune(letterA.codepoint), rune('A'); got != want {
		t.Fatalf("A codepoint mismatch: got=%q want=%q", got, want)
	}
	if letterA.img == nil {
		t.Fatalf("A glyph image is nil")
	}
	if got, want := letterA.img.Bounds().Dx(), 2; got != want {
		t.Fatalf("A tight width mismatch: got=%d want=%d", got, want)
	}
	if got, want := letterA.img.Bounds().Dy(), 2; got != want {
		t.Fatalf("A tight height mismatch: got=%d want=%d", got, want)
	}
	if got, want := letterA.cropRect, (rect{X: 0, Y: 1, Width: 2, Height: 4}); got != want {
		t.Fatalf("A crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := letterA.kerning, (vec3{X: 1, Y: 2, Z: 1}); got != want {
		t.Fatalf("A kerning mismatch: got=%+v want=%+v", got, want)
	}

	zeroWidth := glyphs[3]
	if got, want := rune(zeroWidth.codepoint), rune('B'); got != want {
		t.Fatalf("B codepoint mismatch: got=%q want=%q", got, want)
	}
	if zeroWidth.img != nil {
		t.Fatalf("zero-width glyph should not have atlas image")
	}
	if got, want := zeroWidth.cropRect, (rect{X: 0, Y: 0, Width: 0, Height: 4}); got != want {
		t.Fatalf("zero-width crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := zeroWidth.kerning, (vec3{}); got != want {
		t.Fatalf("zero-width kerning mismatch: got=%+v want=%+v", got, want)
	}
}

func TestMarshalBinaryWritesReferenceLikeSpriteFontXNB(t *testing.T) {
	font := sampleReferenceLikeFont()

	raw, err := MarshalBinary(font)
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	if got, want := string(raw[:3]), xnbMagic; got != want {
		t.Fatalf("magic mismatch: got=%q want=%q", got, want)
	}
	if got, want := raw[3], byte(xnbPlatformWindows); got != want {
		t.Fatalf("platform mismatch: got=%q want=%q", got, want)
	}
	if got, want := raw[4], byte(xnbVersion5); got != want {
		t.Fatalf("version mismatch: got=%d want=%d", got, want)
	}
	if got, want := raw[5], byte(xnbFlagsCompressed); got != want {
		t.Fatalf("flags mismatch: got=%d want=%d", got, want)
	}
	if got, want := binary.LittleEndian.Uint32(raw[6:10]), uint32(len(raw)); got != want {
		t.Fatalf("file size mismatch: got=%d want=%d", got, want)
	}
	decompressedSize := int(binary.LittleEndian.Uint32(raw[10:14]))
	if decompressedSize <= 0 {
		t.Fatalf("invalid decompressed payload size: %d", decompressedSize)
	}

	decompressedPayload := make([]byte, decompressedSize)
	n, err := lz4.UncompressBlock(raw[14:], decompressedPayload)
	if err != nil {
		t.Fatalf("lz4.UncompressBlock: %v", err)
	}
	if got, want := n, decompressedSize; got != want {
		t.Fatalf("decompressed payload size mismatch: got=%d want=%d", got, want)
	}

	parser := newTestParser(t, decompressedPayload)
	typeReaderCount := parser.read7BitEncodedInt()
	if got, want := typeReaderCount, 8; got != want {
		t.Fatalf("type reader count mismatch: got=%d want=%d", got, want)
	}
	for i := 0; i < typeReaderCount; i++ {
		_ = parser.readString()
		if got := parser.readInt32(); got != 0 {
			t.Fatalf("type reader %d version mismatch: got=%d want=0", i, got)
		}
	}

	if got := parser.read7BitEncodedInt(); got != 0 {
		t.Fatalf("shared resource count mismatch: got=%d want=0", got)
	}
	if got := parser.read7BitEncodedInt(); got != 1 {
		t.Fatalf("root object type reader mismatch: got=%d want=1", got)
	}
	if got := parser.read7BitEncodedInt(); got != 2 {
		t.Fatalf("texture object type reader mismatch: got=%d want=2", got)
	}
	if got, want := int32(parser.readInt32()), surfaceFormatDXT3; got != want {
		t.Fatalf("surface format mismatch: got=%d want=%d", got, want)
	}

	atlasWidth := parser.readInt32()
	atlasHeight := parser.readInt32()
	if atlasWidth < 2 || atlasHeight < 2 {
		t.Fatalf("atlas dimensions too small: %dx%d", atlasWidth, atlasHeight)
	}
	if !isPowerOfTwo(atlasWidth) || !isPowerOfTwo(atlasHeight) {
		t.Fatalf("atlas dimensions must be power-of-two: %dx%d", atlasWidth, atlasHeight)
	}
	if got := parser.readInt32(); got != 1 {
		t.Fatalf("mip level count mismatch: got=%d want=1", got)
	}

	levelDataSize := parser.readInt32()
	if got, want := levelDataSize, atlasWidth*atlasHeight; got != want {
		t.Fatalf("texture byte size mismatch: got=%d want=%d", got, want)
	}
	parser.skip(levelDataSize)

	if got := parser.read7BitEncodedInt(); got != 3 {
		t.Fatalf("glyph rect list reader mismatch: got=%d want=3", got)
	}
	glyphRects := parser.readRects()
	if got, want := len(glyphRects), 4; got != want {
		t.Fatalf("glyph rect count mismatch: got=%d want=%d", got, want)
	}
	if got, want := glyphRects[0].Width, int32(1); got != want {
		t.Fatalf("space glyph rect width mismatch: got=%d want=%d", got, want)
	}
	if got, want := glyphRects[1].Height, int32(2); got != want {
		t.Fatalf("question glyph rect height mismatch: got=%d want=%d", got, want)
	}
	if got, want := glyphRects[2].Width, int32(2); got != want {
		t.Fatalf("A glyph rect width mismatch: got=%d want=%d", got, want)
	}
	if got, want := glyphRects[3].Width, int32(0); got != want {
		t.Fatalf("zero-width glyph rect width mismatch: got=%d want=%d", got, want)
	}

	if got := parser.read7BitEncodedInt(); got != 3 {
		t.Fatalf("crop rect list reader mismatch: got=%d want=3", got)
	}
	cropRects := parser.readRects()
	if got, want := cropRects[0], (rect{X: 2, Y: 4, Width: 3, Height: 4}); got != want {
		t.Fatalf("space crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := cropRects[2], (rect{X: 0, Y: 1, Width: 2, Height: 4}); got != want {
		t.Fatalf("A crop rect mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := cropRects[3].Width, int32(0); got != want {
		t.Fatalf("zero-width crop rect width mismatch: got=%d want=%d", got, want)
	}

	if got := parser.read7BitEncodedInt(); got != 5 {
		t.Fatalf("char list reader mismatch: got=%d want=5", got)
	}
	chars := parser.readChars()
	if got, want := string(chars), " ?AB"; got != want {
		t.Fatalf("char list mismatch: got=%q want=%q", got, want)
	}

	if got, want := parser.readInt32(), int(spriteFontLineSpacing(font)); got != want {
		t.Fatalf("line spacing mismatch: got=%d want=%d", got, want)
	}
	if got := parser.readFloat32(); got != 0 {
		t.Fatalf("spacing mismatch: got=%f want=0", got)
	}

	if got := parser.read7BitEncodedInt(); got != 7 {
		t.Fatalf("kerning list reader mismatch: got=%d want=7", got)
	}
	kernings := parser.readVec3s()
	if got, want := len(kernings), 4; got != want {
		t.Fatalf("kerning count mismatch: got=%d want=%d", got, want)
	}
	if got, want := kernings[0], (vec3{X: 0, Y: 1, Z: 2}); got != want {
		t.Fatalf("space kerning mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := kernings[2], (vec3{X: 1, Y: 2, Z: 1}); got != want {
		t.Fatalf("A kerning mismatch: got=%+v want=%+v", got, want)
	}
	if got, want := kernings[3], (vec3{}); got != want {
		t.Fatalf("zero-width kerning mismatch: got=%+v want=%+v", got, want)
	}

	hasDefaultChar, defaultChar := parser.readOptionalChar()
	if !hasDefaultChar {
		t.Fatalf("expected default character to be present")
	}
	if got, want := defaultChar, rune('?'); got != want {
		t.Fatalf("default character mismatch: got=%q want=%q", got, want)
	}

	if parser.remaining() != 0 {
		t.Fatalf("unexpected trailing payload bytes: %d", parser.remaining())
	}
}

func TestMarshalBinaryOmitsDefaultCharWhenQuestionIsMissing(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   2,
		FontHeight:     3,
		SymbolsCount:   1,
		SymbolDataSize: 3,
		Symbols: []fnt.Symbol{
			{Width: 1, Data: []byte{0b1000_0000, 0x00}},
		},
	}
	font.UnicodeTable['A'] = 1

	raw, err := MarshalBinary(font)
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	decompressedPayload := decompressXNBPayload(t, raw)
	parser := newTestParser(t, decompressedPayload)
	typeReaderCount := parser.read7BitEncodedInt()
	for i := 0; i < typeReaderCount; i++ {
		_ = parser.readString()
		_ = parser.readInt32()
	}
	_ = parser.read7BitEncodedInt()
	_ = parser.read7BitEncodedInt()
	_ = parser.read7BitEncodedInt()
	_ = parser.readInt32()
	atlasWidth := parser.readInt32()
	atlasHeight := parser.readInt32()
	_ = parser.readInt32()
	parser.skip(parser.readInt32())
	_ = atlasWidth
	_ = atlasHeight
	_ = parser.read7BitEncodedInt()
	_ = parser.readRects()
	_ = parser.read7BitEncodedInt()
	_ = parser.readRects()
	_ = parser.read7BitEncodedInt()
	_ = parser.readChars()
	_ = parser.readInt32()
	_ = parser.readFloat32()
	_ = parser.read7BitEncodedInt()
	_ = parser.readVec3s()

	hasDefaultChar, _ := parser.readOptionalChar()
	if hasDefaultChar {
		t.Fatalf("expected default character to be absent")
	}
}

func TestMarshalBinaryRejectsSurrogateCodepoints(t *testing.T) {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   1,
		FontHeight:     1,
		SymbolsCount:   1,
		SymbolDataSize: 2,
		Symbols: []fnt.Symbol{
			{Width: 1, Data: []byte{0b1000_0000}},
		},
	}
	font.UnicodeTable[0xD800] = 1

	_, err := MarshalBinary(font)
	if err == nil {
		t.Fatalf("expected surrogate validation error")
	}
}

func TestCompressDXT3EncodesExplicitAlphaAndWhiteColor(t *testing.T) {
	img := image.NewAlpha(image.Rect(0, 0, 4, 4))
	img.Pix[0] = 0xFF
	img.Pix[1] = 0x80
	img.Pix[4] = 0xFF

	raw, err := compressDXT3(img)
	if err != nil {
		t.Fatalf("compressDXT3: %v", err)
	}
	if got, want := len(raw), 16; got != want {
		t.Fatalf("compressed size mismatch: got=%d want=%d", got, want)
	}

	expected := []byte{
		0x7F, 0x00,
		0x0F, 0x00,
		0x00, 0x00,
		0x00, 0x00,
		0xFF, 0xFF,
		0x00, 0x00,
		0x50, 0x54, 0x55, 0x55,
	}
	if got, want := raw, expected; !bytes.Equal(got, want) {
		t.Fatalf("compressed block mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestCompressDXT3RejectsNonBlockAlignedImage(t *testing.T) {
	img := image.NewAlpha(image.Rect(0, 0, 5, 4))

	_, err := compressDXT3(img)
	if err == nil {
		t.Fatalf("expected block alignment error")
	}
}

func TestLayoutGlyphsDoesNotMutateInput(t *testing.T) {
	font := sampleReferenceLikeFont()
	glyphs, _, _, err := buildGlyphs(font)
	if err != nil {
		t.Fatalf("buildGlyphs: %v", err)
	}

	original := append([]glyph(nil), glyphs...)
	layout, err := layoutGlyphs(glyphs, int(font.SymbolHeight))
	if err != nil {
		t.Fatalf("layoutGlyphs: %v", err)
	}

	if len(layout.glyphs) != len(glyphs) {
		t.Fatalf("layout glyph count mismatch: got=%d want=%d", len(layout.glyphs), len(glyphs))
	}
	for i := range glyphs {
		if got, want := glyphs[i].glyphRect, original[i].glyphRect; got != want {
			t.Fatalf("input glyph mutated at %d: got=%+v want=%+v", i, got, want)
		}
	}
}

func sampleReferenceLikeFont() *fnt.Font {
	font := &fnt.Font{
		IdeographWidth: 8,
		SymbolStride:   1,
		SymbolHeight:   4,
		FontHeight:     5,
		SymbolsCount:   4,
		SymbolDataSize: 5,
		Symbols: []fnt.Symbol{
			{Width: 3, Data: []byte{0x00, 0x00, 0x00, 0x00}},
			{Width: 2, Data: []byte{0b1100_0000, 0b0100_0000, 0x00, 0x00}},
			{Width: 4, Data: []byte{0x00, 0b0110_0000, 0b0010_0000, 0x00}},
			{Width: 0, Data: []byte{0x00, 0x00, 0x00, 0x00}},
		},
	}
	font.UnicodeTable[' '] = 1
	font.UnicodeTable['?'] = 2
	font.UnicodeTable['A'] = 3
	font.UnicodeTable['B'] = 4
	return font
}

func decompressXNBPayload(t *testing.T, raw []byte) []byte {
	t.Helper()
	if len(raw) < xnbCompressedHeaderSize {
		t.Fatalf("compressed XNB too short: %d", len(raw))
	}
	decompressedSize := int(binary.LittleEndian.Uint32(raw[10:14]))
	if decompressedSize <= 0 {
		t.Fatalf("invalid decompressed payload size: %d", decompressedSize)
	}
	decompressedPayload := make([]byte, decompressedSize)
	n, err := lz4.UncompressBlock(raw[14:], decompressedPayload)
	if err != nil {
		t.Fatalf("lz4.UncompressBlock: %v", err)
	}
	if got, want := n, decompressedSize; got != want {
		t.Fatalf("decompressed payload size mismatch: got=%d want=%d", got, want)
	}
	return decompressedPayload
}

type testParser struct {
	t   *testing.T
	raw []byte
	pos int
}

func newTestParser(t *testing.T, raw []byte) *testParser {
	t.Helper()
	return &testParser{t: t, raw: raw}
}

func (p *testParser) remaining() int {
	return len(p.raw) - p.pos
}

func (p *testParser) readByte() byte {
	p.t.Helper()
	if p.pos >= len(p.raw) {
		p.t.Fatalf("unexpected end of payload")
	}
	value := p.raw[p.pos]
	p.pos++
	return value
}

func (p *testParser) read7BitEncodedInt() int {
	value := 0
	shift := 0
	for {
		b := p.readByte()
		value |= int(b&0x7F) << shift
		if b&0x80 == 0 {
			return value
		}
		shift += 7
	}
}

func (p *testParser) readString() string {
	size := p.read7BitEncodedInt()
	p.t.Helper()
	if p.pos+size > len(p.raw) {
		p.t.Fatalf("string exceeds payload size: pos=%d size=%d len=%d", p.pos, size, len(p.raw))
	}
	value := string(p.raw[p.pos : p.pos+size])
	p.pos += size
	return value
}

func (p *testParser) readInt32() int {
	p.t.Helper()
	if p.pos+4 > len(p.raw) {
		p.t.Fatalf("int32 exceeds payload size: pos=%d len=%d", p.pos, len(p.raw))
	}
	value := int(binary.LittleEndian.Uint32(p.raw[p.pos : p.pos+4]))
	p.pos += 4
	return value
}

func (p *testParser) readFloat32() float32 {
	p.t.Helper()
	if p.pos+4 > len(p.raw) {
		p.t.Fatalf("float32 exceeds payload size: pos=%d len=%d", p.pos, len(p.raw))
	}
	value := math.Float32frombits(binary.LittleEndian.Uint32(p.raw[p.pos : p.pos+4]))
	p.pos += 4
	return value
}

func (p *testParser) readChar() rune {
	p.t.Helper()
	if p.pos >= len(p.raw) {
		p.t.Fatalf("char exceeds payload size")
	}
	value, size := utf8.DecodeRune(p.raw[p.pos:])
	if value == utf8.RuneError && size == 1 {
		p.t.Fatalf("invalid utf-8 char at pos=%d", p.pos)
	}
	p.pos += size
	return value
}

func (p *testParser) readOptionalChar() (bool, rune) {
	if p.readByte() == 0 {
		return false, 0
	}
	return true, p.readChar()
}

func (p *testParser) skip(size int) {
	p.t.Helper()
	if size < 0 || p.pos+size > len(p.raw) {
		p.t.Fatalf("skip exceeds payload size: pos=%d size=%d len=%d", p.pos, size, len(p.raw))
	}
	p.pos += size
}

func (p *testParser) readRects() []rect {
	count := p.readInt32()
	values := make([]rect, count)
	for i := 0; i < count; i++ {
		values[i] = rect{
			X:      int32(p.readInt32()),
			Y:      int32(p.readInt32()),
			Width:  int32(p.readInt32()),
			Height: int32(p.readInt32()),
		}
	}
	return values
}

func (p *testParser) readChars() []rune {
	count := p.readInt32()
	values := make([]rune, count)
	for i := 0; i < count; i++ {
		values[i] = p.readChar()
	}
	return values
}

func (p *testParser) readVec3s() []vec3 {
	count := p.readInt32()
	values := make([]vec3, count)
	for i := 0; i < count; i++ {
		values[i] = vec3{
			X: p.readFloat32(),
			Y: p.readFloat32(),
			Z: p.readFloat32(),
		}
	}
	return values
}

func isPowerOfTwo(value int) bool {
	return value > 0 && value&(value-1) == 0
}
