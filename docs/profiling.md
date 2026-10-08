# Profiling and allocation protocol

Ordinary development tests use `make test`, `make test-race`, `make test-purego` or `make test-focused PKG=./decode RUN=TestName` without profiling. During pre-release verification, use `make prerelease-profile`; `scripts/test-profile.sh` captures CPU and heap profiles per package, examines cumulative CPU, `alloc_space` and `alloc_objects`, writes concise findings to `GO264_EVIDENCE_ROOT/conclusions/<run-id>.txt`, then deletes raw profiles, the matching test binary and disposable logs. Targeted `make profile-benchmark` follows the same disposal rule. The CPU profiler samples at 100 Hz and the heap profiling rate is 524288 bytes. Short tests can produce zero CPU samples; use a representative workload before judging performance. `Makefile` resolves `PROJECT_TMP_ROOT` before setting tool caches or child temporary paths.

Use `scripts/profile_matrix.sh` for the fixed video, AAC, WAV and resampler pre-release matrix. Rebuildable caches and temporary files use the resolved project's `cache/`, `build/` and `runs/` directories. Source fixtures remain durable; captures, binaries and logs under `runs/profile-matrix/` are deleted after analysis. Only short findings remain under `GO264_EVIDENCE_ROOT/conclusions/`.

Preparation does not run workloads:

```sh
scripts/profile_matrix.sh
```

Preparation records Git revision, toolchain, working-tree state, CPU list, fixture hashes, binary hashes, commands and compiler escape analysis while it runs, then removes that disposable preparation data. Missing fixtures or tools fail closed.

After explicit compute admission, run the matrix with both gates:

```sh
GO264_PROFILE_RUN=1 scripts/profile_matrix.sh --run --cpu-list 0,1
```

`--run` refuses to start when either gate is absent or a material process is already using an admitted CPU. Each workload is bounded and emits:

- CPU and normal-sampling heap profiles;
- cumulative `alloc_space`, `alloc_objects`, `inuse_space` and `inuse_objects` reports;
- profiled `-benchmem` attribution (not independent speed evidence); and
- a survivor check and concise conclusions before raw artifacts are deleted.

Profile-instrumented `ns/op` values are attribution evidence, not speed evidence. Compare speed with equivalent unprofiled workloads on identical binaries, fixtures, CPU affinity and Go settings. Ordinary development tests may run without profiling; pre-release CPU and allocation analysis must still cover the same workload.

## Allocation accounting

Classify memory before changing ownership:

1. **Output lifetime:** frames, caller-visible PCM and other retained results. Report these separately; reducing temporary churn must not shorten caller ownership.
2. **Decoder-owned bounded state:** picture, motion, context, filterbank and resampler storage. Right-size these from validated geometry. Reuse is valid only within the documented sequential lifetime.
3. **Temporary allocations:** parse trees, wrappers, copied metadata and escaped macroblock values. Use `alloc_objects`, `alloc_space` and escape analysis to select changes.
4. **Profiler/runtime overhead:** profile buffers, compression writers, test harness and file handles. Exclude these from product allocation claims.

Do not add an unbounded cache, package-global mutable scratch, shared decoder storage or speculative `sync.Pool`. A caller-owned or decoder-owned buffer needs an explicit alias, cancellation, rollback and close lifetime.

## Acceptance gates

Accept an optimisation only when:

- scalar, SIMD and `purego` outputs remain exact;
- focused and full tests, vet and architecture builds pass;
- the relevant trace, YUV or PCM oracle comparison remains exact; delete its raw captures after analysis;
- a same-window benchmark shows a credible CPU improvement, or allocation/retained memory falls without a meaningful time regression;
- the change does not move work into unmeasured setup, retained memory or another caller;
- failed and rejected candidates leave concise findings and important measurements; raw profiles and failed/probe artifacts are disposed of after analysis.

Reprofile after each video, AAC and WAV/resampler phase. Stop when the target falls below material profile share, exactness fails, or the measured gain does not justify complexity.

## October 2026 hotspot tuning

Baseline `ec2748e` and the hotspot changes used Go 1.27.1 on Linux/amd64,
Intel i7-12700, `GOMAXPROCS=2`, CPU 5 affinity and `CGO_ENABLED=0`.
Five unprofiled trials alternated baseline/candidate order with identical
benchmark definitions and inputs. The host was shared; medians and overlapping
timing ranges do not establish an end-to-end decode speedup.

| Workload | Before | After |
| --- | ---: | ---: |
| Resample one second, 48 kHz → 16 kHz mono | 1.089 ms | 0.968 ms |
| Resample one second, 48 kHz → 16 kHz stereo | 6.350 ms | 2.155 ms |
| Resample one second, 44.1 kHz → 16 kHz mono | 1.046 ms | 0.903 ms |
| Resample one second, 44.1 kHz → 16 kHz stereo | 5.575 ms | 1.947 ms |
| Gap of 65,535 frame numbers, three reference slots | ~3.87 ms; 27.26 MB; 65,537 allocations | ~0.18 µs; 1,280 B; four allocations |
| Inactive low-QP macroblock deblocking | 108 ns | 4.774 ns |
| Active-QP macroblock deblocking | 1,339 ns | 1,326 ns |
| MR1 video decode | 19.698 ms; 3,890 allocations | 19.420 ms; 3,829 allocations |

Resampler reads allocate nothing in either revision. Exact rational coordinate
advancement removes repeated rate divisions; stereo interior windows share
indexing while retaining each channel's product order. `renderChannels` is a
test collector: reserving its remaining output reduces test-harness growth,
not production resampler allocations.

Bulk gap staging materialises only the final bounded reference metadata when
no output callback observes insertions. The callback path stays stepwise;
long-term references, rollback and input ownership are unchanged. A redundant
DPB pointer-slice copy is also removed. Inactive deblocking skips boundary
strength computation only when every luma/chroma threshold rejects filtering.
The active-QP ranges overlap. MR1 decode ranges also overlap
(19.410–20.448 ms before, 19.381–20.220 ms after); its established improvement
is 61 fewer allocations per decode. Retained output picture storage is unchanged.

Before/after CPU, `alloc_space` and `alloc_objects` profiles were analysed.
For equal workloads, sampled CPU was 1.22 s → 450 ms for 200 stereo resamples,
2.20 s → 100 ms for 20 million inactive deblock calls, and 1.01 s in both
50-decode video runs. Small post-change gap allocations were below useful
heap-sampling resolution; unprofiled allocation counts supply that comparison.
Raw profiles and matching binaries were deleted after analysis.

A compact CAVLC run-before lookup and an intra prediction bypass showed no
clear repeatable gain and were removed. Tests compare gap results with the
stepwise schedule, deblocking with the original edge schedule, and resampling
with a direct-index FIR oracle, including seek and cancellation/resume.
The FIR arithmetic comparison allows `1e-14` absolute rounding error for
normalised inputs; filter coefficients, accumulation order and existing
passband/alias requirements are unchanged. No subjective audio assessment was
made because the changes preserve the established arithmetic path.

Full, race (including the resampler), `purego`, vet, available Phase 4 parity
and ARM64 compile/link checks passed. Native ARM64 execution, the full external
six-workload matrix and strict parity against the missing historical BBB
fixture were not run. Fifteen short packages in the pre-release suite had no
CPU samples; the representative workloads above supplied CPU attribution.

## P-reference-list allocation tuning

The next batch uses baseline `ed39bcf`, Go 1.27.1 and the same host, affinity
and five-trial alternating procedure. `buildPReferenceList` sorts reference
pointers in 16-entry local scratch, with a heap fallback for larger manually
supplied stores. A typed sort removes reflection allocations. Modifications
compact the remaining list before an overlapping right shift, preserving
repeated earlier selections without allocating a temporary tail. Only the
slice-owned active list is allocated for normal-sized stores.

| Reference count | Plain list, before → after | Modified list, before → after | Allocations, plain / modified |
| --- | ---: | ---: | ---: |
| 1 | 76.19 → 23.35 ns | 108.8 → 29.8 ns | 3 / 4 → 1 / 1 |
| 4 | 256.4 → 107.7 ns | 328.1 → 121.3 ns | 6 / 7 → 1 / 1 |
| 16 | 711.6 → 444.8 ns | 944.9 → 490.1 ns | 8 / 9 → 1 / 1 |

MR1 decoding saves 1,002 allocations per decode (3,829 → 2,827) and about
32 KB (4,589,587 → 4,557,352 B/op). Median unprofiled time is
19.810 → 19.583 ms, with overlapping ranges of 19.509–20.245 ms and
19.476–26.603 ms. These trials establish allocation savings, with no
established end-to-end CPU speedup. Output-picture storage is unchanged.

Equivalent 50-decode CPU, `alloc_space` and `alloc_objects` profiles were
analysed. Both CPU profiles sampled 1.03 s. Baseline list construction
accounted for 30,038 sampled objects and 1.50 MB; the candidate stack was
below useful normal-rate sampling resolution. Aggregate sampled heap totals
varied upwards despite lower benchmark allocation counts, so exact per-run
allocation estimates come from `benchmem`, not the sampled heap totals.
Raw captures and matching binaries were removed after analysis and validation.

A 20,000-case differential test compares valid and invalid stores and
modifications with the original algorithm, including wrap, long-term
references, repeated selections, nil entries, larger manual stores, error
behaviour and input ownership. An allocation test enforces one owned list
allocation. Full, race, `purego`, vet, available Phase 4 parity and ARM64
compile/link checks pass. Fourteen short packages in the pre-release suite
provided no CPU samples. Historical BBB parity and native ARM64 execution
are still unavailable; the wider-format and encoder tracks are unchanged.
