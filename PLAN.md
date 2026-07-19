# go-264 development plan

The decoder matches FFmpeg 7.1.3 sample for sample on the pinned 300-frame regression stream. Development now covers more H.264 syntax combinations, then measured SIMD work, then an encoder.

## Engineering rules

* Base syntax, prediction and filtering changes on H.264, FFmpeg or x264 behaviour.
* Keep scalar implementations as the reference for SIMD and GPU code.
* Require exact sample equality for decoder acceptance. Use PSNR only to locate a difference.
* Preserve coded dimensions during reconstruction. Apply cropping at visible-output boundaries.
* Measure a hot path before adding low-level code.
* Store fixtures, generated FFmpeg sources, raw video and traces under `/workspace/tmp`.

## Accepted decoder baseline

The hard regression uses this Annex B stream:

```text
Path:       /workspace/tmp/bbb_annexb.h264
SHA-256:    1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
Format:     640x360, yuv420p, High profile, CABAC, three B-frames
Frames:     300
Reference:  FFmpeg 7.1.3
```

The decoder matches every visible Y, U and V sample in display order with in-loop deblocking enabled. A separate run with deblocking disabled also matches. The CABAC trace check compares 2,100 macroblock events from each decoder without a differing compared field.

`TestFFmpegReferenceParityBBB` verifies the fixture hash, FFmpeg version, frame count, display order and pixel data. A mismatch reports the first frame, plane, macroblock and pixel.

## Implemented and tested

* Annex B scanning, emulation-prevention handling and bounded SPS/PPS parsing.
* I, P and B slice headers; POC and DPB bookkeeping; reference marking; P-slice list modification; B-list operand parsing; display reordering.
* CAVLC and CABAC macroblock and residual decoding, including 8x8 transforms and I_PCM reset handling.
* I4x4, I8x8, I16x16 and chroma intra prediction.
* P and B inter partitions, quarter-sample luma, chroma interpolation, spatial and temporal Direct mode, and weighted prediction used by the regression stream.
* Scalar 4x4 and 8x8 transforms, residual addition and in-loop luma and chroma deblocking.
* Bounds checks for readers, frame storage, coefficient buffers and reconstruction helpers.
* Unit, fuzz, syntax, motion, reconstruction, scalar/SIMD parity and architecture build checks.

The tested scope is progressive 8-bit YUV420 Annex B video. The regression stream does not cover every item above in every legal combination.

## Decoder work

### Add conformance streams

Add small fixtures for these cases:

* Multiple IDR GOPs.
* `frame_num` and POC wrap.
* Explicit weighted B prediction.
* Long-term references.
* B-slice list modification.
* `log2_max_frame_num` values that produce `MaxPicNum` values other than 16.
* Field-coded and MBAFF video.
* Legal cropping at coded-frame edges.

Record the source, encoding parameters and SHA-256 for each fixture. Tests must compare display-order Y, U and V samples with a pinned reference decoder. Add a focused unit test for the primitive that caused each mismatch.

### Complete unsupported syntax

FMO syntax is parsed but FMO reconstruction is unsupported. Implement it only with fixtures for the required slice-group map types.

Explicit weighted B prediction needs fixtures that cover luma and chroma weights, offsets and both reference lists. The existing regression covers only the weighting behaviour present in the pinned stream.

Interlaced field pictures and MBAFF require separate picture-order, reference-list, motion and deblocking tests. Do not infer support from progressive streams.

### Maintain trace comparisons

CABAC, Direct-mode, BIDI and reconstruction traces must remain opt-in and deterministic. A new trace needs a comparator or another named consumer. Production code must not contain hard-coded POC or macroblock probes.

Generated FFmpeg changes and trace files belong under `/workspace/tmp`. Repository scripts may patch the local FFmpeg 7.1.3 tree but must not modify a system FFmpeg installation.

## SIMD and allocation work

Re-profile the exact-parity tree on amd64 and arm64 before selecting a kernel. Historical BBB runs measured 44-52ms after earlier allocation work, but benchmark names and fixture paths have changed. Record the complete command, fixture, host, Go version, time per operation, bytes per operation and allocations per operation for the new baseline.

Candidates include:

* Batched inverse transform and dequantisation.
* Fractional motion-compensation shapes that lack an interior fast path.
* Luma and chroma deblocking.
* Allocations outside frame buffers and per-slice state.

Each SIMD change requires:

1. Scalar and assembly outputs that are coefficient-exact or pixel-exact.
2. Architecture-specific tests and a safe scalar fallback.
3. Before-and-after benchmarks on the same host, Go version and fixture.
4. The complete 300-frame FFmpeg parity test.
5. A Linux arm64 build from the development host.

CABAC is sequential and is excluded from GPU work. GPU experiments may cover batched motion search or transforms after CPU profiles identify enough parallel work to offset transfer and setup costs.

## Encoder sequence

Encoder development starts after the conformance fixtures above are accepted and shared decoder primitives have stable tests.

1. Encode I and P slices with CAVLC, integer transforms and simple rate control.
2. Add CABAC, B-frames, 8x8 transforms and weighted prediction.
3. Implement scalar full-search motion estimation.
4. Add measured SAD and SATD SIMD kernels.
5. Add mode decision and rate-distortion optimisation.
6. Evaluate GPU motion-search or transform batches with CPU fallbacks.

Design the public encoder API with the first end-to-end implementation. It must accept YUV420 frames and return NAL units without depending on `cmd/decode264`.

## Required checks

Use a workspace-backed Go temporary directory where `/tmp` is mounted with `noexec`:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp/go-264
mkdir -p "$GOTMPDIR"

go test ./...
go vet ./...
GOOS=linux GOARCH=arm64 go build ./...
git diff --check
```

Run the pinned CABAC and pixel gates after decoder changes:

```bash
./scripts/bootstrap_fixtures.sh
./scripts/cabac_firstdiv.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-firstdiv

GO264_FFMPEG_REGRESSION=1 \
GO264_FFMPEG_BIN=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
GO264_BBB_FIXTURE=/workspace/tmp/bbb_annexb.h264 \
go test ./cmd/decode264 -run TestFFmpegReferenceParityBBB -count=1 -v
```

Run table generation when entropy tables or generators change:

```bash
go generate ./entropy/cabac ./entropy/cavlc
git diff --check
```

A decoder change is accepted when its focused test passes, the repository checks pass and the pinned FFmpeg comparison remains exact. An optimisation also needs benchmark evidence. A generated-table change needs a reproducible `go generate` diff.
