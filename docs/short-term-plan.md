## Short-term plan

The decoder now has exact FFmpeg results for its main progressive 8-bit YUV420 regression stream, plus matching amd64 and ARM64 SIMD coverage. The next work should tighten what the repository claims and make that result easier to reproduce. This plan deliberately stops before FMO, field pictures, MBAFF, new chroma formats, higher bit depths or encoder work.

Native ARM64 benchmarking is also deferred. ARM64 correctness, guard tests and cross-builds remain required; native timing is not a gate for the phases below.

## Phase 1 -- Make the documentation describe the code

Repair `README.md`, `PLAN.md` and `docs/video-simd.md` where they still describe the pre-multi-slice or pre-SIMD decoder. In particular, remove the one-slice restriction, distinguish implemented behaviour from fixture coverage, and describe the current amd64/ARM64 dispatch without calling accelerated paths scalar.

This phase is complete when the three documents agree on supported input, known limits, fixture requirements and architecture checks. Documentation-only changes run link/path checks where available plus `git diff --check`; they do not need the media regression corpus.

## Phase 2 -- Make skipped fixture tests visible

Inventory every fixture-dependent `t.Skip` and classify it as an optional developer test, a generated synthetic fixture or the pinned external regression. Add one test/reporting command that lists unavailable fixture gates without turning an ordinary `go test ./...` run into a network operation.

Keep media outside Git unless it is synthetic and small. Every fixture needs provenance, generation or retrieval instructions, SHA-256, decoder/reference versions and the exact comparison command. The pinned BBB hash remains authoritative; a different encoding is diagnostic data, not a substitute.

This phase is complete when a clean checkout can say precisely which gates ran and which could not run, and CI cannot present a skipped external regression as a successful parity run.

## Phase 3 -- Put supported state transitions under exact fixtures

Add small synthetic progressive 8-bit YUV420 fixtures for behaviour the decoder already implements but the main stream does not isolate:

* more than one IDR GOP;
* `frame_num` and POC wrap with a non-default `MaxPicNum`;
* legal cropping at coded-frame edges.

Keep each fixture focused enough that a failure identifies one state transition. Compare display-order Y, U and V samples exactly against a pinned FFmpeg build; add a primitive unit test only when a stream exposes an implementation defect.

This phase is complete when all three fixtures are reproducible, hash-pinned and exact, with no regression in the existing built-in suite or retained low-QP CABAC/B fixture.

## Phase 4 -- Exercise reference and weighting syntax already present

Add separate exact fixtures for long-term references, B-slice list modification and explicit weighted B prediction. Weighted prediction must cover luma and chroma, offsets, and both reference lists rather than merely generating a stream with the flag set.

Treat mismatches as decoder bugs only after locating the first syntax, reference-list, prediction or reconstruction divergence. Fixes stay fixture-driven and narrow; no tolerance changes or image-specific corrections.

This phase is complete. The official FFmpeg FATE vectors are hash-pinned and compared byte-for-byte with FFmpeg 7.1.3, both filtered and with deblocking disabled:

* `CVWP2_TOSHIBA_E.264` covers explicit weighted B prediction;
* `HCMP1_HHI_A.264` covers B-slice list modification; and
* `MR1_BT_A.h264` covers long-term-reference promotion and limits (MMCO 3/4).

The first two exposed the focused prediction/list defects fixed by PR #20. `MR1_BT_A` was already exact and is backed by the existing direct MMCO/reference-state tests.

## Phase 5 -- Close the short-term loop

Run the complete available checks on the resulting tree:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp/go-264
mkdir -p "$GOTMPDIR"

go test -count=1 ./...
go vet ./...
go test -count=1 -race ./decode ./frame ./filter ./pred ./transform
CGO_ENABLED=0 go test -count=1 -tags purego ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
git diff --check
```

Run the pinned FFmpeg/CABAC gates when their exact fixtures and FFmpeg 7.1.3 are available. Record unavailable gates rather than replacing them with another stream.

The short-term plan is complete for all hardware-feasible gates. The three checked-in state fixtures and three external Phase 4 vectors are sample-exact. Full, race, `purego`, vet and Linux ARM64 cross-build gates pass. The historical pinned BBB bytes remain unavailable and are reported as such by strict fixture status rather than replaced with a different stream. Native ARM64 execution/performance work remains deferred.
