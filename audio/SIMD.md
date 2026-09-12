# Audio SIMD coverage

The first extended audio SIMD increment targets AAC filterbank synthesis on amd64. The requirement to vectorise timing-critical code is still open: this document lists implemented kernels and remaining work instead of treating assembly names as proof of SIMD coverage.

## Implemented

| Kernel | amd64 | Other architectures / `purego` | Correctness |
|---|---|---|---|
| Contiguous FIR products | SSE2, two products then original-order scalar accumulation | Go reference | Bit-exact, alignment/tail/guard-page tests |
| AAC FFT butterfly stages | SSE2 packed real/imaginary products and add/subtract | Go reference | Bit-exact stage comparisons; signed zero/subnormal, every stage, roots unchanged, guarded loads |
| AAC forward/reverse window multiply and windowed overlap | SSE2, two independent float64 lanes | Go reference | Bit-exact, odd tails, 8-byte alignment, input immutability, guard pages |
| AAC frame overlap addition | SSE2, two independent float64 lanes | Go reference | Bit-exact, exact alias and guard-page tests |
| IMDCT pre/post rotations | SSE2 packed complex products / independent output lanes | Go reference | Signed-zero/subnormal, tails/guards and 64-frame state hash parity |
| AAC band dequantisation scaling | Exact lookup plus paired SSE2 products | Same lookup plus Go products | Every signed magnitude × 256 scale values matches the original formula; range checked before dispatch |
| PCM float64 → S16 | SSE2 clipped truncation/fraction comparison with explicit sign restoration | Go `math.Round` reference | Every S16 tie and its neighbouring float values, random finite bit patterns, guard/tail tests, unchanged destination on non-finite input |
| Stereo mix, mono duplication, planar interleave | SSE2 independent lanes | Go reference | Exact layout/rounding, signed-zero/subnormal, odd-tail/alignment/guard tests |

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

## Rotation/dequantisation increment

A second coordinated window, `go264-audio-simd-1310` (2026-09-12 13:05:03.780–13:05:11.666 UTC, affinity CPUs 0/1), compared the preceding FFT/window implementation against added rotations and dequantisation. Two-second synthetic AAC source-rate decode improved from a mean of 10.758 ms to 9.864 ms (1.09×); canonical output improved from 13.273 ms to 12.271 ms (1.08×). The ordering was A1, B1/B2, A2. Each short sample is diagnostic; unrelated host isolation was not attested.

Rotation median: scalar 2.798 µs, SIMD 1.654 µs (1.69×). Dequantisation first caches the exact `abs(q)*Cbrt(abs(q))` values for the bounded signed coefficient domain in a 131,064-byte immutable table. This is an algorithmic reuse gain, not SIMD attribution. For a 1,024-value band, direct formula/table-scalar/table-SIMD medians were 8.633 µs / 639.2 ns / 458.1 ns; packed scaling contributes 1.39× over the table-scalar path. The exhaustive magnitude/scale test verifies bitwise output including signs. Initialization work and retained table memory are separate from steady-state benchmark allocations.

Default and `purego` audio tests/vet, ARM64/386 builds, protected-page tests, synthetic FFmpeg PCM/stereo/trim/seek oracles and byte-identical tone/noise/transient PCM across the preceding commit, new SIMD and new `purego` builds pass. No public speech/model work was repeated.

## PCM numeric increment

Window `go264-pcm-simd-1340` measured local PCM kernels on the same host, affinity CPUs0/1, two50ms samples: 2048-value S16 conversion 5.0445→2.227µs, stereo-to-mono 1.842→0.7119µs, mono duplication 1.5665→0.7359µs, planar interleave 2.009→0.6714µs. Kernel allocations remain zero. These are raw numeric kernels; public S16 finiteness validation remains a separate pass and must finish before any write.

Packed S16 conversion truncates the absolute clipped sample, compares its exact fractional residual with0.5, increments and restores sign. It does **not** use input-plus0.5, which can misround floating-point values immediately below a tie. Existing ties-away-from-zero semantics and non-finite rollback are exact. Synthetic full-codec A1/B1/B2/A2 timings showed only about0.5% canonical benefit, within short-run noise; no larger decoder speedup is attributed to these kernels.

## Allocations

The rotation/dequant baseline allocated 548–549 kB/op and 1,520–1,521 objects per two-second synthetic clip. A full-rate allocation profile attributed most object churn to section slices, temporary group offsets, copied band-offset tables and per-packet `SectionReader` values. The next allocation increment replaces those with bounded frame-owned arrays/compact section metadata, stack scratch and checked direct ReaderAt loops. Returned channels never alias shared offset tables. Common mono/stereo/grouped-short parser paths now have a zero-allocation test.

The resampler now owns one exact-sized coefficient/ring/scratch allocation for rate changes (2048/4096 ring frames, channels accounted for), and no DSP storage for same-rate pass-through. Source metadata is cached under the immutable-source contract; two copies handle ring wrap. Power-of-two masks avoid runtime division. No pool or global mutable scratch was added.

Coordinated `go264-alloc-refine-1330` A1/B1/B2/A2 samples report source-rate allocation **547,960→130,392 B/op, 1520→190 objects** and canonical **549,240→157,656 B/op, 1521→191 objects**. Source-rate times were approximately 9.770→9.683 ms and canonical 12.223→12.027 ms; the small timing gains are diagnostic, while allocation counts are deterministic on these fixtures. The first allocation candidate regressed canonical time about4%; compacting section entries from24 to3bytes and mask-based ring addressing removed that regression. Failed candidate evidence is retained. Bounded same-rate storage and all scalar/SIMD PCM bytes, seeks, chunking, cancellation and oracle gates pass.

Follow [goperf.dev escape-analysis guidance](https://goperf.dev/01-common-patterns/stack-alloc/) and [known-size preallocation guidance](https://goperf.dev/01-common-patterns/mem-prealloc/), backed by exact `alloc_space` / `alloc_objects` profiles and `-benchmem`, before choosing reuse changes. Keep scratch owned by a decoder, respect transactional decode/alias lifetimes, and report retained memory separately. Do not introduce unbounded caches or indiscriminate pools.

## Remaining timing-critical work

- Refresh whole-decode profiling after each increment. The first SIMD profile still showed FFT stages and reconstruction as numeric hotspots; the allocation profile after parser cleanup shifted towards MP4 metadata and decoder buffers. The original pre-optimisation 41.88% resampler/25.64% Huffman profile is historical only.
- IMDCT bit-reversal remains Go; it is indexed movement rather than regular packed arithmetic. FFT layout/batching may further reduce overhead, subject to exact arithmetic order.
- WAV integer unpacking/normalisation remains Go; PCM output quantisation and layout/mix now use SSE2. Finite validation is still Go and preserves transactional output semantics. Measure before changing validation or unpacking.
- AAC M/S and intensity stereo, TNS and PNS need fresh numeric-kernel profiles. TNS/PNS contain dependencies that limit naive across-sample SIMD; keep reference ordering.
- ARM64 audio SIMD is not implemented. The scalar fallback builds on ARM64 and 386. Do not describe fallback execution as vectorised.
- Huffman/bit parsing, checked container metadata, seek/replay orchestration, cancellation and filesystem operations remain scalar. SIMD is appropriate only for a measured batchable sub-operation; replacing a function with scalar assembly is not SIMD.

Video coverage is tracked separately in the root plan: some historical transform entry points have AVX2/NEON names but use scalar registers, so they require actual instruction-level audit and implementation before claiming vector coverage. Existing SAD16x16 and prediction-copy/fill kernels do use vector instructions. Deblocking, smaller SAD/SATD and motion interpolation need measured inventory.
