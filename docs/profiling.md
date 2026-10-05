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
