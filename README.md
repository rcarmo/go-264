# go-264

H.264/AVC decoder-first implementation in pure Go with assembly acceleration hooks for amd64/arm64 and optional GPU experiments.

The current focus is a correct, inspectable decoder core. Encoder work remains planned; the implemented code is already useful for Annex B parsing, H.264 syntax tracing, CAVLC/CABAC experimentation, frame reconstruction, PSNR regression testing, and SIMD kernel development.

## Current status

| Area | Status | Notes |
|---|---:|---|
| Annex B / NAL parsing | ✅ | Start-code scan, emulation-prevention removal, SPS/PPS parsing including PPS slice-group-map/change-cycle consumption, scaling-list guardrails, and saturated cropped-dimension derivation |
| Bitstream reader | ✅ | Fixed-width, Exp-Golomb, fuzz-tested |
| Slice syntax | ✅ | I/P/B header parsing, POC/ref-marking/SP-SI/deblock field consumption, weighted-prediction table skipping, I_PCM sample consumption, FFmpeg-aligned B-slice list-use/sub-partition syntax, and B intra/residual payload consumption; macroblock syntax lives in `syntax/` |
| CAVLC | ✅ | Baseline CAVLC decode is the current hard-gated completion point; High-profile inter 8×8 transform residuals now consume FFmpeg-style CAVLC scan chunks |
| CABAC | 🔶 | Real CABAC contexts, P-slice/intra-in-P syntax, residuals, CBP/DQP/ref/MVD; byte-aligned arithmetic init, FFmpeg luma/chroma/DQP/MVD contexts, I_PCM payload/reset handling, 8×8 residual layout/non-zero context, and first-frame MB syntax parity for the active CABAC fixtures; per-block I4x4/I8x8 luma and chroma reconstruction match FFmpeg with loop filter disabled; B-slice CABAC has FFmpeg-aligned syntax and an expanding post-motion parity window for the reference B frame, with remaining quality gap dominated by full Direct-mode derivation |
| Intra prediction | ✅ | I4x4, I8x8, I16x16, chroma DC/horizontal/vertical/plane; chroma DC matches FFmpeg quadrant/edge predictors; I8x8 strong reference filter implemented |
| Inter prediction | ✅ | P skip, P16x16/P16x8/P8x16/P8x8, 4×4 MV/ref cache write-back, partition-aware chroma prediction |
| Transforms | ✅ | 4×4 and 8×8 integer transforms with scalar + assembly dispatch hooks |
| Deblocking | ✅ | Scalar correctness tests; SIMD deblock is planned |
| Frame / DPB | ✅ | YUV420 frame storage, guarded safe pixel/block helper reads, reference frame tracking |
| Validation | ✅ | Syntax parity, FFmpeg/YUV PSNR gates, fuzz/unit tests |
| Encoder | ⬜ | Planned after decoder/SIMD gates |

Legend: ✅ implemented · 🔶 partial/quality-gated · ⬜ planned

## Decoder quality gates

Current regression gates are intentionally conservative guards, not a claim of full H.264 conformance:

- Syntax parity: per-slice/MB trace comparison tooling against FFmpeg-derived references
- Motion parity: representative macroblock MV checks against FFmpeg motion-vector side data
- Reconstruction parity: frame PSNR and max-diff checks against FFmpeg YUV output
- Baseline CAVLC: marked complete
- CABAC: first-frame syntax parity and per-block reconstruction (I4x4/I8x8 luma + chroma) match FFmpeg with loop filter disabled; deblocking is wired, and current B-frame work is focused on FFmpeg-grounded Direct-mode derivation and remaining reference/MV cache parity

Recent reference values:

| Fixture | Metric |
|---|---:|
| `dark64` | 31.23 dB avg PSNR |
| Baseline CAVLC | 27.65 dB avg PSNR |
| Baseline YUV | Y=39.58 U=38.13 V=34.03 dB |
| `testsrc_cabac_p.h264` frame 0 | Y=56.96 U=60.68 V=64.62 dB |
| `bbb-frame0` CABAC | ~31 dB avg PSNR (est.) |
| `bbb_annexb.h264` frame 0 | Y=59.85 U=56.14 V=57.08 dB |
| `bbb_annexb.h264` B POC=2 / POC=6 | Y≈21.8 / 21.5 dB |
| `bbb_annexb.h264` later B POC=14 / POC=20 | Y≈19.6 / 19.3 dB |
| `bbb_annexb.h264` 300-frame luma average | Y≈8.14 dB |

## Package layout

```text
go-264/
├── nal/              Annex B, NAL units, SPS/PPS, bit reader
├── frame/            YUV420 frames, DPB helpers, ChromaQP, guarded SafePixelY/block helpers
├── entropy/
│   ├── cabac/        CABAC decoder, context init tables, residual decoder
│   └── cavlc/        CAVLC block decoder and VLC tables
├── syntax/           H.264 syntax layer: slice headers, MBIntra/MBInter, CAVLC/CABAC MB syntax
├── pred/             Intra/inter prediction kernels and SIMD dispatch hooks
├── transform/        4×4/8×8 integer transforms, dequant, scalar/SIMD fallbacks
├── filter/           Deblocking filter
├── me/               Motion-estimation kernels (SAD/SATD)
├── gpu/              GPU experiment stubs/parity scaffolding
├── decode/           End-to-end decoder pipeline and conformance tests
├── internal/tables/  Reproducible table generator commands used by go:generate
└── cmd/
    ├── decode264     Annex B decoder: color PNG, luma PNG, or raw YUV output
    ├── trace264      CAVLC syntax tracing plus decoder-backed CABAC MB/event diagnostics
    ├── trace264cmp   Syntax/parity comparison and frame/MB histogram summaries
    └── trace264diff  Trace diff helper
```

The old `slice` package was intentionally renamed to `syntax` to avoid confusion with Go slices. Entropy coding is intentionally split into `entropy/cavlc` and `entropy/cabac` so call sites import the exact coding mode they need.

## Command-line tools

### Decode Annex B to images/YUV

```bash
go build -o /workspace/tmp/decode264 ./cmd/decode264
/workspace/tmp/decode264 -i input.h264 -o frames -f color   # default, BT.601 YUV→RGB PNG
/workspace/tmp/decode264 -i input.h264 -o frames -f png     # luma-only PNG
/workspace/tmp/decode264 -i input.h264 -o frames -f yuv     # raw planar YUV420
```

### Trace macroblock syntax and CABAC decode events

```bash
go build -o /workspace/tmp/trace264 ./cmd/trace264
/workspace/tmp/trace264 -i input_baseline.h264 -limit 64
```

`trace264` still has a parser-side CAVLC syntax mode for Baseline fixtures, but `-cabac` now routes through the decoder and emits MB-level CABAC events for Main/High debugging. Its QP, B-intra payload, and MV-prediction bookkeeping is kept aligned with the decoder so diagnostic output uses the same wraparound and 4×4 MV/ref cache semantics.

Useful CABAC/reconstruction diagnostics are opt-in environment variables so normal decode remains quiet:

- `GO264_CABAC_CBP_TRACE=1` — CBP bin/context/arithmetic-state trace
- `GO264_CABAC_RESIDUAL_TRACE=1` — residual CBF/significant/last/level trace, plus `levelseq`/`matrixseq` decode-order diagnostics for CABAC 8×8 residual placement
- `GO264_CABAC_ARITH_TRACE=1` — CABAC arithmetic state trace
- `GO264_CABAC_SYNTAX_TRACE=1` — intra syntax bin trace
- `GO264_RECON_TRACE=1` — reconstruction checksums for luma Intra_8x8 and chroma prediction/residual/output, including syntax vs reconstruction modes, raw prediction references (`top`, `left`, `top_left`), raw row-major coefficients, FFmpeg-storage raw coefficient view, dequantized coefficients, per-4×4 prediction/residual/output, and pre/post-IDCT sums

### Compare against FFmpeg-derived frame metadata

```bash
go run ./cmd/trace264cmp -i input.h264 -v
```

## FFmpeg CABAC parity workflow

The active CABAC/Main/High work uses FFmpeg as the reference implementation and fixes one source-grounded divergence at a time. The canonical fixture order is `testsrc_cabac_p.h264`, then `bbb-frame0`, then broader Main/High samples.

Two scripts support the loop:

```bash
./scripts/cabac_parity_baseline.sh /workspace/tmp/testsrc_cabac_p.h264 /workspace/tmp/go264-cabac-parity-baseline
./scripts/cabac_firstdiv.sh /workspace/tmp/testsrc_cabac_p.h264 /workspace/tmp/go264-cabac-firstdiv
```

`cabac_parity_baseline.sh` regenerates Go YUV/PNG, FFmpeg YUV/showinfo, PSNR, and a summary in one repeatable command. `cabac_firstdiv.sh` patches/builds the local FFmpeg tree under `/workspace/tmp/ffmpeg-7.1.3` when needed, captures FFmpeg CABAC MB traces, captures Go decoder-backed traces, filters Go events to the FFmpeg-decoded frame range, and reports the first mismatching MB-level field.

Current first-divergence status: `testsrc_cabac_p.h264` and `bbb_annexb.h264` first-frame MB syntax summaries report `NO_DIVERGENCE in compared fields`. Per-block I4x4/I8x8 luma and chroma reconstruction match FFmpeg exactly with loop filter disabled: `bbb` frame 0 is Y=99.00 U=99.00 V=99.00 dB without deblocking. The in-loop deblocking filter is wired into the decode pipeline (H.264 §8.7, `filter.DeblockMBFrame`), lifting `bbb` frame 0 to Y=59.85 U=56.14 V=57.08 dB vs FFmpeg's deblocked output; the residual first-frame gap is a post-pass ordering artefact (uniform across all pixel zones, max diff=6). For multi-frame B-slice decode, the active blocker is not deblocking but FFmpeg-style Direct-mode derivation: the current bounded fallback gives useful but incomplete B-frame quality (for example POC=2/6 around 21.8/21.5 dB and POC=14/20 around 19.6/19.3 dB). Recent source-grounded fixes include FFmpeg-style CABAC intra edge contexts, High-profile intra `transform_size_8x8_flag` consumption, luma Intra_8x8 filtered DC references, FFmpeg chroma DC quadrant/edge prediction, FFmpeg `pred_intra_mode` unavailable-neighbour handling, separate I4x4-derived right/bottom mode caches for CABAC I8x8 neighbour prediction, aligned-buffer reconstruction for partial edge macroblocks, FFmpeg-scale 8×8 dequant before IDCT, correct I8x8 top-right references from already reconstructed rows, FFmpeg-exact horizontal-down and vertical-right prediction, explicit top-right availability in I8x8 predictors, partial-edge chroma reconstruction, B-slice MB-type parity, shaped B_8x8/sub-partition MV write-back, and reference-frame-only DPB list filtering.

For reconstruction triage, `scripts/recon_i8x8_compare.py` compares Go `GORECON part=i8x8` and FFmpeg `FFRECON part=i8x8` logs by `(frame, mb, b8, occurrence)`. It supports focused filters (`--frame`, `--mb`, `--b8`, `--occurrence`), prediction/residual splits (`--max-pred-delta`, `--min-pred-delta`, `--sort out|pred|res`), mode filters (`--mode-mismatch`), and summaries (`--summary-by-mode`, `--summary-spatial`). `scripts/i8x8_mode_compare.py` compares Go `trace264` I8x8 predicted/decoded modes and edge-cache inputs against local FFmpeg `FFMODE part=i8x8` rows; after the accepted cache and partial-edge fixes, it reports no decoded-mode mismatches for the compared `bbb` first-frame I8x8 rows. `scripts/i4x4_mode_compare.py` performs the matching Go/FFmpeg comparison for I4x4 raw/final/writeback rows. B-direct work uses `scripts/b_direct_trace.sh` to patch local FFmpeg `h264_direct.c` and emit deterministic `FFDIRECT` rows, `GO264_DIRECT_TRACE=1` to emit matching `GODIRECT` rows from Go, and `scripts/compare_direct_trace.py` to compare `(ref0, ref1, mv0, mv1)`, direct sub-MB types, and per-sub-block direct MVs. The same script also emits `FFCOLZERO/FFCOLZERO8`, `GOCOLZERO`, `GOMOTSAVE`, and `GOMOTWRITE` rows; `scripts/compare_colzero_trace.py` focuses colocated-zero rows, while `scripts/compare_direct_writeback.py` checks that direct sub-MV representatives agree with final motion-cache write-back and can filter by POC, spatial mode, MB type, occurrence, and FF direct MV equality. The current direct write-back gate for `bbb` POC=6 reports `compared=1111 diffs=0`, so explicit-zero direct sub-MVs are no longer being mistaken for unset write-back cells in the checked window. Remaining direct-mode mismatches are true FFmpeg-vs-Go reference/MV derivation differences, first visible in the focused POC=6 window around `mb=0291`, `mb=0297`, `mb=0307`, `mb=0331`, and `mb=0339`. Post-motion B-slice work uses `scripts/b_bidi_trace.sh` to emit FFmpeg `FFBIDI` rows after ref/MVD/MVP write-back and Go `GOBIDI` rows after reconstruction-time motion resolution, then `scripts/compare_bidi_trace.py` compares only used-list representative cache entries so stale/unused FFmpeg cache cells do not drive fixes. After the recent B motion-cache, direct write-back, and P16x8 top-before-bottom MVP fixes, the focused reference B-frame window through `MB_LIMIT=400` is down to one post-motion diff (`mb=0371`, `compared=136 diffs=1`). FFmpeg `frame_num` repeats across B pictures, so select repeated groups with `--ff-occurrence` when comparing beyond the first occurrence.

## Validation and profiling

Because some containers mount `/tmp` as `noexec`, set `TMPDIR`/`GOTMPDIR` to a workspace path when running Go tools here:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp

go test ./...
go vet ./...
go test -v ./decode -run 'TestConformancePSNR|TestConformanceYUV|TestSyntaxParity'
go test ./decode -run '^$' -bench BenchmarkDecode -benchmem
GOOS=linux GOARCH=arm64 go build ./...
```

Current decode profiling has already removed the largest hot-path allocations and added several low-level guardrails:

| Benchmark | Before | Current |
|---|---:|---:|
| BBB baseline allocated bytes | ~87.5 MB/op | ~10.9 MB/op |
| BBB baseline allocations | ~18.8k/op | ~1.3k/op |
| BBB baseline decode sample | ~125-145 ms/op | ~44-52 ms/op typical recent sample |

Recent performance/safety work:

- `nal.Reader` caches whether a payload contains emulation-prevention bytes; no-EPB `ReadBits` and `PeekBits` use raw backing-byte fast paths, while EPB-bearing/short-tail reads keep the safe path. `ReadBits`, `BitsLeft`, raw-position `Seek`, and `ByteAlign` have defensive bounds/EPB handling.
- CAVLC `coeff_token`, `total_zeros`, and `run_before` now have prefix lookup tables with exhaustive scan-vs-lookup invariant tests; `level_prefix` has a 16-bit leading-zero fast path with capped fallback.
- CAVLC residual decode uses fixed stack arrays for trailing-one signs and level storage, including chroma DC; public residual/VLC helpers return zero values for nil direct-reader use and clamp malformed coefficient counts before fixed-buffer indexing. Inter 8×8-transform residuals use FFmpeg's dedicated CAVLC scan chunks before splitting the result back into the decoder's four 4×4 coefficient slots.
- `pred.InterPred16x16At` has an unclipped interior fractional-MV fast path plus horizontal-only/vertical-only fractional specializations; edge/negative coordinates still use the clipped scalar path.
- `decode.copyInterSubRect` copies integer-MV P8x8 sub-rectangles directly instead of predicting a full 16×16 block.
- `decode.fillChromaInterPred` has an interior 8×8 row-copy fast path with malformed-input guards.
- Inter residual write-back now writes luma and chroma rows directly into frame planes after the same add + clip operation, avoiding per-pixel setter calls in the hot path; residual category/coefficient bounds and frame extents are validated before writes, and inter chroma CBP is masked to its two syntax bits before CAVLC chroma residual consumption. CABAC chroma DC residuals use a static identity scan table, and CABAC 8×8 residuals mirror FFmpeg by storing the decoded 8×8 coefficient count into all four covered 4×4 non-zero-context slots and by splitting/joining the 8×8 coefficient matrix through true 4×4 quadrants instead of contiguous 16-coefficient chunks.
- Inter chroma prediction now follows luma partition boundaries for P16x8, P8x16, and P8x8 macroblocks, including P8x8 8×4/4×8/4×4 sub-partition MVs at 4:2:0 scale.
- MV/ref caches are the single source of motion-prediction context in the decoder and trace tooling. Cache reads/fills and CABAC MV/ref context helpers reject malformed strides, short slices, and negative origins instead of panicking in direct helper/tool use. CABAC B-slice motion now keeps separate L0/L1 MVD context caches, writes B_8x8 direct sub-MB motion back into both list caches, routes all two-part B MB types through shape-derived 16×8/8×16 directional MVP helpers, and writes P16x8 top-part motion into cache before predicting the bottom part. CABAC MVD context cache write-back mirrors FFmpeg by keeping the full signed MVD for reconstruction while storing `min(abs(mvd),70)` for future context selection.
- QP updates are centralized through a wraparound helper so both decoder and `trace264` normalize arbitrary signed deltas consistently; SPS/PPS scaling-list wraparound uses the same defensive modulo style for malformed deltas.
- Inter zero-residual paths copy prediction directly: uncoded luma CBP groups, zero-`TotalCoeff` 4×4 blocks, all-zero 8×8 transform groups, chroma CBP=0, and zero chroma 4×4 residual blocks.
- Intra reconstruction diagnostics can emit luma Intra_8x8 prediction/residual/output and pre-IDCT coefficient checksums, plus chroma prediction/residual/output checksums. Chroma intra DC now follows FFmpeg's quadrant predictors (`pred8x8_dc`, `pred8x8_left_dc`, `pred8x8_top_dc`) instead of a single-block average, closing the canonical CABAC fixture's chroma planes to near-pixel parity.
- Slice/PPS parsing now consumes POC deltas, non-reference-slice ref-marking absence, SP/SI-only fields, weighted-prediction tables, reference-list modification operands, deblocking idc/offsets, PPS slice-group-map syntax, and FMO `slice_group_change_cycle` bits so later CABAC init, QP, deblock, and High-profile PPS fields stay bit-aligned even where weighted prediction/FMO reconstruction itself is not yet implemented. SPS cropped-dimension math uses wider intermediates and saturates malformed over-cropping instead of wrapping. B-slice CAVLC syntax now consumes table-driven sub-MB list use, truncated-Golomb ref_idx, sub-partition MVD counts, direct/intra/residual payloads, and clamps malformed TE ref indices; B skip runs are also applied in the decode branch. CABAC slice data is byte-aligned before arithmetic decoder initialization, matching FFmpeg.
- I_PCM macroblocks now consume aligned raw 8-bit 4:2:0 luma/chroma samples and reconstruct them directly, avoiding slice desynchronization after PCM payloads. The CABAC path also resets arithmetic state after raw I_PCM bytes, matching FFmpeg; raw sample writes are extent-guarded for direct helper use.
- Frame and reconstruction helper boundaries are guarded for direct tests/tools: `SafePixelY` and 4×4 block helpers validate malformed frame storage, while intra/inter/B reconstruction guards nil frames/macroblocks, invalid references, malformed B prediction rectangles/reference storage, chroma intra plane storage, and out-of-frame macroblock coordinates instead of panicking.
- `transform.IDCT4x4BatchMask` skips IDCT for known-zero dense residual slots.
- `transform.Dequant4x4` uses precomputed per-QP/per-position scales; `Dequant4x4Block` serves fixed-size hot decode blocks; public `Quant4x4`/`Dequant4x4` helpers defensively handle short blocks and invalid QP values.
- SIMD/scalar parity gates cover intra prediction wrappers, inter-copy wrappers, SAD, DCT4x4, IDCT4x4, IDCT8x8, and DCT8x8 fallback behavior.
- `transform.IDCT4x4Batch` is now an integration seam for future true batched AVX2/NEON kernels.
- `unsafe.Slice` scalar fallback wrappers have nil/stride guards for unsupported/non-native architecture paths.

Current CPU candidates for the next SIMD/low-level pass:

1. Re-profile after current no-EPB bit-reader and CAVLC prefix fast paths; only keep further bit IO changes if they beat the ~44-52 ms/op sample range
2. True batched AVX2/NEON kernels for IDCT/dequant where profiles justify assembly
3. Remaining motion-compensation variants not yet covered by row-copy/interior/sub-rect fast paths
4. Deblocking SIMD once reconstruction parity remains stable
5. Decoder allocation cleanup beyond expected frame buffers/slice state

## Table generation

Large VLC/context tables are checked in as generated Go. The generator commands normalize the table files and make accidental hand edits visible:

```bash
go generate ./entropy/cabac ./entropy/cavlc
```

Generators live in `internal/tables/` and are marked with `//go:build ignore` so they are run explicitly by `go generate` and not compiled into normal packages.

## Known gaps / tracked work

- CABAC P-slice syntax now covers intra-in-P, skip/ref/MVD neighbour contexts, P8x8 sub-MB types and transform-size eligibility, chroma prediction mode contexts, `mb_qp_delta` context state, luma/chroma DC coded-block contexts, chroma DC/AC placement, residual category bounds, FFmpeg-style MVD context-cache clamping, FFmpeg-style 8×8 residual layout/non-zero-context write-back, CABAC I_PCM payload/reset handling, inter transform-size-before-DQP ordering, and byte-aligned arithmetic initialization. First-frame MB syntax summaries now match FFmpeg on the active `testsrc_cabac_p` and `bbb_annexb` fixtures, but Main/High frame reconstruction still remains below the correctness gate. CABAC residual/ref_idx/MVD/arithmetic helper boundaries are guarded against malformed direct use.
- High-profile CABAC intra `transform_size_8x8_flag` is now consumed in normal decode after FFmpeg first-divergence proof. Remaining I8x8 work is reconstruction parity, not flag consumption. Rejected candidates include simple 8×8 IDCT rounding changes, simple/global ¼ dequant scaling, blindly transposing the CABAC 8×8 scan table, residual handoff transposes, and blanket FFmpeg storage/qmul paths because each regressed the hard `bbb` gate.
- SIMD acceleration is in incremental integration: parity/fallback gates are present, an IDCT4x4 batch seam exists, and current work is focused on measured hot paths rather than speculative assembly.
- Weighted prediction and FMO slice-group reconstruction are not implemented yet; the parser now consumes their syntax, including changing-FMO `slice_group_change_cycle`, plus reference marking/list-modification, POC, SP/SI, and deblocking fields, to keep subsequent fields aligned. B-slice list-use/sub-partition decisions are table-driven from FFmpeg/H.264, B ref_idx syntax uses clamped TE decoding, B skip runs are applied in decode, and B intra/residual payloads are consumed and exposed to trace/decode paths, though B reconstruction remains much less mature than Baseline/P-slice paths.
- Encoder API, rate control, and full x264-like encode pipeline are planned but not yet implemented.
- GPU work is experimental scaffolding only.

## License

MIT
