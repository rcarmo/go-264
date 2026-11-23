# PR #26 review — multichannel AAC-LC

Reviewed contributor head `88884d7` against `cecca38` with Go 1.27.1 on
Linux/amd64 and FFmpeg 8.1.2. Multichannel configurations 3–7 are accepted
within the existing narrow AAC-LC subset; PCE and coupling remain unsupported.

## Fixes

- Configuration 7 uses specification-defined 7.1 wide, including FLC/FRC.
  Coded order is FC, FLC, FRC, FL, FR, BL, BR, LFE; native PCM order is
  FL, FR, FC, LFE, BL, BR, FLC, FRC. FFmpeg's default compatibility remap
  cannot define the public layout. Independent oracle tests now use strict
  decoder mode, verify the layout and fail on layout/probe errors.
- Duplicate element type/tag pairs fail as malformed; tags remain arbitrary
  and may overlap across different element types. Focused tests cover both.
- Internal MP4 PCM output requests reject unsupported multichannel counts
  instead of silently returning the source layout.
- PCM scratch is normalised once before finite validation and publication.
  Mono/stereo scratch uses a separate helper frame: on amd64 the original
  Decode stack frame was 164,072 bytes; the mono/stereo helper reserves
  32,904 bytes and Decode reserves 104. Eight-channel scratch stays bounded
  in its own frame. Per-packet heap allocation remains zero.

## Verification and profiling

Full, race (including all audio packages), `purego`, vet and Linux ARM64
build/test-link checks pass. ARM64 binaries were compiled, not executed.
The complete opt-in FFmpeg audio oracle suite passes, including native
positions, independently mixed stereo/mono, PNS, transients, clipping bounds,
resampling and container handling. About 396,000 multichannel fuzz executions
completed without failure. All profile captures and disposable test artifacts
were removed after analysis.

CPU, `alloc_space` and `alloc_objects` were analysed on representative
multichannel decoding. FFT/filterbank synthesis dominates CPU; packet decoding
allocates nothing, while sampled heap data primarily represents test setup
and profiling runtime. Equal 5,000-packet profiles sampled 850 ms before and
830 ms after. Heap samples varied from 2.37 MB/1,160 objects to
3.33 MB/1,190 objects, dominated by profiling/setup rather than per-packet
work. These sampled totals establish no heap improvement. No new DSP
approximation or reassociation was added.

Five final unprofiled trials alternate original/fixed binary order on CPU 5,
`GOMAXPROCS=2`, with identical synthetic impulse access units. Median decode
ns/op: mono 20,807 → 20,681; stereo 39,983 → 39,619; 5.1 125,291 → 122,537;
7.1 wide 164,495 → 164,226. Timing ranges overlap, so these final changes
establish lower mono/stereo stack reservation and zero per-packet allocations,
without a general CPU speedup claim. Earlier normalisation-only measurements
showed 2.6–4.5% gains; the final combined timings above supersede that claim.

The synthetic oracle checks error distributions against independent float32
FFmpeg PCM: at least 70 dB SNR and maximum absolute error at most `1e-5`,
with observed downmix checks around 139 dB and errors below `7e-8`.
Channel-specific tones also verify isolation. Encoder startup is excluded
from sample comparison but remains included in channel/clip checks. These
synthetic tests do not provide subjective listening evidence for real media.
Historical video BBB bytes remain unavailable; this audio PR does not replace
that gate. Native ARM64 execution and the external six-workload matrix were
not run. Twelve short packages in final pre-release profiling had empty
CPU captures and provide no CPU attribution evidence.
