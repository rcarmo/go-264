# Video SIMD coverage

This inventory describes the SIMD paths on `master` at `76a23d9`. Decoder correctness still requires the pinned 300-frame FFmpeg gate in `PLAN.md`; that fixture is absent on the current host. The checked-in four-frame regression, retained diagnostic stream and primitive tests provide narrower exact-output evidence.

## Dispatch and arithmetic

New amd64 kernels require only baseline SSE2. They do not depend on the historical `HasAVX2` flag. `purego` bypasses these new kernels. No cgo, new dependency, shared pool or floating-point approximation is used.

| Operation | amd64 implementation | Remaining scope |
|---|---|---|
| 4x4 forward/inverse transform | amd64 packed signed32 SSE2; ARM64 forward uses signed16 NEON and inverse uses explicit signed32 widening, both with packed transposes | Native ARM64 timing remains open |
| 8x8 inverse transform | Packed SSE2 butterflies, word/dword/qword transposes | Forward8 remains Go; ARM64 remains unvectorised |
| Inverse scaling4/8 | Packed products; exact wrapping and rounding | Other architectures use Go |
| SAD4/8 | amd64 `PSADBW`; ARM64 exact-width 32/64-bit lane loads then `VUMAX/VUMIN/VSUB/VUADDLV` | SATD remains scalar; native ARM64 timing open |
| SAD16 | amd64 `PSADBW`; ARM64 `VUMAX/VUMIN/VSUB` plus widened horizontal reduction | Native ARM64 timing remains open |
| Integer luma prediction | Interior Go copy; clamped edges | No interpolation arithmetic involved |
| Luma H/V | Eight signed16 six-tap lanes, arithmetic shift, unsigned byte saturation | Fast path limited to <=16x16 blocks; other shapes/aliases use scalar |
| Chroma bilinear | amd64 SSE2 eight pixels/row, separable word products with exact `(v+32)>>6` | Interior fractional8x8 only; edges/aliases/purego/other architectures use scalar |
| Luma HV | Four signed32 lanes over unrounded signed16 H sums | Same bounded fast-path scope |
| Quarter-pel average | `PAVGB` with exact-width stores and scalar tail | Scalar fallback retains sequential alias semantics |
| Intra copy/fill | Existing packed copy/fill paths | Remaining directional predictors need profiles |
| B-frame blending | amd64 SSE2 and ARM64 NEON equal and weighted byte blends | Scalar fallback covers partial overlap and `purego` |
| Deblocking pixels | amd64 SSE2 vertical lanes and bounded horizontal staging | ARM64 and boundary-strength classification remain scalar |
| Residual add/store | amd64 SSE2 and ARM64 NEON 4x4/8x8 kernels | Scalar fallback retains sparse, alias and `purego` semantics |
| Weighted P prediction | Scalar | Profile share remains below the accepted SIMD work |
| CABAC arithmetic decoding | Sequential Go state machine | Kept scalar because every bin mutates decoder state |

Six-tap coefficients are `[1,-5,20,20,-5,1]`. Horizontal sums fit signed16 (`-2550..10710`). Diagonal filtering widens these unrounded values to signed32 before `(v+512)>>10`; directional halves use `(v+16)>>5`. Both are clipped to bytes before quarter-pel averaging. This preserves scalar rounding and clipping order.

The luma fast path copies clamped reference pixels into bounded padded scratch before SIMD access. Its five local arrays total 2,024 bytes. Partial widths compute padded lanes but write only the requested pixels. Source/output overlap dispatches to the original scalar implementation because writes can affect later reads. Invalid or unusual input shapes keep existing scalar behaviour; this change does not promise newly defined malformed-input semantics.

## Verification

- Full-range transform comparison against legacy arithmetic, scalar references and purego; protected-page and unaligned buffers.
- All 52 quantisation parameters and full-range coefficients for inverse scaling.
- Exact-width SAD loads/stores and bounds guards.
- 102,400 luma scalar comparisons: all16 fractional positions, positive/negative vectors, eight edge/interior base locations, ten block sizes, four reference strides and five constant/extreme/random patterns.
- Additional luma alias, destination padding, rejected-input, protected-page and zero-allocation tests.
- Default/purego retained decode produces YUV SHA256 `54bdddd49d3ec6f13f6147abb300f1d96e3e0159944cc7142800ad667cb3944b`.
- CABAC allocation cleanup and stream-level trace-flag snapshotting preserve 33,421 trace lines exactly; ordinary residual decode and L1 selection have zero-allocation tests.
- B-slice temporal List 0 is constructed once per slice and reused by macroblock consumers; no pool or shared mutable cache was added.

ARM64 QEMU execution covers selected transform, PCM, filterbank, prediction, B-blend and residual-store tests plus exact retained pixels. The SAD16 test covers 40,000 random full-range blocks, four stride pairs, legacy zero-stride fallback and both protected-page edges; disassembly confirms `VUMAX`, `VUMIN`, `VSUB` and `VUADDLV`. Small SAD4/8 uses NEON with exact-width lane loads and matches 60,000 random blocks plus both-edge guards. ARM64 forward/inverse4 uses packed NEON in both passes and matches 20,000 full-range cases plus offset and guard-page blocks. Inverse4 widens signed16 input, emulates arithmetic shifts, adds the required 32 rounding bias and narrows modulo int16 between passes. The 8x8 transform entry points still use Go fallbacks. QEMU establishes functional parity, not native ARM64 performance.

## Scoped performance evidence

Intel i5-1340P, Go1.26.2, CGO0, CPU0/1, max2CPU. Timings come from short explicitly coordinated synthetic windows, not hostwide isolation. Raw logs are in workspace report `reports/go264-simd-20260912/`.

- Inverse8 packed transpose:79.45→47.05ns. Rejected scalar-transpose candidate:117ns.
- Dequant4/8:10.41→4.24ns and65.05→10.95ns. SAD4/8:16.8→5.82ns and50.5→6.99ns.
- Trace-disabled allocation cleanup:5,177→565allocs,1.52MB→668KB, retained clip2.93→2.75ms.
- Luma interpolation: all18 measured shape/mode pairs faster.16x16 HV:3404→373.4ns; quarterHV:30958→400.7ns. The latter also avoids recomputing the H rows for each output pixel, so its gain is not attributed solely to SIMD.
- Luma whole-clip ABBA window1455: baseline mean3.357ms, candidate3.209ms (~4.4% lower),566allocs/668KB unchanged in that invocation.
- Chroma fractional8x8 SSE2 window1550:17.07ns median, zero allocations. Retained decode A/B/B/A baseline mean2.614ms, candidate2.570ms (~1.7% lower), allocation count unchanged apart from one-sample565/566 noise. The refreshed candidate CPU profile still attributes only~2.0% flat to chroma fill and~0.7% to the packed kernel. Earlier window1450 decode runs skipped due to a wrong cwd and are not performance evidence; its primitive timings are valid.
- On the diagnostic 300-frame `b115b066…bc94a` stream, the pre-snapshot CPU profile attributed 13.82% cumulative to `syscall.Getenv`; `GO264_REF_LIST_TRACE` lookup alone accounted for about100ms cumulative. Window1850 A/B/B/A measured `94084e6` at1.515678s mean and `1369a5c` at1.468102s mean (~3.14% lower). The trace flags now refresh once per top-level decode/CABAC reset, including zero-value CABAC use.
- The normal-rate `alloc_objects` profile at `1369a5c` attributed360,453 of724,718 sampled objects (49.74%) to repeated `bidiL0FramesWithMods`; `alloc_space` was dominated by retained picture/frame storage and macroblock result objects. Commit `a66b319` hoists the immutable modified List0 to slice scope. Window1850 B/C/C/B measured allocations767,900.5→361,701 (-52.90%), bytes873,724,408→867,732,420 (-0.69%), and time1.457779s→1.439494s (~1.25%).

The 300-frame stream above is diagnostic. Its Go output matches its retained FFmpeg reference, but its hash is not the pinned `1305bc99…841ff` fixture. The retained source and available toolchain do not reproduce that hash, so these measurements do not pass the historical regression gate.

The comparable final matrix at `76a23d9` uses the same diagnostic `b115b066…bc94a` stream as the original profile. Unprofiled decode time fell from 1.449 s to 1.185 s (18.2%), allocated space from 867.7 MB to 492.2 MB, and allocations from 361,706 to 20,046. Final CPU profiles place boundary-strength calculation, reference ordering and sequential CABAC above the accepted pixel kernels. Profile cumulative percentages and flat percentages are different quantities; profile-instrumented timings are not speed evidence.
