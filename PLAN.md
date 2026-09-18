# go-264 development plan

The H.264 decoder, audio frontend, measured optimisation campaign and progressive 8-bit YUV420 short-term conformance plan are implemented on `master`. [`docs/short-term-plan.md`](docs/short-term-plan.md) records the completed fixture and validation evidence. Native ARM64 benchmarking and broader decoder or encoder work remain deferred.

## Engineering rules

* Base syntax, prediction and filtering changes on H.264, FFmpeg or x264 behaviour.
* Keep scalar implementations as the reference for SIMD and GPU code.
* Require exact sample equality for decoder acceptance. Use PSNR only to locate a difference.
* Preserve coded dimensions during reconstruction. Apply cropping at visible-output boundaries.
* Measure a hot path before adding low-level code.
* Store fixtures, generated FFmpeg sources, raw video and traces under `/workspace/tmp`.
* License the entire project under the root [MIT License](LICENSE). Retain upstream MIT notices for imported material and separate licences for referenced external datasets.

## Accepted decoder baseline

The hard regression uses this Annex B stream:

```text
Path:       /workspace/tmp/bbb_annexb.h264
SHA-256:    1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
Format:     640x360, yuv420p, High profile, CABAC, three B-frames
Frames:     300
Reference:  FFmpeg 7.1.3
```

The accepted historical run matched every visible Y, U and V sample in display order with in-loop deblocking enabled. A separate run with deblocking disabled also matched. The CABAC trace check compared 2,100 macroblock events from each decoder without a differing field.

`TestFFmpegReferenceParityBBB` verifies the fixture hash, FFmpeg version, frame count, display order and pixel data. A mismatch reports the first frame, plane, macroblock and pixel. The retained source and current toolchain cannot reproduce the pinned fixture, so current changes use the checked-in four-frame exact regression and the separate diagnostic 300-frame stream until the historical bytes are restored.

## Implemented and tested

* Annex B scanning, emulation-prevention handling and bounded SPS/PPS parsing.
* Multi-slice I, P and B pictures; POC types 0/1/2; short- and long-term reference marking; P- and B-list operand parsing; optional presentation-order output.
* CAVLC and CABAC macroblock and residual decoding, including 8x8 transforms and I_PCM reset handling.
* I4x4, I8x8, I16x16 and chroma intra prediction.
* P and B inter partitions, quarter-sample luma, chroma interpolation, spatial and temporal Direct mode, and weighted prediction used by the regression stream.
* Scalar 4x4 and 8x8 transforms, exact amd64 SSE2 and selected ARM64 NEON fast paths, fused 4x4 reconstruction, residual addition and in-loop luma/chroma deblocking.
* Bounds checks for readers, frame storage, coefficient buffers and reconstruction helpers.
* Unit, fuzz, syntax, motion, reconstruction, scalar/SIMD parity and architecture build checks.

The tested scope is progressive 8-bit YUV420 Annex B video. The regression stream does not cover every item above in every legal combination.

## Decoder work

### Conformance streams

The completed short-term set covers multiple IDR GOPs, `frame_num`/POC wrap, legal coded-edge cropping, explicit weighted B prediction, long-term references and B-slice list modification. The three external official vectors are downloaded from FFmpeg FATE, SHA-256 verified, and compared in display order against FFmpeg 7.1.3 with filtering enabled and disabled. `MR1_BT_A.h264` exercises MMCO 3/4 long-term promotion and limits.

Future fixture additions should target `log2_max_frame_num` values producing `MaxPicNum` values other than 16 and any broader syntax selected for implementation. Record source, encoding parameters and SHA-256; compare display-order Y, U and V samples with a pinned reference decoder; add a focused primitive test only when a stream exposes a defect.

### Complete unsupported syntax

FMO syntax is parsed but FMO reconstruction is unsupported. Implement it only with fixtures for the required slice-group map types.

Explicit weighted B prediction needs fixtures that cover luma and chroma weights, offsets and both reference lists. The existing regression covers only the weighting behaviour present in the pinned stream.

Interlaced field pictures and MBAFF require separate picture-order, reference-list, motion and deblocking tests. Do not infer support from progressive streams.

### Maintain trace comparisons

CABAC, Direct-mode, BIDI and reconstruction traces must remain opt-in and deterministic. A new trace needs a comparator or another named consumer. Production code must not contain hard-coded POC or macroblock probes.

Generated FFmpeg changes and trace files belong under `/workspace/tmp`. Repository scripts may patch the local FFmpeg 7.1.3 tree but must not modify a system FFmpeg installation.

## SIMD and allocation state

The completed campaign retained only exact-output changes with measured CPU or allocation gains. [Video SIMD coverage](docs/video-simd.md), [audio SIMD coverage](audio/SIMD.md) and the [profiling protocol](docs/profiling.md) contain the implementation details and evidence limits.

Current video paths include:

* amd64 SSE2 and ARM64 NEON B-frame equal and weighted blending;
* amd64 SSE2 vertical deblocking and bounded horizontal staging;
* amd64 SSE2 and ARM64 NEON 4×4/8×8 residual add, clip and store;
* amd64 SSE2 motion compensation, transforms, inverse scaling and SAD;
* selected ARM64 NEON transforms, SAD and motion kernels; and
* scalar and `purego` fallbacks with exact trace and pixel checks.

All 52 quantisation parameters, boundary strengths 0–4, both filter orientations, luma/chroma planes, aliases and protected edges are covered for the accepted deblocking paths. ARM64 and amd64 both have exact luma/chroma deblocking pixel kernels; boundary-strength classification remains scalar. Native ARM64 timing is deferred by the short-term plan.

Decoder-owned CABAC macroblock storage and lazy trace tags reduced the comparable diagnostic 300-frame workload from 362,042 to 20,059 allocations and from 867.9 MB to 492.2 MB. Unprofiled decode time fell from 1.209 s to 1.165 s in that same measurement window. These results use the diagnostic `b115b066…bc94a` stream and do not replace the unavailable historical fixture.

Current audio paths include exact amd64 SSE2/AVX2 and selected ARM64 NEON kernels for AAC FFT/filterbank work, PCM conversion and layout, WAV unpacking, ordered resampler products and AC-3 overlap-add. The AC-3 frontend supports `bsid` 0–8, mono-to-5.1 input, deterministic mono/stereo downmix and explicit one/two-channel extraction. E-AC-3 fails closed. Native ARM64 CI runs default, `purego`, vet and race gates for `audio/...`.

Keep CABAC arithmetic, Huffman bit traversal, PNS random generation and ordered energy accumulation, TNS recurrence, checked container parsing, cancellation and filesystem operations scalar unless a profile and an exact formulation justify a change. Do not use FMA or reassociate ordered floating-point sums.

Each future optimisation requires:

1. exact scalar, SIMD and `purego` output;
2. focused and full tests, vet and architecture builds;
3. retained trace, YUV or PCM oracle parity;
4. before-and-after measurements on the same host, toolchain, fixture and CPU affinity; and
5. explicit ownership, alias, cancellation and rollback rules for reused memory.

The next useful video targets are boundary-strength calculation and sequential CABAC consumers identified by the final profiles; the Phase 4 reference-ordering defects were corrected by PR #20. New optimisation work starts only when a measured share and exact implementation justify the complexity. Native ARM64 timing and wider H.264 conformance need separate evidence.

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

Report fixture availability before running external gates. Strict mode fails when the pinned stream or reference decoder is absent or wrong; it does not download media:

```bash
./scripts/fixture_gate_status.sh
./scripts/fixture_gate_status.sh --strict
```

Run the pinned CABAC and pixel gates after decoder changes:

```bash
./scripts/bootstrap_fixtures.sh
./scripts/cabac_firstdiv.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-firstdiv

./scripts/fixture_gate_status.sh --strict
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
