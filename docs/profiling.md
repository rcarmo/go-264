# Profiling and allocation protocol

Use `make test`, `make test-race`, `make test-purego` or `make test-focused PKG=./decode RUN=TestName` for all Go test runs. `scripts/test-profile.sh` retains a matching test binary, CPU/heap profiles, logs, revision, toolchain, workload flags and cumulative CPU/`alloc_space`/`alloc_objects` reports per package under `GO264_EVIDENCE_ROOT/tests/<run-id>/`. Review the reports after every run; short tests can produce zero CPU samples, so use a representative profiled benchmark before judging performance. The sample rate is 100 Hz and the heap profiling rate is 524288 bytes. `Makefile` resolves `PROJECT_TMP_ROOT` before setting tool caches or child temporary paths.

Use `scripts/profile_matrix.sh` for the fixed video, AAC, WAV and resampler optimisation matrix. Rebuildable caches and temporary files use the resolved project's `cache/`, `build/` and `runs/` directories. Fixtures and profile evidence are retained separately.

Preparation does not run workloads:

```sh
scripts/profile_matrix.sh --output /workspace/reports/go-264/profile-matrix-prepared
```

Preparation records the Git revision, toolchain, working-tree state, CPU list, fixture hashes, binary hashes, commands and compiler escape analysis. Missing fixtures or tools fail closed.

After explicit compute admission, run the matrix with both gates:

```sh
GO264_PROFILE_RUN=1 scripts/profile_matrix.sh --run \
  --output /workspace/reports/go-264/profile-matrix-current \
  --cpu-list 0,1
```

`--run` refuses to start when either gate is absent or a material process is already using an admitted CPU. Each workload is bounded and emits:

- a CPU profile;
- a normal-sampling heap profile;
- `alloc_space`, `alloc_objects`, `inuse_space` and `inuse_objects` reports;
- a separate unprofiled `-benchmem` result;
- maximum resident-set-size evidence;
- survivor and SHA-256 manifests.

Profile-instrumented `ns/op` values are attribution evidence, not speed evidence. Compare time only with separate unprofiled same-window runs that use identical binaries, fixtures, commands, CPU affinity and Go settings.

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
- the relevant retained trace, YUV or PCM oracle remains exact;
- a same-window benchmark shows a credible CPU improvement, or allocation/retained memory falls without a meaningful time regression;
- the change does not move work into unmeasured setup, retained memory or another caller;
- failed and rejected candidates remain recorded.

Reprofile after each video, AAC and WAV/resampler phase. Stop when the target falls below material profile share, exactness fails, or the measured gain does not justify complexity.
