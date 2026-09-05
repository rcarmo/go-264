# go-264

[MIT licensed](LICENSE).

`go-264` is an H.264/AVC decoder written in Go. It targets progressive 8-bit YUV420 Annex B streams and includes scalar reference code, amd64 and arm64 assembly, trace tools and optional GPU experiments.

The historical 300-frame regression stream produced the same visible Y, U and V samples as FFmpeg 7.1.3 in display order, including in-loop deblocking. Its pinned SHA-256 fixture is not currently reproducible from the retained source and toolchain. The checked-in four-frame regression matches its exact retained trace and YUV references; the diagnostic 300-frame stream matches its retained FFmpeg pixels but does not replace the pinned gate.

The independently importable [`audio`](audio/README.md) frontend provides PCM WAV and narrow progressive MP4/AAC-LC decoding, channel conversion and exact polyphase resampling without video dependencies or CGo. AAC coverage is synthetic-fixture qualified and pre-release; see its supported tools, timing restrictions and open quality gates. `cmd/decodeaudio` is an audio-only example.

## Why

I wanted a dead simple, `ffmpeg`-free way to extract selected frames from videos as quickly as possible on low-end hardware, and a reusable library/stack to build upon for later video work.

## Tested features

| Area | Tested behaviour |
|---|---|
| Annex B and NAL parsing | Start-code scanning, emulation-prevention removal and bounded SPS/PPS parsing |
| Slice syntax | I, P and B headers; POC; reference marking; P-list modification; weighted-prediction fields; deblocking controls; I_PCM |
| CAVLC | Baseline decoding and High-profile inter 8x8 residual scans |
| CABAC | I, P and B macroblocks; residuals; reference and motion-vector contexts; 8x8 transforms; I_PCM reset |
| Intra prediction | I4x4, I8x8, I16x16 and chroma prediction modes |
| Inter prediction | P and B partitions, quarter-sample luma, chroma interpolation, Direct mode and weighted prediction used by the pinned stream |
| Transforms | Exact scalar fallbacks plus packed amd64 SSE2 and selected arm64 NEON kernels |
| Frame handling | DPB reference tracking, POC handling and display ordering across IDR GOPs |
| Deblocking | In-loop luma and chroma filtering; amd64 SSE2 pixel kernels with scalar and `purego` fallbacks |
| Residual stores | Exact 4x4/8x8 SSE2 and NEON add, clip and store kernels |
| B prediction | Direct strided writes and exact SSE2/NEON equal or weighted blending |

The pinned stream does not exercise every legal H.264 combination. FMO reconstruction, uncommon weighted B-prediction modes, interlaced and MBAFF streams, chroma formats other than 4:2:0 and bit depths above 8 are unsupported or untested. The project does not contain an encoder.

## Build

Requires Go 1.26.2 or later. Go 1.27 enables additional ARM64 NEON kernels;
older compilers use compatible NEON or scalar implementations. The decoder
builds without cgo or experimental SIMD flags.

```bash
go build -o /workspace/tmp/decode264 ./cmd/decode264
```

## Decode an Annex B stream

```bash
/workspace/tmp/decode264 -i input.h264 -o frames -f color
/workspace/tmp/decode264 -i input.h264 -o frames -f png
/workspace/tmp/decode264 -i input.h264 -o frames -f yuv
```

Output formats:

* `color` writes one colour PNG per display-order frame.
* `png` writes one luma-only PNG per display-order frame.
* `yuv` writes one planar YUV420 file per display-order frame.

Use `-frames N` to limit decoding. The default value, zero, decodes the complete stream.

### Input validation and resource limits

Construct library decoders with `decode.NewDecoder()`. `Decode` rejects malformed
Annex B headers, truncated syntax and incomplete pictures. It requires progressive
8-bit YUV420. Multiple slices are assembled into one picture, with slice-aware
prediction, constrained intra prediction and per-slice deblocking controls.
Each `Decode` call must end at a complete picture. Use `StreamDecoder` for
partial input and long-running sessions.

P-picture short-term references use the active SPS frame-number modulus, including
wrap, list modifications and explicitly signaled gaps. Inferred gap pictures hold
metadata only: attempting to predict from one returns an error. Unannounced gaps
are errors, not automatic packet-loss concealment. POC types 0/1/2, frame-number
and POC wrap, IDR/MMCO-5 resets, long-term references, P-list modification
operations 0/1/2 and ordered MMCO commands 1–6 are supported. B pictures involving
long-term references (including co-located long-term motion) or inferred gaps
remain explicitly unsupported.

`Decode` returns pictures in decoding order. `POC` and `FullPOC` both contain the
derived picture order count, not the raw POC-LSB syntax value.
`ResetsPictureOrder` identifies IDR/MMCO-5 boundaries; `NoOutputOfPriorPics`
preserves the IDR flag for consumers managing a display-order queue. The CLI
sorts pictures within each POC epoch.

`Decoder.MaxFrameMacroblocks` bounds the coded picture before pixel or macroblock
state allocation. Zero uses `decode.DefaultMaxFrameMacroblocks` (36,864). Set a
positive value to choose another budget within the parser and frame-storage
limits. Cropping does not reduce the coded allocation.

The batch API retains outputs in `Decoder.Frames`; `MaxFrames` limits one call,
not the lifetime of a reused decoder. Batch output views share reference storage
and must be treated as read-only.

### Incremental decoding

```go
stream, err := decode.NewStreamDecoder(decode.StreamConfig{
    MaxFrameMacroblocks: 8160, // e.g. coded 1920x1088 for visible 1920x1080
}, func(f *decode.DecodedFrame) error {
    // Consume f here. Its visible pixels and metadata are owned by the caller;
    // keeping or modifying it cannot change future reference prediction.
    return nil
})
if err != nil {
    return err
}
```

Call `stream.Push(chunk)` as Annex B bytes arrive; start codes may span chunks.
Call `stream.Drain()` at the end of a complete segment to finish its last NAL and
picture. Drain preserves references for continuation and is a no-op when repeated
without new input. Outputs arrive synchronously in decoding order, with no
retained output history or internal display queue.

Set `StreamConfig.OutputOrder: true` to deliver pictures in presentation order
instead. This uses the progressive-frame output DPB from H.264 Annex C.4,
including reference/output storage sharing, frame-number gaps, IDR discard/flush
and MMCO5 resets. **I/P streams can need reordering too.** Pictures are released
in increasing POC order when pending output exceeds the SPS
`max_num_reorder_frames` bound, or when DPB storage fills. A zero reorder bound
delivers each completed picture immediately, while retaining it separately if
needed for prediction. When the bound is absent, H.264's inference can allow up
to the level's DPB capacity (at most 16) pictures to await output; the absence of
B slices alone does not imply zero reordering.

In this mode, `Drain()` flushes all pending output and **ends the sequence**:
SPS/PPS remain available, but the next picture must be IDR. Use `Push` without
`Drain` between chunks of one sequence. End markers flush; `Discontinuity`,
`Reset`, and input/callback errors discard pending pictures. Output pixels remain
caller-owned. The default decoding-order mode and the batch API are unchanged.

`MaxNALBytes` bounds buffered encoded input (default 8 MiB per NAL). Picture
storage is bounded by the coded-picture budget and SPS reference count, at most
16. Consumer-retained outputs are outside these limits. The API is sequential:
do not call it concurrently or reentrantly from its callback.

After transport loss, call `stream.Discontinuity()`. It retains SPS/PPS but drops
partial input and reference state, and requires a complete IDR to resume. Input
or callback errors do this automatically; already delivered pictures remain
valid, but the unconsumed remainder of a failed Push is discarded. Start the next
Push at an Annex B start code. `WaitingForIDR()` and `ErrWaitingForIDR` allow a
receiver to request a keyframe. End-of-sequence/end-of-stream NALs also end
prediction continuity. `Reset()` additionally discards all parameter sets.

### Complete-picture input

When a caller already knows the picture boundary, use
`stream.DecodeAccessUnit(annexB, tag)` instead of `Push` followed by `Drain`.
The buffer contains one complete picture, including all its slices and any
leading parameter sets. It is consumed during the call without waiting for
the next picture. The same per-NAL and coded-picture budgets apply.
Callers must also bound the whole input buffer: `MaxNALBytes` is not an
access-unit size limit, and parsing allocates a temporary list of its NALs.

`tag` is an opaque `uint64`, returned unchanged in that picture's `Frame.Tag`.
For example, a caller can use it to associate timestamps or presentation
metadata with decoded images. It belongs to the picture, not to the input call
that happens to release an older output. Tags need not be unique; zero is valid.
Parameter-set and filler-only calls produce no picture and do not carry their
tag into later output. Untagged `Push`/batch output has tag zero.

With `OutputOrder: true`, `DecodeAccessUnit` finishes the input picture and
releases output allowed by the reorder bound without flushing the whole queue.
A call may emit older pictures, each with its own tag, while its new picture
remains buffered; a zero reorder bound emits the completed picture in that call.
Use `Drain` only when ending the sequence: it emits the remaining pictures in
presentation order and requires a new IDR before decoding resumes.

Completing an access unit preserves prediction continuity; only explicit end
markers terminate it. Incomplete pictures or buffers spanning multiple pictures
are errors. Finish or discard pending incremental input before switching to
`DecodeAccessUnit`; a call made while input is pending returns an error without
discarding it. Output ownership and callback rules are the same as for `Push`.

## Packages

```text
nal/              Annex B, NAL units, SPS/PPS and bit reader
frame/            YUV420 storage, DPB helpers and guarded pixel access
entropy/cabac/    CABAC arithmetic, context initialisation and residual decode
entropy/cavlc/    CAVLC residual decode and generated VLC tables
syntax/           Slice and macroblock syntax
pred/             Intra/inter prediction and SIMD dispatch hooks
transform/        4x4/8x8 transforms, quantisation and scalar fallbacks
filter/           In-loop deblocking
me/               SAD/SATD motion-estimation kernels
gpu/              Optional experiment scaffolding
decode/           Decoder pipeline, reconstruction and conformance tests
internal/tables/  Generators for checked-in entropy tables
cmd/decode264      Annex B decoder
cmd/trace264       Syntax and CABAC event tracer
cmd/trace264cmp    Frame and trace comparison helper
cmd/trace264diff   Trace diff helper
```

## FFmpeg parity test

The historical parity gate uses this fixture:

```text
Path:       /workspace/tmp/bbb_annexb.h264
SHA-256:    1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
Format:     640x360, yuv420p, High profile, CABAC, three B-frames
Frames:     300
Reference:  FFmpeg 7.1.3
```

`scripts/bootstrap_fixtures.sh` verifies fixtures in `/workspace/tmp`. It can encode missing fixtures only when the installed FFmpeg includes libx264 and reproduces the pinned hash. The current retained Blender source and FFmpeg source release do not reproduce that bitstream, so a newly encoded diagnostic stream does not pass this gate.

The checked-in low-QP regression remains independently reproducible. Its four decoded frames have YUV SHA-256 `54bdddd49d3ec6f13f6147abb300f1d96e3e0159944cc7142800ad667cb3944b`.

Run the CABAC event comparison:

```bash
./scripts/bootstrap_fixtures.sh
./scripts/cabac_firstdiv.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-firstdiv
```

The accepted trace contains 2,100 events from each decoder and no differing compared field.

Run the pixel comparison:

```bash
GO264_FFMPEG_REGRESSION=1 \
GO264_FFMPEG_BIN=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
GO264_BBB_FIXTURE=/workspace/tmp/bbb_annexb.h264 \
go test ./cmd/decode264 -run TestFFmpegReferenceParityBBB -count=1 -v
```

`TestFFmpegReferenceParityBBB` checks the fixture hash, FFmpeg version, frame count, display order and every visible sample in the Y, U and V planes. A failure reports the first differing frame, plane, macroblock and pixel. The accepted result has `maxdiff=0` for all three planes over all 300 frames.

Compare files produced by a separate decoder run with a contiguous FFmpeg rawvideo file:

```bash
scripts/compare_yuv_frames.py \
  --go-dir /workspace/tmp/bbb-go \
  --reference /workspace/tmp/bbb-ffmpeg.yuv \
  --width 640 \
  --height 360 \
  --frames 300
```

## Licence

The entire go-264 project is licensed under the [MIT License](LICENSE), including the video decoder, audio packages, command-line tools, scripts, tests and documentation. Copyright (c) 2026 Rui Carmo.

Imported MIT material retains its upstream copyright and licence notices. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the OxideAV AAC source attribution and pinned table provenance. Referenced external datasets retain their own licences; they are not relicensed by this project. FFmpeg and other offline validation tools are not runtime dependencies or bundled project code.

## Validation

Use a workspace-backed Go temporary directory on systems where `/tmp` is mounted with `noexec`:

```bash
export TMPDIR=/workspace/tmp
export GOTMPDIR=/workspace/tmp/go-264
mkdir -p "$GOTMPDIR"

go test ./...
go vet ./...
GOOS=linux GOARCH=arm64 go build ./...
git diff --check
```

Run the one-frame CABAC reconstruction check when changing entropy or reconstruction code:

```bash
FFMPEG=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
./scripts/cabac_parity_baseline.sh \
  /workspace/tmp/testsrc_cabac_p.h264 \
  /workspace/tmp/go264-cabac-parity-baseline
```

The accepted result is `99.00dB` and `maxdiff=0` for Y, U and V.

## Trace tools

`trace264 -cabac` emits macroblock events from the decoder. Scripts under `scripts/` can instrument a local FFmpeg 7.1.3 source tree and compare CABAC, Direct-mode, motion-cache and reconstruction state. Store output directories under `/workspace/tmp` because raw frames and trace files can be large.

Available decoder traces include:

| Variable | Output |
|---|---|
| `GO264_CABAC_ARITH_TRACE=1` | CABAC arithmetic state |
| `GO264_CABAC_CBP_TRACE=1` | Coded-block-pattern decisions |
| `GO264_CABAC_RESIDUAL_TRACE=1` | Residual significance, last flags and levels |
| `GO264_CABAC_SYNTAX_TRACE=1` | Intra syntax bins |
| `GO264_RECON_TRACE=1` | Prediction, coefficient, residual and output checksums |
| `GO264_DIRECT_TRACE=1` | Spatial and temporal Direct derivation |

Trace text is an internal diagnostic format and may change.

## Performance

The current fast paths cover bit reading, CAVLC prefix lookup, motion compensation, B-frame blending, deblocking pixels, residual stores, AAC FFT/IMDCT work, PCM validation and conversion, WAV unpacking and ordered resampler products.

The table compares baseline `a66b319` with `76a23d9` on an Intel i5-1340P with Go 1.26.2, `CGO_ENABLED=0`, `GOMAXPROCS=2` and CPUs 0–1. Both matrices use the same diagnostic H.264 stream (`b115b066…bc94a`) and retained audio fixtures. Values are unprofiled `benchmem` results; profile-instrumented timings are excluded.

| Workload | Before | After | Change | After allocations |
|---|---:|---:|---:|---:|
| H.264, 300 diagnostic frames | 1.449 s | 1.185 s | −18.2% | 20,046/op |
| AAC, 48 kHz stereo, three files | 5.334 ms | 4.332 ms | −18.8% | 162/op |
| AAC, 16 kHz mono, three files | 6.762 ms | 5.776 ms | −14.6% | 171/op |
| WAV, unchanged 48 kHz stereo | 0.622 ms | 0.120 ms | −80.7% | 14/op |
| WAV, 16 kHz mono | 2.969 ms | 2.674 ms | −9.9% | 18/op |
| Resampler, 48→16 kHz mono | 1.346 ms | 1.263 ms | −6.2% | 0/op |

These figures rank work on one host. They do not replace the unavailable historical H.264 fixture gate or native ARM64 measurements.

Run the same workload matrix with immutable local fixtures:

```bash
GO264_PROFILE_RUN=1 scripts/profile_matrix.sh --run \
  --output /workspace/reports/go264-profile-current \
  --cpu-list 0,1
```

Use `docs/profiling.md` for fixture, CPU-affinity, allocation and acceptance requirements.

## Generate entropy tables

```bash
go generate ./entropy/cabac ./entropy/cavlc
```

The generators live under `internal/tables/` and use the `//go:build ignore` constraint. Generated CABAC and CAVLC tables are checked in.

## Development plan

`PLAN.md` lists tested scope, open decoder work, optimisation requirements and the encoder sequence.
