# go-264

H.264/AVC decoder-first implementation in pure Go with assembly acceleration hooks for amd64/arm64 and optional GPU experiments.

The current focus is a correct, inspectable decoder core. Encoder work remains planned; the implemented code is already useful for Annex B parsing, H.264 syntax tracing, CAVLC/CABAC experimentation, frame reconstruction, PSNR regression testing, and SIMD kernel development.

## Current status

| Area | Status | Notes |
|---|---:|---|
| Annex B / NAL parsing | ✅ | Start-code scan, emulation-prevention removal, SPS/PPS parsing including PPS slice-group-map skipping and scaling-list guardrails |
| Bitstream reader | ✅ | Fixed-width, Exp-Golomb, fuzz-tested |
| Slice syntax | ✅ | I/P/B header parsing, weighted-prediction table skipping, I_PCM sample consumption; macroblock syntax lives in `syntax/` |
| CAVLC | ✅ | Baseline CAVLC decode is the current hard-gated completion point |
| CABAC | 🔶 | Real CABAC contexts, P-slice/intra-in-P syntax, residuals, CBP/DQP/ref/MVD; I8x8 flag still quality-gated |
| Intra prediction | ✅ | I4x4, I8x8, I16x16; I8x8 strong reference filter implemented |
| Inter prediction | ✅ | P skip, P16x16/P16x8/P8x16/P8x8, 4×4 MV/ref cache write-back, partition-aware chroma prediction |
| Transforms | ✅ | 4×4 and 8×8 integer transforms with scalar + assembly dispatch hooks |
| Deblocking | ✅ | Scalar correctness tests; SIMD deblock is planned |
| Frame / DPB | ✅ | YUV420 frame storage, safe pixel reads, reference frame tracking |
| Validation | ✅ | Syntax parity, FFmpeg/YUV PSNR gates, fuzz/unit tests |
| Encoder | ⬜ | Planned after decoder/SIMD gates |

Legend: ✅ implemented · 🔶 partial/quality-gated · ⬜ planned

## Decoder quality gates

Current regression gates are intentionally conservative guards, not a claim of full H.264 conformance:

- Syntax parity: per-slice/MB trace comparison tooling against FFmpeg-derived references
- Motion parity: representative macroblock MV checks against FFmpeg motion-vector side data
- Reconstruction parity: frame PSNR and max-diff checks against FFmpeg YUV output
- Baseline CAVLC: marked complete
- CABAC: functional but still gated on I8x8 `transform_size_8x8_flag` quality

Recent reference values:

| Fixture | Metric |
|---|---:|
| `dark64` | 31.23 dB avg PSNR |
| Baseline CAVLC | 27.65 dB avg PSNR |
| Baseline YUV | Y=39.58 U=26.64 V=20.33 dB |
| `bbb-frame0` CABAC | 7.92 dB avg PSNR |

## Package layout

```text
go-264/
├── nal/              Annex B, NAL units, SPS/PPS, bit reader
├── frame/            YUV420 frames, DPB helpers, ChromaQP, SafePixelY
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
    ├── trace264      CAVLC MB trace tool; rejects CABAC streams loudly for now
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

### Trace CAVLC macroblock syntax

```bash
go build -o /workspace/tmp/trace264 ./cmd/trace264
/workspace/tmp/trace264 -i input_baseline.h264 -limit 64
```

`trace264` is a CAVLC syntax tracer. CABAC streams are rejected explicitly until MB-level CABAC tracing is wired to the decode path; this avoids misleading CAVLC traces for Main/High streams. Its P-slice QP and MV-prediction bookkeeping is kept aligned with the decoder so CAVLC diagnostic output uses the same wraparound and 4×4 MV/ref cache semantics.

### Compare against FFmpeg-derived frame metadata

```bash
go run ./cmd/trace264cmp -i input.h264 -v
```

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
- CAVLC residual decode uses fixed stack arrays for trailing-one signs and level storage.
- `pred.InterPred16x16At` has an unclipped interior fractional-MV fast path plus horizontal-only/vertical-only fractional specializations; edge/negative coordinates still use the clipped scalar path.
- `decode.copyInterSubRect` copies integer-MV P8x8 sub-rectangles directly instead of predicting a full 16×16 block.
- `decode.fillChromaInterPred` has an interior 8×8 row-copy fast path with malformed-input guards.
- Inter residual write-back now writes luma and chroma rows directly into frame planes after the same add + clip operation, avoiding per-pixel setter calls in the hot path; residual category/coefficient bounds and CABAC 8×8 per-4×4 non-zero counts are validated before writes.
- Inter chroma prediction now follows luma partition boundaries for P16x8, P8x16, and P8x8 macroblocks, including P8x8 8×4/4×8/4×4 sub-partition MVs at 4:2:0 scale.
- MV/ref caches are the single source of motion-prediction context in the decoder and trace tooling. Cache reads/fills and CABAC MV/ref context helpers reject malformed strides, short slices, and negative origins instead of panicking in direct helper/tool use.
- QP updates are centralized through a wraparound helper so both decoder and `trace264` normalize arbitrary signed deltas consistently; SPS/PPS scaling-list wraparound uses the same defensive modulo style for malformed deltas.
- Inter zero-residual paths copy prediction directly: uncoded luma CBP groups, zero-`TotalCoeff` 4×4 blocks, all-zero 8×8 transform groups, chroma CBP=0, and zero chroma 4×4 residual blocks.
- Slice/PPS parsing now consumes weighted-prediction tables and slice-group-map syntax so later CABAC init, QP, deblock, and High-profile PPS fields stay bit-aligned even where weighted prediction/FMO reconstruction itself is not yet implemented.
- I_PCM macroblocks now consume aligned raw 8-bit 4:2:0 luma/chroma samples and reconstruct them directly, avoiding slice desynchronization after PCM payloads.
- Intra/inter/B reconstruction use fixed stack prediction buffers for 16×16 temporaries.
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

- CABAC P-slice syntax now covers intra-in-P, skip/ref/MVD neighbour contexts, P8x8 sub-MB types, chroma DC/AC placement, residual category bounds, per-4×4 non-zero counts for 8×8 residual quadrants, and transform-size context selection, but Main/High frame quality remains below the correctness gate. CABAC residual/ref_idx/MVD helper boundaries are guarded against malformed direct use.
- CABAC I8x8 `transform_size_8x8_flag` decode is intentionally guarded by `enableCABACI8x8Transform=false` because consuming the flag currently lowers BBB CABAC quality; it remains gated on better I8x8 neighbour-mode inference / reconstruction parity.
- SIMD acceleration is in incremental integration: parity/fallback gates are present, an IDCT4x4 batch seam exists, and current work is focused on measured hot paths rather than speculative assembly.
- Weighted prediction and FMO slice-group reconstruction are not implemented yet; the parser now consumes their syntax to keep subsequent fields aligned.
- Encoder API, rate control, and full x264-like encode pipeline are planned but not yet implemented.
- GPU work is experimental scaffolding only.

## License

MIT
