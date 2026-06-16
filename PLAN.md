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
frame/            YUV420 frame storage, DPB helpers, ChromaQP, guarded SafePixelY/block helpers
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
  trace264        CAVLC syntax tracer plus decoder-backed CABAC MB/event diagnostics
  trace264cmp     FFmpeg/showinfo comparison, frame stats, histograms
  trace264diff    Trace diff helper
```

Historical note: the package formerly named `slice` is now `syntax`, and the old monolithic `entropy` package is split into `entropy/cavlc` and `entropy/cabac`.

## Decoder status

### Completed hard gates

- Baseline CAVLC decode is correct enough to be marked complete.
- Syntax parity tooling exists (`trace264`, `trace264cmp`, `trace264diff`) plus FFmpeg-backed CABAC first-divergence scripts.
- Motion-vector parity has been validated on representative P-slice macroblocks.
- Reconstruction parity has FFmpeg YUV/PSNR regression tests.
- Chroma dequant now applies `chroma_qp_index_offset` through `frame.ChromaQP`.
- Chroma intra DC prediction now matches FFmpeg's quadrant/edge predictors (`pred8x8_dc`, `pred8x8_left_dc`, `pred8x8_top_dc`).
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
- CABAC skip, ref_idx, MVD, CBP, DQP syntax helpers, including neighbour-dependent skip/ref/MVD/transform-size/chroma-pred/luma-DC/chroma-DC contexts, FFmpeg-style `mb_qp_delta` context state threading, FFmpeg-style MVD context-cache magnitude clamping, and guarded helper boundaries for malformed direct use.
- CABAC residual category/bounds validation, FFmpeg-style 8×8 residual quadrant layout and non-zero-context write-back, and MV/ref context guards for malformed direct helper/tool inputs.
- CABAC P8x8 sub-MB type decoding, variable sub-partition MVD consumption, and FFmpeg-style transform_size_8x8_flag eligibility for full-8x8-only sub partitions.
- CABAC chroma DC/AC coefficient placement across the four chroma 4×4 blocks for both inter and intra paths.
- CABAC coded-block-flag and residual decoding, including FFmpeg high-bit CBP context tracking for I16x16 luma DC and chroma DC.
- CABAC end-of-slice terminate handling, byte-aligned arithmetic decoder initialization after slice header parsing, reinitialization after CABAC I_PCM raw sample payloads, and FFmpeg-compatible CABAC arithmetic/table mode (signed C table initializers preserved modulo 256; unaligned three-byte low seed verified against FFmpeg's C decoder trace).
- H.264 zigzag scan mapping for residual output.
- I8x8 prediction modes, strong reference-pixel filtering, and CABAC intra `transform_size_8x8_flag` consumption.
- Inter-MB `transform_size_8x8_flag` decode path and 8×8 residual category support.
- Slice/PPS syntax keeps later fields aligned by consuming POC deltas, non-reference-slice ref-marking absence, SP/SI-only fields, weighted-prediction tables, reference-list modification operands, unsigned deblocking idc/offsets, PPS slice-group-map syntax, and changing-FMO `slice_group_change_cycle` bits even though weighted prediction and FMO reconstruction are still future work. B-slice CAVLC macroblock syntax now consumes FFmpeg/H.264 table-driven list-use, sub-partition MVD counts, TE ref_idx values, direct residual syntax, B intra payloads, and luma/chroma residual coefficients; B skip runs are applied in the decode branch.
- I_PCM macroblocks consume aligned raw 8-bit 4:2:0 samples and reconstruct them directly in both CAVLC and CABAC paths; CABAC resets arithmetic state after the raw payload.
- Chroma intra plane prediction mode is implemented; DC fallback remains only for unavailable plane references.

Current parity tooling and findings:

- `scripts/cabac_parity_baseline.sh` is the repeatable baseline harness for Go output, FFmpeg output, PSNR, snapshots, and logs.
- `scripts/cabac_firstdiv.sh` patches/builds the local FFmpeg source tree when needed and compares decoder-backed Go CABAC MB traces against FFmpeg traces. It filters Go events to the FFmpeg-decoded frame range so event-count failures do not mask the first real frame-0 divergence.
- CABAC diagnostics now cover MB summaries, CBP bin decisions with consistent arithmetic state, residual CBF/significant/last/level decisions, and intra syntax bins.
- `testsrc_cabac_p.h264` and `bbb_annexb.h264` first-frame MB syntax summaries now report `NO_DIVERGENCE in compared fields` with FFmpeg-compatible CABAC arithmetic enabled.
- `GO264_RECON_TRACE=1` now emits luma Intra_8x8 syntax vs reconstruction mode, prediction reference samples (`top`, `left`, `top_left`), raw row-major coefficients, FFmpeg-storage raw coefficient view, dequantized coefficients, prediction/residual/output and pre/post-IDCT checksums, plus chroma prediction/residual/output and per-4×4 block checksums, enabling direct FFmpeg reconstruction comparisons.
- Recent accepted reconstruction/motion fixes include luma Intra_8x8 filtered DC references, FFmpeg chroma DC quadrant/edge predictors, FFmpeg `pred_intra_mode` unavailable-neighbour handling, separate I4x4-derived right/bottom mode caches for CABAC I8x8 neighbour prediction, aligned-buffer reconstruction for partial edge macroblocks, FFmpeg-scale 8×8 dequant before IDCT, available top-right references from already reconstructed rows, FFmpeg-exact I8x8 horizontal-down and vertical-right predictors, partial-edge chroma reconstruction, B-slice MB-type default-branch parity, shaped B_8x8/sub-partition MV cache write-back, separate CABAC B L0/L1 MVD context caches, direct B_8x8 write-back into both list caches, shape-derived 16×8/8×16 directional MVP routing for all two-part B MB types, P16x8 top-part cache write-back before bottom-part MVP prediction, reference-frame-only DPB list filtering, and saved per-frame list0 4×4 motion/ref metadata for future Direct-mode colocated checks. Per-block I4x4/I8x8 luma and chroma reconstruction matches FFmpeg with loop filter disabled; B-frame quality now depends mainly on proper FFmpeg-style Direct-mode shape/colocated derivation.

Still gated:

- B-slice Direct-mode reconstruction is still quality-gated against FFmpeg-derived colocated temporal/spatial derivation. The active comparison fixture is the regenerated `bootstrap_fixtures.sh` BBB stream (SHA-256 `1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff`); historical POC128/134 notes are archival unless the exact old transient fixture is restored. Full `B_Direct_16x16` and direct `B_8x8` sub-MBs now seed and write back representative motion for neighbour parity, saved reference frames carry list0 4×4 motion/ref caches, the B_8x8 zero helper follows FFmpeg's `x8*3/y8*3` representative rule, and `compare_direct_writeback.py` proves direct representatives and motion-cache write-back agree in the focused POC=6 gate (`compared=1111 diffs=0`). Remaining work is to keep widening the FFmpeg-vs-Go Direct checks across later pictures and fix only the first source-grounded reference/MV derivation mismatch that still reproduces.
- Multi-frame CABAC diagnostics are now usable for post-motion B parity via `scripts/b_bidi_trace.sh`/`scripts/compare_bidi_trace.py`, plus `scripts/compare_bpart_mvd.py` for FFmpeg-vs-Go partition MVD/MVP/final-MV triage. After the B cache layer, direct write-back diagnostics, P16x8 top-before-bottom MVP fix, P 4x4 bottom-right diagonal handling, top-edge B_Bi_L0_16x8 MVP handling, cropped-edge trace fixes, Direct-flag normalization, unused-cache seeding, FFmpeg-style list-by-list `B_8x8` MVD decode order, saved colocated list1 motion/ref metadata, per-8x8 colocated Direct-zeroing, FFmpeg-style refusal to zero spatial Direct from unavailable colocated L0 representatives, B-intra transform8x8 neighbor-context write-back, and B 8x16 part-1 MVP handling including right-edge top/left/generic fallbacks for B_Bi_L0/B_L0_L0/B_L0_L1, the historical transient BBB fixture had POC30/32/36/38/44/46/48/54/56/66/70/72/74/80/82/84/86/88/90/92/96/98/100/102/104/106/108/112/114/116/118/120/122/124/174/176 full-frame BIDI gates clean; POC128 advanced past the earlier MB88 chain to MB168 after refusing unavailable-L0 Direct zeroing, POC34 was presentation-only, and POC78/94/126/134 plus later window-selection gaps remained the next widened Direct targets. If `/workspace/tmp` is rebuilt from `scripts/bootstrap_fixtures.sh`, first remap those historical POC targets onto the regenerated fixture (or restore the exact old BBB encoding) before treating diffs as decoder regressions. `scripts/b_direct_trace.sh`/`scripts/b_bidi_trace.sh` also emit `FFCOLZERO/FFCOLZERO8`, `FFMOTSAVE4`, `GOCOLZERO`, `GOMOTSAVE`, `GOMOTWRITE`, and occurrence-keyed diff artifacts with optional FF `colpoc` filtering so write-back regressions are caught separately from real FFmpeg-vs-Go Direct-mode derivation mismatches. FF BIDI/MVP/MVD trace refresh now preserves full `cur_pic_ptr` POC when available, FF Direct rows include `colpoc=`, B L1 colocated selection uses effective POC ordering around compact POC wrap, `b_bidi_trace.sh` can extract `goheader.rows` with `GO264_HEADER_TRACE=1`, and `compare_bidi_trace.py` normalizes FF's internal 65536 wrap offset, keys rows by `(frame, poc, occurrence, mb)`, and ignores FF intra MB rows plus sub-partition flag-only presentation differences when resolved motion representatives match, so repeated compact POCs across regenerated GOP windows and trace presentation are not collapsed into false decode diffs.
- `scripts/recon_i8x8_compare.py` now compares by `(frame,mb,b8,occurrence)` and can filter/sort by prediction or residual delta (`--max-pred-delta`, `--min-pred-delta`, `--sort out|pred|res`) plus summarize by predictor mode or spatial bucket. `scripts/i8x8_mode_compare.py` compares Go I8x8 predicted/decoded modes and edge-cache inputs with FFmpeg `FFMODE` rows, including decoded-mode-only filtering; after the partial-edge fix it reports no decoded-mode mismatches for the compared `bbb` first-frame I8x8 rows. `scripts/i4x4_mode_compare.py` compares Go I4x4 raw/final modes with FFmpeg decoded/writeback rows.

### Current reference metrics

| Fixture | Current value |
|---|---:|
| `dark64` avg PSNR | 31.23 dB |
| Baseline CAVLC avg PSNR | 27.65 dB |
| Baseline YUV PSNR | Y=39.58 U=38.13 V=34.03 dB |
| `testsrc_cabac_p.h264` frame 0 | Y=56.96 U=60.68 V=64.62 dB |
| `bbb_annexb.h264` frame 0 | Y=80.33 U=56.14 V=57.08 dB |
| historical `bbb_annexb.h264` 300-frame avg | Y=21.39 U=33.86 V=38.32 dB |
| regenerated `bootstrap_fixtures.sh` BBB 300-frame avg | Y=4.72 U=30.34 V=32.39 dB |
| `bbb_annexb.h264` B POC=2 / POC=6 | Y≈41.4 / 37.8 dB display-order early B frames |
| `bbb_annexb.h264` later B/P frames | luma remains quality-gated by remaining inter prediction/reference parity |
| `bbb_annexb.h264` frame-0 deblocked PSNR | Y=80.33 U=56.14 V=57.08 dB |
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

Bootstrap transient local fixtures/tooling and then run the CABAC parity loop:

```bash
./scripts/bootstrap_fixtures.sh
./scripts/cabac_parity_baseline.sh /workspace/tmp/testsrc_cabac_p.h264 /workspace/tmp/go264-cabac-parity-baseline
./scripts/cabac_firstdiv.sh /workspace/tmp/testsrc_cabac_p.h264 /workspace/tmp/go264-cabac-firstdiv
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
- CAVLC residual decode uses fixed stack arrays for trailing-one signs and levels, including chroma DC, and public residual/VLC helpers guard nil direct-reader use while clamping malformed coefficient counts before fixed-buffer indexing; inter 8×8-transform residuals follow FFmpeg's `zigzag_scan8x8_cavlc` chunk ordering before coefficients are split back into the decoder's four 4×4 storage slots.
- `pred.InterPred16x16At` has fast paths for interior fractional-MV bilinear interpolation plus horizontal-only/vertical-only fractional interpolation while preserving the clipped edge path.
- `decode.copyInterSubRect` copies integer-MV P8x8 sub-rectangles directly, preserving fractional fallback semantics.
- `decode.fillChromaInterPred` has an interior 8×8 row-copy fast path plus malformed-input guards; inter chroma prediction now respects P16x8/P8x16/P8x8 and B-slice partition boundaries, including P8x8/B8x8 8×4/4×8/4×4 sub-partition MVs at 4:2:0 scale and H.264 chroma interpolation.
- Inter luma/chroma residual write-back now writes directly to frame rows after the same add + clip operation, avoiding per-pixel setter calls in the hot path; direct helper inputs, frame extents, residual category/coefficient bounds, inter chroma CBP syntax-bit masking, CABAC arithmetic decoder inputs, static CABAC chroma DC scan storage, and CABAC 8×8 residual quadrant/non-zero-context handling are guarded to avoid panics, coefficient scrambling, or stale neighbour context on malformed internal tests/tools.
- Inter zero-residual paths copy prediction directly for uncoded luma CBP groups, zero-`TotalCoeff` 4×4 blocks, all-zero 8×8 transform groups, chroma CBP=0, and zero chroma 4×4 residual blocks.
- Decoder and `trace264` now share the same QP wraparound semantics and 4×4 MV/ref-cache source-of-truth model; B-intra QP deltas are read from parsed intra payloads in both decode/trace flows, stale macroblock-level trace MV context was removed, B_8x8 direct sub-MBs are written back to both list caches, two-part B MVP routing is derived from partition shape, P16x8 top motion is written before bottom MVP prediction, and MV cache read/fill helpers reject bad strides, short slices, and negative origins.
- SPS/PPS parsing has defensive scaling-list wraparound, saturated cropped-dimension derivation for malformed crop fields, and continues past PPS slice-group maps before reading ref counts, weighted prediction flags, QP offsets, deblocking flags, and High-profile extensions. Slice parsing now follows FFmpeg ordering for POC type 0/1 deltas, reference marking gated by `nal_ref_idc`, SP/SI fields, unsigned deblocking idc syntax, and changing-FMO `slice_group_change_cycle` consumption.
- Frame and reconstruction helper boundaries are guarded for direct tests/tools: `SafePixelY`/4×4 block helpers validate malformed frame storage, while intra/inter/B reconstruction uses fixed stack prediction buffers for intra edge/prediction temporaries and guards nil frames, nil macroblocks, invalid references, malformed B prediction rectangles/reference storage, chroma intra plane storage, and out-of-frame macroblock coordinates. B-slice syntax helpers now follow FFmpeg/H.264 table mappings for list use/sub-partition counts, share inter residual CAVLC decoding with P-slices including the 8×8-transform scan path, clamp malformed TE ref_idx values, and pass slice/PPS/neighbour context through `BidiDecodeOpts`.
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

## Resolved investigation notes: P-frame CABAC divergence (2026-05-22/23)

The original P-frame collapse was traced to CABAC arithmetic/table incompatibility with FFmpeg/x264 rather than P-slice syntax. The critical symptom was the first P-frame skip/MVD arithmetic path decoding from the wrong LPS-range model, causing frame-1 quality around 21 dB and cascading inter-frame failures.

Resolved facts:

- FFmpeg-compatible CABAC arithmetic is enabled.
- Signed C table initializers are preserved modulo 256.
- CABAC initialization uses the unaligned three-byte seed compatible with FFmpeg.
- Active first-frame MB syntax summaries for `bbb_annexb.h264` and `testsrc_cabac_p.h264` report `NO_DIVERGENCE in compared fields`.
- P-frame syntax/arithmetic is no longer considered the active multi-frame blocker.

Current multi-frame quality is instead gated by FFmpeg-style B-slice Direct-mode derivation, reference-list/cache parity, and remaining trace-window disambiguation. The current source-grounded post-motion target is the POC128/134 chain: POC128 now advances past the earlier MB88 Direct-zeroing chain to MB168, which traces upstream into unresolved POC134/window-selection state.
