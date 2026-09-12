# Audio SIMD coverage

The first extended audio SIMD increment targets AAC filterbank synthesis on amd64. The requirement to vectorise timing-critical code is still open: this document lists implemented kernels and remaining work instead of treating assembly names as proof of SIMD coverage.

## Implemented

| Kernel | amd64 | Other architectures / `purego` | Correctness |
|---|---|---|---|
| Contiguous FIR products | SSE2, two products then original-order scalar accumulation | Go reference | Bit-exact, alignment/tail/guard-page tests |
| AAC FFT butterfly stages | SSE2 packed real/imaginary products and add/subtract | Go reference | Bit-exact stage comparisons; signed zero/subnormal, every stage, roots unchanged, guarded loads |
| AAC forward/reverse window multiply and windowed overlap | SSE2, two independent float64 lanes | Go reference | Bit-exact, odd tails, 8-byte alignment, input immutability, guard pages |
| AAC frame overlap addition | SSE2, two independent float64 lanes | Go reference | Bit-exact, exact alias and guard-page tests |

These kernels need only baseline SSE2 on amd64. No AVX/FMA dispatch or cgo dependency is added. Multiplication and addition remain separate; FFT and overlap accumulation order are preserved. Kernels are internal and receive validated lengths/geometry from the filterbank. The public filterbank checks coefficient finiteness and validates all output/state before committing it, including SIMD results.

The window refactor hoists sequence branches out of per-sample loops and uses Go `copy` for flat sections. That structural improvement applies to both scalar and SIMD builds. The synthesis comparison below isolates the current SIMD build from the **same refactored code** built with `purego`; it is not a before/after result against the pre-change release.

## Measured filterbank result

Coordinated synthetic-only window `go264-audio-simd-1250`, Intel Core i5-1340P, Go 1.26.2, linux/amd64, `CGO_ENABLED=0`, `GOMAXPROCS=2`. Three short 80-ms benchmark samples per case, zero allocation events per operation. Median values:

| Operation | Scalar / `purego` | SSE2 dispatch | Ratio |
|---|---:|---:|---:|
| FFT stages, 256 complex points | 1.729 µs | 1.173 µs | 1.47× |
| FFT stages, 2048 complex points | 18.678 µs | 12.629 µs | 1.48× |
| Reverse multiply-add window, 128 samples | 115.9 ns | 60.02 ns | 1.93× |
| Reverse multiply-add window, 1024 samples | 826.3 ns | 378.5 ns | 2.18× |
| Complete only-long synthesis | 32.932 µs | 25.478 µs | 1.29× |
| Complete eight-short synthesis | 28.666 µs | 22.943 µs | 1.25× |

Consult the retained raw benchmark log for exact medians; short microbenchmarks are diagnostic. The synthesis benchmark reported 2 B/op and 0 allocs/op in both arms due to one-time setup amortisation. No whole-file decode throughput, cross-machine speedup, energy benefit or model performance is established by these figures. Neither public-media nor model qualification was rerun in this timing window.

## Remaining timing-critical work

- Current whole-decode profiling after the FIR/Huffman/filterbank changes. The old profile showed 41.88% resampling, 25.64% Huffman lookup and 9.83% IMDCT, but predates earlier optimisations and must not be reused as current attribution.
- IMDCT pre/post rotations and bit-reversal remain Go. Measure their share before adding more kernels; bit-reversal is indexed movement rather than regular packed arithmetic.
- PCM conversion/channel layout and integer quantisation remain Go. Preserve rounding, clipping, signed-zero/non-finite policy and exact sample counts.
- AAC dequantisation, M/S and intensity stereo, TNS and PNS need fresh numeric-kernel profiles. TNS/PNS contain dependencies that limit naive across-sample SIMD; keep reference ordering.
- ARM64 audio SIMD is not implemented. The scalar fallback builds on ARM64 and 386. Do not describe fallback execution as vectorised.
- Huffman/bit parsing, checked container metadata, seek/replay orchestration, cancellation and filesystem operations remain scalar. SIMD is appropriate only for a measured batchable sub-operation; replacing a function with scalar assembly is not SIMD.

Video coverage is tracked separately in the root plan: some historical transform entry points have AVX2/NEON names but use scalar registers, so they require actual instruction-level audit and implementation before claiming vector coverage. Existing SAD16x16 and prediction-copy/fill kernels do use vector instructions. Deblocking, smaller SAD/SATD and motion interpolation need measured inventory.
