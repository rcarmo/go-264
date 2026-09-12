# go-264 development plan

The decoder matches FFmpeg 7.1.3 sample for sample on the pinned 300-frame regression stream. Development now covers more H.264 syntax combinations, then measured SIMD work, then an encoder.

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

The decoder matches every visible Y, U and V sample in display order with in-loop deblocking enabled. A separate run with deblocking disabled also matches. The CABAC trace check compares 2,100 macroblock events from each decoder without a differing compared field.

`TestFFmpegReferenceParityBBB` verifies the fixture hash, FFmpeg version, frame count, display order and pixel data. A mismatch reports the first frame, plane, macroblock and pixel.

## Implemented and tested

* Annex B scanning, emulation-prevention handling and bounded SPS/PPS parsing.
* I, P and B slice headers; POC and DPB bookkeeping; reference marking; P-slice list modification; B-list operand parsing; display reordering.
* CAVLC and CABAC macroblock and residual decoding, including 8x8 transforms and I_PCM reset handling.
* I4x4, I8x8, I16x16 and chroma intra prediction.
* P and B inter partitions, quarter-sample luma, chroma interpolation, spatial and temporal Direct mode, and weighted prediction used by the regression stream.
* Scalar 4x4 and 8x8 transforms, residual addition and in-loop luma and chroma deblocking.
* Bounds checks for readers, frame storage, coefficient buffers and reconstruction helpers.
* Unit, fuzz, syntax, motion, reconstruction, scalar/SIMD parity and architecture build checks.

The tested scope is progressive 8-bit YUV420 Annex B video. The regression stream does not cover every item above in every legal combination.

## Decoder work

### Add conformance streams

Add small fixtures for these cases:

* Multiple IDR GOPs.
* `frame_num` and POC wrap.
* Explicit weighted B prediction.
* Long-term references.
* B-slice list modification.
* `log2_max_frame_num` values that produce `MaxPicNum` values other than 16.
* Field-coded and MBAFF video.
* Legal cropping at coded-frame edges.

Record the source, encoding parameters and SHA-256 for each fixture. Tests must compare display-order Y, U and V samples with a pinned reference decoder. Add a focused unit test for the primitive that caused each mismatch.

### Complete unsupported syntax

FMO syntax is parsed but FMO reconstruction is unsupported. Implement it only with fixtures for the required slice-group map types.

Explicit weighted B prediction needs fixtures that cover luma and chroma weights, offsets and both reference lists. The existing regression covers only the weighting behaviour present in the pinned stream.

Interlaced field pictures and MBAFF require separate picture-order, reference-list, motion and deblocking tests. Do not infer support from progressive streams.

### Maintain trace comparisons

CABAC, Direct-mode, BIDI and reconstruction traces must remain opt-in and deterministic. A new trace needs a comparator or another named consumer. Production code must not contain hard-coded POC or macroblock probes.

Generated FFmpeg changes and trace files belong under `/workspace/tmp`. Repository scripts may patch the local FFmpeg 7.1.3 tree but must not modify a system FFmpeg installation.

## SIMD and allocation work

Rui requires SIMD for timing-critical vectorisable code. The completed optimisation campaign used instruction-level coverage inventories and end-to-end profiles rather than assembly function names. The [audio coverage report](audio/SIMD.md) records SSE2 filterbank/rotations, exact-table dequant, PCM conversion/layout, stereo bands/TNS and allocation reduction; ARM64 audio and remaining validation/unpacking/PRNG kernels are explicit unqualified scope, not a claim that every loop is vectorised. Synthesis results are not whole-file decoder speedups.

The video inventory found real vectors in SAD16x16 and prediction copy/fill. The new amd64 4×4 transform path now uses actual SSE2 packed int32 lanes; historical AVX2-named scalar entry points remain compatibility references. It uses baseline SSE2 independently of CPUID AVX2. `purego` disables transform assembly, and the scalar inverse reference now matches existing assembly's wide arithmetic/narrow-between-pass contract over full-range coefficients. Before that correction, the old scalar reference disagreed with legacy inverse assembly on1000/1000 random full-range blocks (small historical tests missed it).

20,000 full-range cases per transform, legacy-assembly comparison, guarded/unaligned blocks, default/purego decode tests and the retained four-frame synthetic low-QP exact YUV hash pass. Coordinated100ms samples measure inverse15.59→7.783ns (2×), forward12.62→7.616ns (1.66×), zero allocations. The host FFmpeg lacks libx264 and its advertised OpenH264 encoder/decoder fail initialization; live synthetic comparison attempts are retained as failures. The separate pinned300-frame FFmpeg7.1.3 gate remains unavailable and is **not** replaced by the four-frame regression.

amd64 8×8 inverse now uses packed SSE2 butterfly passes and word/dword/qword transposes with stack-owned scratch. Ten thousand full-range cases match legacy assembly and the corrected wide scalar reference; guards and the retained pixel hash pass. The first scalar-transpose candidate was rejected (117ns vs79ns); packed transpose measured47.05ns vs79.45ns scalar (1.69×,zeroalloc) in coordinated window1415. This is a kernel result, not whole-video speedup.

ARM64 forward/inverse4 now use actual packed NEON and pass full-range/guard/QEMU execution checks; amd64 forward8 and ARM64 8×8 still fall back to Go. Real AVX kernels must check OSXSAVE/XGETBV, not just the existing CPUID7 flag. Deblocking and SATD remain scalar because current profiles did not justify safe exact SIMD work. Broader video qualification remains dependent on restored canonical fixtures/tooling.

SSE2 inverse scaling covers4×4 (wrapped low products, DC preservation) and8×8 (signed wide products,+2/>>2,wrapped narrowing). All52QP values and full-range coefficients match the reference, with exact guard/tail checks.4×4/8×8 SAD uses exact-width byte loads and `PSADBW`; strides/extents are validated before assembly. Window1420 kernel results:4×4dequant~10.41→4.24ns,8×8~65.05→10.95ns,SAD4~16.8→5.82ns,SAD8~50.5→6.99ns,zeroalloc. These are dense-kernel results; sparse whole-decoder gains must be profiled separately.

A retained four-frame allocation profile found trace formatting forced CABAC coefficient scratch onto the heap even when tracing was disabled. Trace helpers now copy only when enabled; residual decode and ordinary B-list selection have zero-allocation regression tests. B-list ordering uses bounded stack scratch up to32DPB entries and proportionate fallback for unusual direct callers; no shared cache/pool. Original ordering, wrap/ties and clamping are preserved. Window1430 A1/B1/B2/A2:5177→565allocations,1,517,555→668,030B and~2.93→2.75ms per clip. Trace-enabled33,421lines and exact YUV output are byte-identical to baseline. CPU time from `memprofilerate=1` runs is excluded from speed comparisons.

The refreshed retained-clip CPU profile at `9c3aafa` attributes 59.21% cumulative to CABAC residual decoding (sequential arithmetic coding) and 7.89% to luma interpolation. The amd64 luma path now uses actual SSE2 eight-lane six-tap H/V filters, four-lane signed32 HV filtering, and rounded byte averages. Bounded <=16x16 blocks use 2,024 bytes of local arrays; edges are clamped into padded scratch. Aliased slices and unusual shapes keep original scalar write-through semantics. Integer interiors use Go copy. 102,400 scalar comparisons cover all fractional positions, negative vectors, clamp edges, sizes and extreme/random samples; aliases, row guards, protected pages and zero allocations also pass.

Window1450 interpolation microbenchmarks improved all18 shape/mode pairs; the precompiled whole-clip benchmark skipped because of a wrong cwd, so that part supplies no timing evidence. Corrected window1455 has four valid A/B/B/A rows:3.357→3.209ms (~4.4% lower) with unchanged566allocs/668KB. Retained pixels match the established hash. This does not clear the absent300-frame gate. ARM64 QEMU now executes selected transform/PCM/filterbank/prediction tests and exact retained pixels. SAD16 uses actual NEON max/min/subtract and widened reduction, matching40,000random full-range cases, four stride pairs, legacy invalid-geometry semantics and protected-page edges. SAD4/8 also uses actual NEON exact-width lane loads and matches60,000random blocks plus protected-page edges. ARM64 `.m4a` mono duplication and planar stereo interleave use `VZIP1/VZIP2`; QEMU layout parity/guards pass and12retained decode combinations match amd64 exactly. S16 rounding and stereo averaging intentionally remain scalar pending exact-order vectorisation. ARM64 forward/inverse4 now use actual packed NEON butterflies and transposes in both passes. Inverse explicitly widens sign, emulates arithmetic shifts, rounds and narrows between passes; both match20,000full-range and protected-page cases under QEMU. ARM64 AAC overwrite-window multiplication also uses two-lane NEON with exact reverse/tail/guard parity and12retained `.m4a` outputs; vector add/overlap was rejected after subnormal and one-bit differences, so it stays scalar. ARM64 AAC dequant scaling packs two exact table values into NEON FMUL after scalar gathers; every signed magnitude×256scale values and guards pass. This is emulated functional coverage, not native ARM64 timing;8x8, FFT/rotations, stereo bands and TNS remain gaps. Window timings must not be compared across different CPU clock/load conditions.

The refreshed retained-video profile at778536c attributed58.78%cumulative to sequential CABAC residual decoding. Chroma interpolation was a measured but modest2.03%flat slice. Interior fractional8×8 chroma now uses exact SSE2 separable word products; all64fraction combinations, clamp-edge scalar fallback, aliases, guards and retained pixels match. Window1550 measured17.07ns/zeroalloc for the kernel and A/B/B/A retained decode2.614→2.570ms (~1.7% lower); allocations were unchanged. Failed profile1540 ended before workload because a pending dispatch stub did not build; replacement1545 supplied the profile evidence.

A later diagnostic 300-frame profile at `1369a5c` measured873,727,136B and767,902allocs/op. Its normal-rate heap profile attributed49.74% of sampled allocated objects to rebuilding B-slice List0 for macroblock temporal-direct calls. Commit `a66b319` builds that immutable list once per slice; exact retained trace/pixels and full checks pass. Window1850 measured767,900.5→361,701allocs/op (-52.90%),873,724,408→867,732,420B/op (-0.69%), and1.457779→1.439494s/op (~1.25%). Trace-flag snapshotting in `cc61849` separately measured1.515678→1.468102s/op (~3.14%) while retaining exact opt-in output.

Further candidates remain future profile-driven work rather than completion blockers:

* Batched inverse transform and dequantisation.
* Fractional motion-compensation shapes that still lack an interior fast path.
* Luma and chroma deblocking.
* Macroblock-result allocations, provided ownership and escape analysis prove safe reuse.

Each future SIMD change requires:

1. Scalar and assembly outputs that are coefficient-exact or pixel-exact.
2. Architecture-specific tests and a safe scalar fallback.
3. Before-and-after benchmarks on the same host, Go version and fixture.
4. The complete canonical 300-frame FFmpeg parity test when its exact fixture/toolchain is restored.
5. A Linux arm64 build from the development host.

The local optimisation branch closes the currently implementable measured scope, not every possible kernel. Full default/purego tests, vet, Linux ARM64/386 builds, retained exact pixels/traces and diagnostic 300-frame profiling pass. Native ARM64 timing, race testing, canonical `1305bc99…841ff` 300-frame reproduction and wider conformance corpora remain explicit qualification gaps; the diagnostic `b115b066…bc94a` stream does not replace them.

CABAC is sequential and is excluded from GPU work. GPU experiments may cover batched motion search or transforms after CPU profiles identify enough parallel work to offset transfer and setup costs.

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

Run the pinned CABAC and pixel gates after decoder changes:

```bash
./scripts/bootstrap_fixtures.sh
./scripts/cabac_firstdiv.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-firstdiv

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
