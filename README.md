# ra2fnt

This program was created entirely using `GPT-5.3-Codex` without writing code by hand.

CLI utility for converting [Westwood Unicode BitFont](https://moddingwiki.shikadi.net/wiki/Westwood_Unicode_BitFont_Format) (`game.fnt`, `fonT`), a font format used in Command & Conquer: Red Alert 2, to a PNG set and back.

## Build

```bash
go build ./src/cmd/ra2fnt
```

Local multi-platform release build scripts:

```bash
./scripts/build-release.sh
./scripts/build-release.sh v1.0.0
```

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1 -Version v1.0.0
```

```bat
scripts\build-release.bat
scripts\build-release.bat v1.0.0
```

## Usage

Export `.fnt` or `.xnb` to PNG set:

```bash
./ra2fnt export -in game.fnt -out out_font
```

```bash
./ra2fnt export -in SpriteFont.xnb -out out_font
```

Export with integer pixel scaling (for easier editing):

```bash
./ra2fnt export -in game.fnt -out out_font --scale 3
```

Non-interactive overwrite for scripts/CI:

```bash
./ra2fnt export -in game.fnt -out out_font --force
```

Create `.fnt` from PNG set:

```bash
./ra2fnt create -in out_font -out rebuilt.fnt
```

Create an experimental LZ4-compressed `SpriteFont XNB v5` font file for [`xna-cncnet-client`](https://github.com/CnCNet/xna-cncnet-client) from PNG set:

```bash
./ra2fnt create -in out_font -out SpriteFont.xnb --format cncnet-spritefont
```

Create without glyph deduplication:

```bash
./ra2fnt create -in out_font -out rebuilt.fnt --no-dedup
```

Validate PNG set and metadata without writing `.fnt`:

```bash
./ra2fnt validate -in out_font
```

Show CLI version:

```bash
./ra2fnt version
./ra2fnt --version
```

`export` and `create` show a progress bar in `stderr`.
If `out` directory already exists, `export` asks for confirmation before deleting it.
- Use `--force` to skip confirmation and overwrite `out`.

## Export format

`export` reads `.fnt` and `cncnet-spritefont` `.xnb` files, and writes only PNG files grouped by Unicode ranges:

```text
out_font/
  metadata.json
  0x0020-0x007F (Basic Latin)/
    0x0041.png
    0x0042.png
  0x0400-0x04FF (Cyrillic)/
    0x0451.png
```

In this tree, only non-zero-width glyphs are shown as PNG files.
Zero-width glyphs are listed only in `metadata.json` (`symbol_width`).

- One PNG is exported per mapped Unicode codepoint from the `.fnt` unicode table.
- PNG filename is fixed-length hex codepoint: `0xXXXX.png`.
- `metadata.json` stores:
  - `symbol_width` (only zero-width glyphs, as `0`)
  - `symbol_stride`
  - `font_height`
  - `ideograph_width`
  - `scale` (integer export scale, default `1`)
- Zero-width glyphs are not exported as PNG files and are restored via `metadata.json` (`symbol_width=0`).
- PNG height is `symbol_height`.
- No `unicode_table.bin` or `tail.bin` is produced.

## Create behavior

`create` reconstructs a font file from PNG files in the input directory:

- PNG files are discovered recursively (subdirectory names are ignored by parser).
- Files must be named as fixed-length hex codepoints (for example `0x0041.png`, `0x30A1.png`).
- Zero-width glyphs are restored from `metadata.json` (`symbol_width=0`).
- Codepoints with `symbol_width=0` must not have PNG files.
- All PNG files must have the same height.
- `symbol_width` contains only zero-width entries and is used to restore width `0`; otherwise width is taken from PNG width.
- `symbol_stride` is taken from `metadata.json` when present; otherwise it is auto-calculated as `ceil(max_symbol_width / 8)`.
- `font_height` is taken from `metadata.json`.
- `ideograph_width` is taken from `metadata.json`.
- `scale` is taken from `metadata.json`; when `scale > 1`, PNG dimensions are downscaled by this factor during `create` (back to normal font size).
- Identical glyphs are deduplicated, so multiple codepoints can reference the same symbol index.
- Output format is selected by `--format`:
  - `fnt` (default): writes Westwood Unicode BitFont `.fnt`
  - `cncnet-spritefont`: writes an experimental LZ4-compressed `SpriteFont XNB v5` `.xnb` font file for [`xna-cncnet-client`](https://github.com/CnCNet/xna-cncnet-client)
- Use `--no-dedup` to disable deduplication.
- Unicode table is rebuilt from filenames (`0xXXXX` -> symbol index in sorted codepoint order).

Because unicode mapping order/tail bytes are rebuilt, the resulting `.fnt` is not expected to be byte-identical to the original input file.

## Validate behavior

`validate` runs the same checks as `create` without writing output `.fnt`, and prints a summary:

- total codepoints
- number of PNG files
- number of zero-width codepoints from metadata
- resulting symbol count
- number of deduplicated symbols

## Limitations

- Output `.fnt` is not byte-identical to source `game.fnt`.
- At least one non-zero-width PNG is required to infer `symbol_height`.
- Zero-width glyphs are represented only in `metadata.json` and have no PNG files.
- Unicode table order is rebuilt from sorted codepoints.
- `cncnet-spritefont` always writes LZ4-compressed `SpriteFont XNB v5`.
- `cncnet-spritefont` is an experimental feature.
- When `?` is present, `cncnet-spritefont` writes it as `defaultChar` fallback.
- `export` preserves PNG glyph images exactly when round-tripping `out_font -> .xnb -> out_font`.

## License

- Project license: `MIT` (see `LICENSE`)
- Third-party notices: `THIRD_PARTY_NOTICES.md`
