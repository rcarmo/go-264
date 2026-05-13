# go-264 plan

H.264/AVC decoder-first implementation in pure Go with scalar correctness first, then amd64/arm64 assembly acceleration and optional GPU experiments.

This file is a durable project overview. The active step-by-step checklist for the current session lives in the Pi plan sidebar, not here.

## Strategy

1. **Decoder first** — exercise bitstream parsing, entropy coding, prediction, transforms, frame storage, DPB state, and conformance tooling before building an encoder.
2. **Reference-grounded fixes** — syntax/MV/prediction changes should be grounded in H.264 text, FFmpeg, or x264 behavior rather than local heuristics.
3. **Scalar correctness before SIMD** — every assembly/GPU kernel must have a scalar reference and parity tests.
4. **Low allocation hot paths** — decode benchmarks and profiles are part of the validation loop.
5. **Intentional breaking refactors** — no compatibility wrappers during package/API cleanup.

## Current implementation layout

```text
nal/              Annex B NAL parsing, SPS/PPS, bit reader
frame/            YUV420 frame storage, DPB helpers, ChromaQP, SafePixelY
entropy/
  cabac/          CABAC arithmetic decoder, context init, residual syntax
  cavlc/          CAVLC block decoder, VLC tables
syntax/           H.264 syntax layer: slice headers, MBIntra/MBInter, MB-level helpers
pred/             Intra/inter prediction and SIMD dispatch hooks
transform/        4×4/8×8 integer transforms, dequant, scalar/SIMD fallbacks
filter/           In-loop deblocking filter
me/               Motion-estimation kernels (SAD/SATD)
gpu/              GPU experiment scaffolding
decode/           End-to-end decoder pipeline, conformance, benchmarks
internal/tables/  go:generate commands for checked-in CABAC/CAVLC tables
cmd/
  decode264       Decode Annex B to color PNG, luma PNG, or raw YUV
  trace264        CAVLC MB-level syntax tracer; rejects CABAC streams for now
  trace264cmp     FFmpeg/showinfo comparison, frame stats, histograms
  trace264diff    Trace diff helper
```

Historical note: the package formerly named `slice` is now `syntax`, and the old monolithic `entropy` package is split into `entropy/cavlc` and `entropy/cabac`.

## Decoder status

### Completed hard gates

- Baseline CAVLC decode is correct enough to be marked complete.
- Syntax parity tooling exists (`trace264`, `trace264cmp`).
- Motion-vector parity has been validated on representative P-slice macroblocks.
- Reconstruction parity has FFmpeg YUV/PSNR regression tests.
- Chroma dequant now applies `chroma_qp_index_offset` through `frame.ChromaQP`.
- I4x4 top-right reference availability was fixed per H.264 §8.3.1.1.
- Refactoring is complete:
  - `decode/decoder.go` split into focused files.
  - `slice` renamed to `syntax`.
  - `entropy` split into `entropy/cavlc` and `entropy/cabac`.
  - CAVLC/CABAC table generation commands live under `internal/tables/`.
  - Under-covered packages gained focused tests.

### CABAC state

Implemented:

- CABAC context initialization from FFmpeg/spec tables.
- P-slice `mb_type` decision tree.
- CABAC intra-in-P decode path wired through intra reconstruction.
- CABAC skip, ref_idx, MVD, CBP, DQP syntax helpers, including neighbour-dependent skip/ref/MVD/transform-size/chroma-pred contexts, FFmpeg-style `mb_qp_delta` context state threading, and guarded helper boundaries for malformed direct use.
- CABAC residual category/bounds validation, FFmpeg-style 8×8 residual quadrant layout and non-zero-context write-back, and MV/ref context guards for malformed direct helper/tool inputs.
- CABAC P8x8 sub-MB type decoding, variable sub-partition MVD consumption, and FFmpeg-style transform_size_8x8_flag eligibility for full-8x8-only sub partitions.
- CABAC chroma DC/AC coefficient placement across the four chroma 4×4 blocks for both inter and intra paths.
- CABAC coded-block-flag and residual decoding.
- CABAC end-of-slice terminate handling and byte-aligned arithmetic decoder initialization after slice header parsing.
- H.264 zigzag scan mapping for residual output.
- I8x8 prediction modes and strong reference-pixel filtering.
- Inter-MB `transform_size_8x8_flag` decode path and 8×8 residual category support.
- Slice/PPS syntax keeps later fields aligned by consuming POC deltas, non-reference-slice ref-marking absence, SP/SI-only fields, weighted-prediction tables, reference-list modification operands, unsigned deblocking idc/offsets, and PPS slice-group-map syntax even though weighted prediction and FMO reconstruction are still future work.
- I_PCM macroblocks consume aligned raw 8-bit 4:2:0 samples and reconstruct them directly.
- Chroma intra plane prediction mode is implemented; DC fallback remains only for unavailable plane references.

Still gated:

- Main/High CABAC frame quality is still below the completion gate despite the P-slice syntax/context fixes above.
- CABAC I8x8 `transform_size_8x8_flag` remains disabled in the intra path behind `enableCABACI8x8Transform=false` because enabling it currently lowers BBB CABAC quality. This should not be toggled on without a reference-grounded fix and PSNR/syntax parity proof.

### Current reference metrics

| Fixture | Current value |
|---|---:|
| `dark64` avg PSNR | 31.23 dB |
| Baseline CAVLC avg PSNR | 27.65 dB |
| Baseline YUV PSNR | Y=39.58 U=26.47 V=21.76 dB |
| `bbb-frame0` CABAC avg PSNR | 7.81 dB |
| BBB baseline decode allocations | ~10.9 MB/op, ~1.3k allocs/op |
| BBB baseline decode sample | ~44-52 ms/op typical recent sample |

## Validation commands

Use a workspace temp directory in this container because `/tmp` may be mounted `noexec`:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp

go test ./...
go vet ./...
go test -v ./decode -run 'TestConformancePSNR|TestConformanceYUV|TestSyntaxParity'
go test ./decode -run '^$' -bench BenchmarkDecode -benchmem
GOOS=linux GOARCH=arm64 go build ./...
```

Regenerate checked-in tables:

```bash
go generate ./entropy/cabac ./entropy/cavlc
```

## SIMD / low-level acceleration plan

Recent completed guardrails and low-level improvements:

- SIMD/scalar parity tests cover intra prediction wrappers, inter-copy wrappers, SAD, DCT4x4, IDCT4x4, IDCT8x8, and DCT8x8 fallback behavior.
- `_other.go` and arch-mismatch fallbacks are scalar-safe and nil/stride-guarded where they wrap `unsafe.Slice`.
- `DCT8x8_ASM` now delegates to scalar until a real forward-8x8 implementation passes parity.
- `transform.IDCT4x4Batch` is wired into residual paths as the integration seam for future batched AVX2/NEON kernels.
- `nal.Reader` caches EPB presence; no-EPB `ReadBits`/`PeekBits` use raw backing-byte fast paths, and `ReadBits`, `BitsLeft`, raw-position `Seek`, and `ByteAlign` are defensively clamped/EPB-aware.
- CAVLC `coeff_token`, `total_zeros`, and `run_before` have prefix lookups with exhaustive scan-vs-lookup invariant coverage; `level_prefix` has a 16-bit leading-zero fast path with capped fallback.
- CAVLC residual decode uses fixed stack arrays for trailing-one signs and levels.
- `pred.InterPred16x16At` has fast paths for interior fractional-MV bilinear interpolation plus horizontal-only/vertical-only fractional interpolation while preserving the clipped edge path.
- `decode.copyInterSubRect` copies integer-MV P8x8 sub-rectangles directly, preserving fractional fallback semantics.
- `decode.fillChromaInterPred` has an interior 8×8 row-copy fast path plus malformed-input guards; inter chroma prediction now respects P16x8/P8x16/P8x8 partition boundaries and P8x8 8×4/4×8/4×4 sub-partition MVs at 4:2:0 scale.
- Inter luma/chroma residual write-back now writes directly to frame rows after the same add + clip operation, avoiding per-pixel setter calls in the hot path; direct helper inputs, frame extents, residual category/coefficient bounds, and CABAC 8×8 residual quadrant/non-zero-context handling are guarded to avoid panics, coefficient scrambling, or stale neighbour context on malformed internal tests/tools.
- Inter zero-residual paths copy prediction directly for uncoded luma CBP groups, zero-`TotalCoeff` 4×4 blocks, all-zero 8×8 transform groups, chroma CBP=0, and zero chroma 4×4 residual blocks.
- Decoder and `trace264` now share the same QP wraparound semantics and 4×4 MV/ref-cache source-of-truth model; stale macroblock-level trace MV context was removed, and MV cache read/fill helpers reject bad strides, short slices, and negative origins.
- SPS/PPS parsing has defensive scaling-list wraparound and continues past PPS slice-group maps before reading ref counts, weighted prediction flags, QP offsets, deblocking flags, and High-profile extensions. Slice parsing now follows FFmpeg ordering for POC type 0/1 deltas, reference marking gated by `nal_ref_idc`, SP/SI fields, and unsigned deblocking idc syntax.
- Intra/inter/B reconstruction use fixed stack prediction buffers for 16×16 temporaries and guard direct helper/tool inputs for nil frames, nil macroblocks, invalid references, and out-of-frame macroblock coordinates. B-slice list-use syntax helpers now follow FFmpeg/H.264 table mappings instead of simplified broad fallbacks.
- `transform.IDCT4x4BatchMask` skips transform work for known-zero dense residual slots.
- `transform.Dequant4x4` uses precomputed scale tables; `Dequant4x4Block` handles fixed-size hot decode blocks; public `Quant4x4`/`Dequant4x4` helpers are hardened for short blocks and invalid QP values.

Current measured targets:

1. Re-profile after current no-EPB bit-reader and CAVLC prefix fast paths; keep further bit IO changes only if they beat the ~44-52 ms/op sample range
2. True batched `IDCT4x4Batch` / dequant kernels for amd64/arm64 if profiling justifies assembly
3. Remaining motion-compensation variants not yet covered by row-copy/interior/sub-rect fast paths
4. Deblocking SIMD after reconstruction parity remains stable
5. Decoder allocation cleanup beyond frame buffers/expected slice state

Planned implementation shape:

- amd64: AVX2/SSE2 kernels only where profiling and parity tests justify assembly.
- arm64: NEON kernels matching the existing assembly dispatch pattern.
- `_other.go` / arch-mismatch functions must remain scalar-safe, not panic.
- SIMD parity tests must compare scalar vs assembly pixel-exact or coefficient-exact outputs.
- Benchmarks should report before/after fps, bytes/op, and allocs/op.

## Encoder roadmap

Encoder work remains future work after decoder/SIMD stabilization.

Target public shape:

```go
enc, err := h264.NewEncoder(h264.Config{
    Width: 1920, Height: 1080,
    Profile: h264.ProfileHigh,
    Preset: h264.PresetMedium,
    RateControl: h264.CRF(23),
})
if err != nil { /* handle */ }
defer enc.Close()

nals, err := enc.Encode(frame) // frame is YUV420 input
```

Phases:

1. Baseline encoder: I/P slices, CAVLC, integer transforms, simple rate control.
2. Main/High: CABAC, B-frames, 8×8 transform, weighted prediction.
3. Motion estimation: scalar full search first, then SAD/SATD SIMD.
4. Mode decision / RDO.
5. Optional GPU experiments for embarrassingly parallel search/transform work.

## GPU notes

GPU acceleration is experimental and should be treated as optional. Likely GPU-friendly workloads are motion search, batched SAD/SATD, batched transform, and possibly deblocking experiments. CABAC is inherently sequential and not a GPU target.

## Profiles and levels

Initial implementation targets:

- **Baseline**: I + P slices, CAVLC, no B-frames.
- **Main**: B-frames, CABAC, weighted prediction.
- **High**: 8×8 transform, I8x8 prediction, adaptive quantization.

Useful levels:

- Level 3.0: 720p30
- Level 4.0: 1080p30
- Level 4.1: 1080p60
- Level 5.1: 4K30
