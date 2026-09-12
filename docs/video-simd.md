# Video SIMD coverage

This inventory describes the local optimisation tree, not the published `a47077e` module. Decoder correctness still requires the pinned 300-frame FFmpeg gate in `PLAN.md`; those assets are absent on the current host. The retained four-frame clip and primitive tests provide narrower evidence.

## Dispatch and arithmetic

New amd64 kernels require only baseline SSE2. They do not depend on the historical `HasAVX2` flag. `purego` bypasses these new kernels. No cgo, new dependency, shared pool or floating-point approximation is used.

| Operation | amd64 implementation | Remaining scope |
|---|---|---|
| 4x4 forward/inverse transform | Packed signed32 SSE2 butterflies and transposes; inverse narrows after each pass | ARM64 historical NEON names use scalar registers |
| 8x8 inverse transform | Packed SSE2 butterflies, word/dword/qword transposes | Forward8 remains Go; ARM64 remains unvectorised |
| Inverse scaling4/8 | Packed products; exact wrapping and rounding | Other architectures use Go |
| SAD4/8 | amd64 `PSADBW`, exact-width loads and extent checks | ARM64 small SAD and SATD remain scalar |
| SAD16 | amd64 `PSADBW`; ARM64 `VUMAX/VUMIN/VSUB` plus widened horizontal reduction | Native ARM64 timing remains open |
| Integer luma prediction | Interior Go copy; clamped edges | No interpolation arithmetic involved |
| Luma H/V | Eight signed16 six-tap lanes, arithmetic shift, unsigned byte saturation | Fast path limited to <=16x16 blocks; other shapes/aliases use scalar |
| Luma HV | Four signed32 lanes over unrounded signed16 H sums | Same bounded fast-path scope |
| Quarter-pel average | `PAVGB` with exact-width stores and scalar tail | Scalar fallback retains sequential alias semantics |
| Intra copy/fill | Existing packed copy/fill paths | Remaining directional predictors need profiles |
| Deblocking, chroma interpolation, weighted prediction | Existing scalar numeric paths | Profile-driven vectorisation remains open |
| CABAC arithmetic decoding | Sequential Go state machine | Not labelled SIMD; dominates the retained clip CPU profile |

Six-tap coefficients are `[1,-5,20,20,-5,1]`. Horizontal sums fit signed16 (`-2550..10710`). Diagonal filtering widens these unrounded values to signed32 before `(v+512)>>10`; directional halves use `(v+16)>>5`. Both are clipped to bytes before quarter-pel averaging. This preserves scalar rounding and clipping order.

The luma fast path copies clamped reference pixels into bounded padded scratch before SIMD access. Its five local arrays total 2,024 bytes. Partial widths compute padded lanes but write only the requested pixels. Source/output overlap dispatches to the original scalar implementation because writes can affect later reads. Invalid or unusual input shapes keep existing scalar behaviour; this change does not promise newly defined malformed-input semantics.

## Verification

- Full-range transform comparison against legacy arithmetic, scalar references and purego; protected-page and unaligned buffers.
- All52 quantisation parameters and full-range coefficients for inverse scaling.
- Exact-width SAD loads/stores and bounds guards.
- 102,400 luma scalar comparisons: all16 fractional positions, positive/negative vectors, eight edge/interior base locations, ten block sizes, four reference strides and five constant/extreme/random patterns.
- Additional luma alias, destination padding, rejected-input, protected-page and zero-allocation tests.
- Default/purego retained decode produces YUV SHA256 `54bdddd49d3ec6f13f6147abb300f1d96e3e0159944cc7142800ad667cb3944b`.
- CABAC allocation cleanup preserves33,421 trace lines exactly; ordinary residual decode and L1 selection have zero-allocation tests.

ARM64 QEMU execution covers selected transform, PCM, filterbank and prediction tests plus exact retained pixels. The later SAD16 test covers40,000random full-range blocks, four stride pairs, legacy zero-stride fallback and both protected-page edges; disassembly confirms `VUMAX`, `VUMIN`, `VSUB` and `VUADDLV`. QEMU proves execution/parity, not native ARM64 timing. Other historically NEON-named transform and small-SAD routines still use scalar registers. No full-corpus, native ARM64 performance, or race approval is implied by this inventory.

## Scoped performance evidence

Intel i5-1340P, Go1.26.2, CGO0, CPU0/1, max2CPU. Timings come from short explicitly coordinated synthetic windows, not hostwide isolation. Raw logs are in workspace report `reports/go264-simd-20260912/`.

- Inverse8 packed transpose:79.45→47.05ns. Rejected scalar-transpose candidate:117ns.
- Dequant4/8:10.41→4.24ns and65.05→10.95ns. SAD4/8:16.8→5.82ns and50.5→6.99ns.
- Trace-disabled allocation cleanup:5,177→565allocs,1.52MB→668KB, retained clip2.93→2.75ms.
- Luma interpolation: all18 measured shape/mode pairs faster.16x16 HV:3404→373.4ns; quarterHV:30958→400.7ns. The latter also avoids recomputing the H rows for each output pixel, so its gain is not attributed solely to SIMD.
- Luma whole-clip ABBA window1455: baseline mean3.357ms, candidate3.209ms (~4.4% lower),566allocs/668KB unchanged in that invocation. Earlier window1450 decode runs skipped due to a wrong cwd and are not performance evidence; its primitive timings are valid.

The pre-luma CPU profile attributes59.21% cumulative to CABAC residual decoding and7.89% to luma interpolation. Do not add cumulative percentages to flat percentages, or compare timings across different clock/load windows. Allocation profiles with `memprofilerate=1` are never speed evidence.
