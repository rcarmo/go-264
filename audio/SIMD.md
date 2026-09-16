# Audio SIMD coverage

This file records implemented audio SIMD paths and their exactness constraints. Assembly entry points count as SIMD only when disassembly confirms packed instructions and scalar or `purego` output remains exact.

## Implemented

| Kernel | amd64 | Other architectures / `purego` | Correctness |
|---|---|---|---|
| Contiguous FIR products | SSE2 issues two product vectors; optional AVX2 issues four products; both accumulate lanes in source order | Go reference | Bit-exact, alignment/tail/guard-page and forced-fallback tests |
| AAC FFT butterfly stages | SSE2 plus CPUID/OSXSAVE/XGETBV-gated AVX2 dual butterflies | ARM64 exact-order FFT; Go under `purego` | Bit-exact stage comparisons; signed zero/subnormal, every stage, roots unchanged, guarded loads |
| AAC forward/reverse window multiply and windowed overlap | amd64 SSE2; ARM64 NEON for overwrite multiplication only | Go reference for ARM64 add mode and `purego` | Bit-exact, odd tails, 8-byte alignment, input immutability, guard pages |
| AAC frame overlap addition | amd64 SSE2 | Go reference on ARM64/`purego` | Bit-exact, exact alias and guard-page tests; ARM64 vector add candidate rejected |
| AC-3 overlap-add | amd64 SSE2 | ARM64 NEON; Go under `purego` | Bit-exact scalar parity, aliases, odd tails and protected-page tests |
| IMDCT pre/post rotations | SSE2 packed complex products / independent output lanes | Go reference | Signed-zero/subnormal, tails/guards and 64-frame state hash parity |
| AAC band dequantisation scaling | Exact lookup plus paired SSE2 products | ARM64 scalar gathers plus paired NEON FMUL; Go products under `purego` | Every signed magnitude × 256 scale values matches the original formula; range checked before dispatch; guard pages |
| PCM finite validation and float64 → S16 | SSE2 read-only validation plus gated AVX2 four-lane conversion; SSE2 tail | Go `math.Round` reference | Every S16 tie and neighbour, finite bit classes, guard/tail tests, unchanged destination on non-finite input |
| Stereo mix, mono duplication, planar interleave | SSE2 independent lanes | ARM64 NEON bit-copy duplication/interleave; Go stereo average and `purego` reference | Exact layout/rounding, signed-zero/subnormal, odd-tail/alignment/guard tests |
| AAC M/S, intensity and PNS gain | SSE2 paired independent products/add/subtract | Go reference | Exact state/PCM/oracle parity, boundaries/guards |
| AAC TNS feedback products | SSE2 coefficient/history registers; scalar-ordered subtraction across lanes | Go reference | Orders1–12, both directions, tails/guards; sample dependency stays sequential |

Baseline amd64 paths require SSE2. Optional AVX2 paths check CPUID, OSXSAVE and XCR0 XMM/YMM state before executing YMM instructions. No FMA or cgo dependency is used. Multiplication and addition remain separate; FFT, overlap and FIR accumulation order are preserved. Internal kernels receive validated lengths and geometry. The public filterbank validates coefficients, output and state before committing SIMD results.

The window refactor hoists sequence branches out of per-sample loops and uses Go `copy` for flat sections. That structural improvement applies to both scalar and SIMD builds. The synthesis comparison below uses the same refactored code in the SIMD and `purego` builds. It does not compare against the pre-change release.

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

Packed S16 conversion truncates the absolute clipped sample, compares its exact fractional residual with 0.5, increments and restores sign. Input-plus-0.5 can misround floating-point values immediately below a tie, so the implementation does not use it. Existing ties-away-from-zero semantics and non-finite rollback are exact. Synthetic full-codec A1/B1/B2/A2 timings showed about 0.5% canonical benefit, within short-run noise. No larger decoder speedup is attributed to these kernels.

## AAC reconstruction bands

M/S conversion, intensity stereo and noise-band normalisation now use paired SSE2 lanes. Intensity gain calculation is hoisted from each coefficient to its band; that reuse applies to both scalar and SIMD builds. PNS random generation and its energy accumulation remain sequential to preserve the specified PRNG/output order.

TNS coefficient/history pairs stay in registers for orders 1–12. Packed products are subtracted low then high in the original order; the filter's cross-sample recurrence remains sequential. An initial memory-shift SIMD loop regressed orders 4 and 12 and was rejected. In coordinated window `go264-tns-register-1350`, the register version measured 377→212 ns for order 1, 609→457 ns for order 4 and 1,704→1,000 ns for order 12 over 128 samples. Full-codec timings were near-flat for canonical output and about 2% faster at source rate. This is not a general TNS corpus result. All default, `purego`, oracle and baseline-PCM checks pass; the report retains rejected timings and source.

## Allocations

The rotation/dequant baseline allocated 548–549 kB/op and 1,520–1,521 objects per two-second synthetic clip. A full-rate allocation profile attributed most object churn to section slices, temporary group offsets, copied band-offset tables and per-packet `SectionReader` values. The next allocation increment replaces those with bounded frame-owned arrays/compact section metadata, stack scratch and checked direct ReaderAt loops. Returned channels never alias shared offset tables. Common mono/stereo/grouped-short parser paths now have a zero-allocation test.

The resampler now owns one exact-sized coefficient, ring and scratch allocation for rate changes (2,048/4,096 ring frames, with channels included), and no DSP storage for same-rate pass-through. Source metadata is cached under the immutable-source contract; two copies handle ring wrap. Power-of-two masks avoid runtime division. No pool or global mutable scratch was added.

Coordinated `go264-alloc-refine-1330` A1/B1/B2/A2 samples report source-rate allocation **547,960→130,392 B/op, 1520→190 objects** and canonical **549,240→157,656 B/op, 1521→191 objects**. Source-rate times were approximately 9.770→9.683 ms and canonical 12.223→12.027 ms; the small timing gains are diagnostic, while allocation counts are deterministic on these fixtures. The first allocation candidate regressed canonical time by about 4%. Compacting section entries from 24 to 3 bytes and using mask-based ring addressing removed that regression. Failed candidate evidence is retained. Bounded same-rate storage and all scalar/SIMD PCM bytes, seeks, chunking, cancellation and oracle gates pass.

Follow [goperf.dev escape-analysis guidance](https://goperf.dev/01-common-patterns/stack-alloc/) and [known-size preallocation guidance](https://goperf.dev/01-common-patterns/mem-prealloc/), backed by exact `alloc_space` / `alloc_objects` profiles and `-benchmem`, before choosing reuse changes. Keep scratch owned by a decoder, respect transactional decode/alias lifetimes, and report retained memory separately. Do not introduce unbounded caches or indiscriminate pools.

## AC-3 overlap-add

The AC-3 decoder uses packed overlap-add after its inverse transform. On the development i5-1340P, the 256-sample amd64 kernel measured about 154 ns in scalar code and 68 ns with SSE2. The kernel allocates no memory. Exact scalar parity, source/destination aliases, odd tails and both protected-page edges pass. Native ARM64 CI executes the NEON path; QEMU checks the same exact-output and bounds cases. These kernel measurements do not establish whole-file speedup.

## Remaining timing-critical work

- Final whole-decode profiles place `fftStageAVX2` first for AAC source output and ordered `dotAVX2` first for canonical output. Further changes need a new exact formulation, not reassociated sums or FMA.
- PNS PRNG and energy accumulation, TNS reflection-coefficient setup, Huffman tree traversal, checked container metadata, seek/replay orchestration, cancellation and filesystem operations remain scalar.
- ARM64 mono duplication and planar stereo interleave use `VZIP1`/`VZIP2`. AAC overwrite-window multiplication and FFT stages use NEON where exact. Vector overlap addition and pre/post rotations were rejected because QEMU exposed subnormal, signed-zero or rounded-sum differences. Float-to-S16 and stereo averaging remain Go on ARM64. QEMU establishes functional parity, not native performance.
- WAV PCM8/16/32 unpack uses SSE2 on amd64; PCM24 stays scalar because the byte-shuffle candidate did not justify its complexity and exactness risk.

Video coverage is tracked in `../docs/video-simd.md`.
