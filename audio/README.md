# Audio frontend

The first implementation decodes PCM WAV to caller-owned S16 buffers, with streaming channel conversion and rational polyphase resampling. Progressive MP4 track demux and packet indexing are available separately. AAC-LC PCM decode, canonical-store checkpoints and SIMD are not implemented. A strict AAC-LC configuration validator and MIT-attributed scalar/FFT synthesis filterbank are development primitives, not a complete codec.

## Public contract

```go
dec, err := audio.Open(ctx, readerAt, size, audio.Options{}) // 16 kHz mono S16
// Handle err before use.
defer dec.Close()
meta := dec.Metadata()
buf := make([]int16, 4096)
for {
    n, span, err := dec.ReadPCM(ctx, buf)
    // Consume buf[:n] BEFORE checking err. n counts scalar samples;
    // span and meta count frames (one sample per channel).
    _ = span
    if err == io.EOF { break }
    if err != nil { return err }
}
_ = meta
```

`Probe(ctx, readerAt, size, pcm.Limits{})` validates metadata without reading PCM. Sources are immutable and caller-owned; `Close` never closes their underlying file. Reads, seeks and close are sequential. `Seek(ctx, canonicalFrame)` reproduces the resampler history from source samples, including near boundaries. Cancellation is checked at chunk/sample boundaries; arbitrary blocking `ReaderAt` implementations cannot be forcibly interrupted. Contexts must be non-nil. `Open` requires random access. `OpenStream(ctx, reader, privateTempDir, opts)` provides capped temporary spooling for non-seekable inputs; `Close` removes its owned spool without closing the caller's input. Spooling checks one extra byte beyond the cap without writing it and cleans up failed/cancelled copies. It is not a durable checkpoint.

The API is pre-release. Audio and video share the existing module version. No new module or dependency is introduced. go-pherence may consume this contract through an adapter; mel/Fbank features, inference, upload storage and job scheduling are outside this package.

## First-delivery matrix

| Input | Implemented now | Intended first delivery |
|---|---|---|
| RIFF/WAVE format 1 integer PCM | 8/16/24/32-bit mono/stereo, 8–192 kHz | Same qualified subset |
| Float, compressed or extensible WAV; RF64/RIFX | Typed unsupported error | Unsupported until qualified |
| MP4/M4A/MOV AAC-LC | `audio/mp4.Open` reads progressive packets; high-level PCM open still unsupported | Progressive, explicit mono/stereo profile subset |
| HE-AAC/SBR/PS, ALAC, DRM, fragmented MP4 | Unsupported | Explicit rejects |

The reader rejects duplicate `fmt`/`data`, data before format, invalid sizes/alignment, partial frames and trailing bytes outside RIFF. Default limits: 512 MiB, four hours and 65,536 chunks. Negative limits fail. PCM files above four hours fail even when their byte size fits. No whole-file or caller-sized scratch allocation is used.

## PCM and timing

- Low-level `ReadFrames` yields normalised interleaved float64 and returns frame counts. Public `ReadPCM` yields S16 and returns scalar sample counts.
- Mono/stereo layouts only. Stereo-to-mono is `(L+R)/2`; mono-to-stereo duplicates samples. Unknown layouts fail.
- Integer conversion clips, rounds ties away from zero and applies no dither. Non-finite PCM fails. Same-rate S16 WAV roundtrips exactly.
- Output frame count is `ceil(sourceFrames*outRate/inRate)`. Timestamps use checked integer ratios, never accumulated float time.
- Resampling uses an exact rational phase table, Blackman-windowed sinc, 94% Nyquist cutoff and zero extension. It aligns output frame zero with source time zero and removes filter delay from the output timeline. `LookaheadFrames` reports input lookahead; metadata delay is zero after compensation.
- Output rates 8–48 kHz, at most 2,048 rational phases. Common 8/16/22.05/44.1/48/96/192 kHz source rates are accepted. Unusual ratios above the phase cap fail.
- The resampler is scalar. Frequency sweeps, broader corpus comparison, performance tuning and end-to-end WER/DER qualification are still required.
- Priming/padding fields are zero for WAV. `mp4.Track.TimingPlan(actualDecodedFrames)` computes an exact integer trim plan for no edits, one unit-rate media edit, or an initial empty edit plus media edit. Fractional-sample edits, repeats and rate changes fail explicitly. This plan is not yet applied by an AAC PCM decoder.

## Package isolation

`audio/pcm` is the shared leaf. `audio/wav`, `audio/convert` and `audio/resample` are independently importable. `audio` composes them. `audio/aac` validates a narrow AAC-LC AudioSpecificConfig subset but does not decode PCM. `audio/mp4.Open` selects the first accepted progressive AAC-LC mono/stereo track, validates self-contained data references, indexes stsc/stsz/stz2/stco/co64/stts/ctts and returns caller-buffer packets. Edits are copied as metadata; `TimingPlan` separately validates the supported integer subset. QuickTime versioned audio entries, description switching, fragmentation and protected tracks fail. Limits bound tables, tracks, packets, duration and descriptor nesting. There are no video, CLI, go-pherence, native-code, subprocess or third-party Go module runtime dependencies.

`TestAudioImportBoundary` checks all runtime Go imports, including inactive architecture files. Run focused tests with `CGO_ENABLED=0 go test ./audio/...`; these do not require video fixtures.

## Audio-only command

`CGO_ENABLED=0 go build ./cmd/decodeaudio` builds an audio-only consumer with no video imports. Run `decodeaudio -rate 16000 -channels 1 input.wav > output.s16le`. The command owns the input file and honours interrupt cancellation; output is raw little-endian PCM, not a WAV container.

## Licensing

New dependencies must be MIT-licensed. A non-MIT implementation can be replaced only through a genuine clean-room process; translating it to Go or assembly does not change its provenance. No FAAD2/LGPL/Apache implementation is included. The development filterbank, bitreader and Huffman primitives are attributed MIT ports from OxideAV at `7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885`; their MIT notices are retained in each internal package. `go generate ./audio/aac/internal/huffman` regenerates 1,241 spectral/121 scalefactor entries from checksum-pinned upstream sources. Set `GO264_MIT_AAC_REFERENCE` to a checkout of that revision when regenerating outside the development workspace. Its FFT reduction is derived from the direct cosine equation and checked against that scalar oracle. These primitives do not form a complete AAC decoder or establish codec conformance. Exhaustive table tests caught and now guard against mistakenly extracting numeric tuples from comments.

## Fixtures and gates

Unit fixtures are synthetic, authored in the tests and distributed under the repository licence. They contain literal PCM extrema, tones, impulses, silence and malformed containers. No private recordings are used. WAV PCM requires exact results; AAC tolerances and public-speech WER/DER gates must be frozen before AAC acceptance. FFmpeg may generate offline oracle output, never serve runtime decoding.

Eight synthetic FFmpeg M4A cases (mono/stereo, 44.1/48kHz, front/tail moov) check every packet's bytes, offset, size and unedited timestamp against ffprobe. They do not qualify decoded AAC PCM. Filterbank tests compare FFT output to the direct equation across window sequences, shapes and coefficient scales; original-media or speech quality is unqualified.

Required future gates: wider MP4/timing qualification, complete qualified AAC-LC tools and flush, gapless accounting, fuzz/resource hardening, durable resume, public adapter quality tests, measured SIMD, whole-repository regression checks and reproducible performance measurements. Provisional decode ≥50× and resample ≥100× realtime targets are not achieved claims.
