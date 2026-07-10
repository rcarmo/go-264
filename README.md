# go-264

`go-264` is an H.264/AVC decoder-first implementation in pure Go, with scalar reference code, amd64/arm64 assembly hooks and a small set of deliberately optional GPU experiments.

The decoder now reproduces every visible Y, U and V sample from the pinned 300-frame Big Buck Bunny fixture when compared with FFmpeg 7.1.3. That includes CABAC, B-frames, display reordering and in-loop deblocking -- which is a useful hard gate, but not a claim of complete H.264 conformance.

## What works

| Area | Status | Notes |
|---|---:|---|
| Annex B and NAL parsing | Yes | Start codes, emulation-prevention removal, SPS/PPS parsing and defensive bounds handling |
| Slice syntax | Yes | I/P/B headers, POC, reference marking and modification, weighted prediction fields, deblocking controls and I_PCM |
| CAVLC | Yes | Baseline decode plus High-profile inter 8x8 residual scan handling |
| CABAC | Yes | I/P/B macroblocks, residuals, reference/MV contexts, 8x8 transforms and I_PCM reset handling |
| Prediction | Yes | I4x4, I8x8, I16x16, chroma modes, P/B inter partitions, Direct mode and weighted prediction used by the reference stream |
| Transforms | Yes | Scalar 4x4 and 8x8 integer transforms with assembly dispatch seams |
| DPB and output order | Yes | Reference tracking, POC handling and display-order output across IDR GOPs |
| Deblocking | Yes | Scalar in-loop luma/chroma filtering with FFmpeg-exact output on the pinned gate |
| Encoder | No | Planned after broader decoder conformance and measured SIMD work |

The implementation currently targets 8-bit YUV420 Annex B streams. FMO reconstruction, the less common weighted B-prediction combinations, interlaced/MBAFF material and broad conformance-suite coverage still need work.

## Build and decode

```bash
go build -o /workspace/tmp/decode264 ./cmd/decode264

/workspace/tmp/decode264 -i input.h264 -o frames -f color
/workspace/tmp/decode264 -i input.h264 -o frames -f png
/workspace/tmp/decode264 -i input.h264 -o frames -f yuv
```

The three output modes write colour PNGs, luma-only PNGs or one planar YUV420 file per display-order frame. `-frames N` limits decode work; zero means the complete stream.

## Packages

```text
nal/              Annex B, NAL units, SPS/PPS and bit reader
frame/            YUV420 storage, DPB helpers and guarded pixel access
entropy/cabac/    CABAC arithmetic, context initialisation and residual decode
entropy/cavlc/    CAVLC residual decode and generated VLC tables
syntax/           Slice and macroblock syntax
pred/             Intra/inter prediction and SIMD dispatch hooks
transform/        4x4/8x8 transforms, quantisation and scalar fallbacks
filter/           In-loop deblocking
me/               SAD/SATD motion-estimation kernels
gpu/              Optional experiment scaffolding
decode/           Decoder pipeline, reconstruction and conformance tests
internal/tables/  Reproducible generators for checked-in entropy tables
cmd/decode264      Annex B decoder
cmd/trace264       Syntax and CABAC event tracer
cmd/trace264cmp    Frame/trace comparison helper
cmd/trace264diff   Trace diff helper
```

## The hard oracle

`scripts/bootstrap_fixtures.sh` verifies existing fixtures under `/workspace/tmp` and can regenerate missing ones with the installed FFmpeg/libx264 toolchain. Its hash guard rejects a different encode, so exact regeneration requires a compatible toolchain; keeping the verified fixture is preferable to assuming every system FFmpeg build will emit the same bytes. The canonical BBB stream is:

```text
/workspace/tmp/bbb_annexb.h264
SHA-256 1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
640x360, High profile, CABAC, three B-frames, 300 frames
```

The opt-in oracle test checks the fixture hash and FFmpeg version before decoding. It then compares all 300 frames in display order, row by row, across Y, U and V; the first mismatch reports the frame, plane, macroblock and pixel.

```bash
./scripts/bootstrap_fixtures.sh

# Builds the pinned FFmpeg tree when necessary and checks CABAC event parity.
./scripts/cabac_firstdiv.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-firstdiv

GO264_FFMPEG_REGRESSION=1 \
GO264_FFMPEG_BIN=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
GO264_BBB_FIXTURE=/workspace/tmp/bbb_annexb.h264 \
go test ./cmd/decode264 -run TestFFmpegReferenceParityBBB -count=1 -v
```

The accepted result is exact rather than PSNR-close:

```text
300 display-order frames
Y/U/V maxdiff=0
CABAC trace: 2100 Go events, 2100 FFmpeg events, no divergence
```

For ad-hoc output produced by `decode264`, `scripts/compare_yuv_frames.py` compares a directory of per-frame YUV files with a contiguous FFmpeg rawvideo stream:

```bash
scripts/compare_yuv_frames.py \
  --go-dir /workspace/tmp/bbb-go \
  --reference /workspace/tmp/bbb-ffmpeg.yuv \
  --width 640 --height 360 --frames 300
```

## Validation

Some development containers mount `/tmp` with `noexec`, so use a workspace-backed Go temp directory:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp/go-264
mkdir -p "$GOTMPDIR"

go test ./...
go vet ./...
GOOS=linux GOARCH=arm64 go build ./...
git diff --check
```

The shorter CABAC smoke gate remains useful because it points at syntax drift before a pixel mismatch has had time to propagate:

```bash
FFMPEG=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
./scripts/cabac_parity_baseline.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-parity-baseline
```

## Tracing without making normal decode noisy

`trace264 -cabac` uses the decoder's own macroblock event stream. The focused scripts under `scripts/` can instrument a local FFmpeg 7.1.3 tree for CABAC, Direct-mode, motion-cache and reconstruction comparisons; pass output directories under `/workspace/tmp` to keep the sizeable trace artefacts out of the repository.

The most generally useful opt-in traces are:

* `GO264_CABAC_ARITH_TRACE=1` for arithmetic state.
* `GO264_CABAC_CBP_TRACE=1` for coded-block-pattern decisions.
* `GO264_CABAC_RESIDUAL_TRACE=1` for residual significance and levels.
* `GO264_CABAC_SYNTAX_TRACE=1` for intra syntax bins.
* `GO264_RECON_TRACE=1` for prediction, coefficient, residual and output checksums.
* `GO264_DIRECT_TRACE=1` for spatial/temporal Direct derivation.

These are diagnostics rather than part of the public API, and their text format may change.

## Performance work

The decoder keeps scalar behaviour as the reference and only retains low-level changes that survive parity tests. Existing fast paths cover no-EPB bit reading, CAVLC prefix lookup, interior and axis-aligned motion compensation, integer-MV sub-rectangle copies, chroma row copies, zero-residual bypasses and direct frame-row write-back.

A typical BBB baseline sample on the development host is 44-52ms per decode with roughly 10.9MB and 1,300 allocations per operation. Those numbers are directional, not portable benchmarks -- run the local benchmark before deciding that another assembly path is worth maintaining.

The next sensible targets are broader conformance streams first, then measured IDCT/dequant and deblocking SIMD. `PLAN.md` keeps that work separate from the historical debugging trail, which belongs in Git and `/workspace/tmp`, not in the reader-facing documentation.

## Table generation

The large entropy tables are checked in but reproducible:

```bash
go generate ./entropy/cabac ./entropy/cavlc
```

Generator commands live under `internal/tables/` and use `//go:build ignore`, so ordinary package builds do not compile them.

## License

MIT
