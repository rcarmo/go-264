# go-264

`go-264` is an H.264/AVC decoder written in Go. The pinned regression stream produces the same visible Y, U and V samples as FFmpeg 7.1.3 for all 300 frames in display order, including in-loop deblocking.

The decoder targets progressive 8-bit YUV420 Annex B streams. The repository also contains scalar reference code, amd64 and arm64 assembly hooks, trace tools and optional GPU experiments.

The independently importable [`audio`](audio/README.md) frontend provides PCM WAV and narrow progressive MP4/AAC-LC decoding, channel conversion and scalar polyphase resampling without video dependencies or CGo. AAC coverage is synthetic-fixture qualified and pre-release; see its supported tools, timing restrictions and open quality gates. `cmd/decodeaudio` is an audio-only example.

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
| Transforms | Scalar 4x4 and 8x8 integer transforms with assembly dispatch hooks |
| Frame handling | DPB reference tracking, POC handling and display ordering across IDR GOPs |
| Deblocking | Scalar in-loop luma and chroma filtering |

The pinned stream does not exercise every legal H.264 combination. FMO reconstruction, uncommon weighted B-prediction modes, interlaced and MBAFF streams, chroma formats other than 4:2:0 and bit depths above 8 are unsupported or untested. The project does not contain an encoder.

## Build

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

The parity test uses this fixture:

```text
Path:       /workspace/tmp/bbb_annexb.h264
SHA-256:    1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
Format:     640x360, yuv420p, High profile, CABAC, three B-frames
Frames:     300
Reference:  FFmpeg 7.1.3
```

`scripts/bootstrap_fixtures.sh` verifies fixtures in `/workspace/tmp`. It can encode missing fixtures with the installed FFmpeg and libx264. The hash check rejects output from an incompatible toolchain, so retain a verified fixture when repeatable byte-for-byte generation matters.

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

Existing fast paths cover bit reading without emulation-prevention bytes, CAVLC prefix lookup, interior and axis-aligned motion compensation, integer-motion sub-rectangle copies, chroma row copies, zero-residual bypasses and direct frame-row writes.

Historical development-host BBB runs measured 44-52ms per decode after the allocation work described in Git history. Current benchmark results depend on the selected benchmark, fixture, hardware, Go version and build flags; record the complete command and compare results from the same host.

```bash
go test ./decode -run '^$' -bench BenchmarkDecode -benchmem
```

## Generate entropy tables

```bash
go generate ./entropy/cabac ./entropy/cavlc
```

The generators live under `internal/tables/` and use the `//go:build ignore` constraint. Generated CABAC and CAVLC tables are checked in.

## Development plan

`PLAN.md` lists tested scope, open decoder work, optimisation requirements and the encoder sequence.

## Licence

MIT
