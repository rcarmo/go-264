# Audio frontend

The audio frontend decodes integer PCM WAV and a narrow progressive MP4/AAC-LC subset to caller-owned S16 buffers. It includes streaming channel conversion, rational polyphase resampling, explicit integral edit/trim handling and an optional hashed checkpoint store. Exact amd64 SSE2/AVX2 and selected ARM64 NEON kernels cover measured numeric paths, with scalar references and a `purego` fallback. [SIMD coverage and measurements](SIMD.md) list the implementation and remaining serial work. The decoder is pre-release; only the qualified cases below are supported.

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

`Probe(ctx, readerAt, size, pcm.Limits{})` validates metadata without decoding PCM and returns the edited source-rate extent. `ProbeMetadata` additionally returns the pre-trim decoded extent, priming, padding and leading silence; callers must not pass the edited `Probe` count back to `mp4.Track.TimingPlan`, whose argument is the pre-trim decoded count. Invalid or unsupported AAC payloads may still fail when read. Sources are immutable and caller-owned; `Close` never closes their underlying file. Reads, seeks and close are sequential. `Seek(ctx, canonicalFrame)` reproduces the resampler history from source samples, including near boundaries. AAC reads after seeks replay earlier access units as needed for exact PNS/overlap state, so random access can be linear in stream length. Cancellation is checked per replayed access unit; no fast-seek claim is made. Cancellation is checked at chunk/sample boundaries; arbitrary blocking `ReaderAt` implementations cannot be forcibly interrupted. Contexts must be non-nil. `Open` requires random access. `OpenStream(ctx, reader, privateTempDir, opts)` provides capped temporary spooling for non-seekable inputs; `Close` removes its owned spool without closing the caller's input. Spooling checks one extra byte beyond the cap without writing it and cleans up failed/cancelled copies. It is not a durable checkpoint.

The API is pre-release. Audio and video share the existing module version. No new module or dependency is introduced. go-pherence may consume this contract through an adapter; mel/Fbank features, inference, upload storage and job scheduling are outside this package.

## First-delivery matrix

| Input | Implemented now | Intended first delivery |
|---|---|---|
| RIFF/WAVE format 1 integer PCM | 8/16/24/32-bit mono/stereo, 8–192 kHz | Same qualified subset |
| Float, compressed or extensible WAV; RF64/RIFX | Typed unsupported error | Unsupported until qualified |
| MP4/M4A/MOV AAC-LC | Progressive AAC-LC mono/stereo, 1024/128, integral unit-rate edits via `Open`/`OpenStream` | Qualified subset; unsupported tools fail during reads |
| HE-AAC/SBR/PS, ALAC, DRM, fragmented MP4 | Unsupported | Explicit rejects |

The reader rejects duplicate `fmt`/`data`, data before format, invalid sizes/alignment, partial frames and trailing bytes outside RIFF. Default limits: 512 MiB, four hours and 65,536 chunks. Negative limits fail. PCM files above four hours fail even when their byte size fits. No whole-file or caller-sized scratch allocation is used.

## PCM and timing

- Low-level `ReadFrames` yields normalised interleaved float64 and returns frame counts. Public `ReadPCM` yields S16 and returns scalar sample counts.
- Mono/stereo layouts only. Stereo-to-mono is `(L+R)/2`; mono-to-stereo duplicates samples. Unknown layouts fail.
- Integer conversion clips, rounds ties away from zero and applies no dither. Non-finite PCM fails. Same-rate S16 WAV roundtrips exactly.
- Output frame count is `ceil(sourceFrames*outRate/inRate)`. Timestamps use checked integer ratios, never accumulated float time.
- Resampling uses an exact rational phase table, Blackman-windowed sinc and zero extension. Downsampling uses a 94% destination-Nyquist transition; upsampling uses input-Nyquist cutoff so integer phases reproduce source samples. It aligns output frame zero with source time zero and removes filter delay from the output timeline. `LookaheadFrames` reports input lookahead; metadata delay is zero after compensation.
- Output rates 8–48 kHz, at most 2,048 rational phases. Common 8/16/22.05/44.1/48/96/192 kHz source rates are accepted. Unusual ratios above the phase cap fail.
- The common contiguous mono FIR uses checked SSE2 and AVX2 kernels on amd64. AVX2 dispatch requires CPUID AVX2, OSXSAVE and XCR0 XMM/YMM state. Products are accumulated in source order; no FMA, horizontal reduction or alternate accumulator changes rounding. Edges, ring wrap, stereo and `-tags=purego` retain scalar paths. Odd tails, unaligned buffers and protected-page bounds are tested.
- Priming/padding fields are zero for WAV. `mp4.Track.TimingPlan(actualDecodedFrames)` computes an exact integer trim plan for no edits, one unit-rate media edit, or an initial empty edit plus media edit. Fractional-sample edits, repeats and rate changes fail explicitly. The public MP4 path applies this plan while streaming, validates even fully trimmed trailing access units, and includes empty-edit silence in output. `Metadata.Source.Frames` is pre-trim coded-frame count; `Output.Frames` is the edited/resampled length. `LeadingSilenceFrames` uses source-rate units. `Span.SourceStartFrame` is the floored pre-trim PCM coordinate after subtracting leading silence and adding priming; spans starting in leading silence report `-1` and `SourcePadding=true`. It describes only the span start, not every FIR contributing sample.

## Package isolation

`audio/pcm` is the shared leaf. `audio/wav`, `audio/convert` and `audio/resample` are independently importable. `audio` composes them. `audio/aac` parses AAC-LC mono/stereo1024/128 access units and reconstructs spectra, PNS, M/S, intensity and TNS through the FFT filterbank. `Decode(ctx, packet, dst)` returns1024 frames on success; errors leave output and decoder state unchanged. Callers apply container timing and trimming. CCE/LFE/PCE, prediction, gain control, SBR/PS and unknown fill extensions are explicitly unsupported. PNS noise vectors remain independent per channel even when a common-window M/S mask covers both noise bands; ordinary M/S transformation excludes noise codebooks. Five-frame per-band/all-band mask oracles match FFmpeg within `1.1e-10` maximum absolute PCM error. `audio/mp4.Open` selects the first accepted progressive AAC-LC mono/stereo track, validates self-contained data references, indexes stsc/stsz/stz2/stco/co64/stts/ctts and returns caller-buffer packets. Edits are copied as metadata; `TimingPlan` separately validates the supported integer subset. QuickTime versioned audio entries, description switching, fragmentation and protected tracks fail. Limits bound tables, tracks, packets, duration and descriptor nesting. There are no video, CLI, go-pherence, native-code, subprocess or third-party Go module runtime dependencies.

`TestAudioImportBoundary` checks all runtime Go imports, including inactive architecture files. Run focused tests with `CGO_ENABLED=0 go test ./audio/...`; these do not require video fixtures. `.github/workflows/audio.yml` runs default and `purego` tests, vet, the full race detector and synthetic FFmpeg oracle matrix on amd64, plus default and `purego` tests and vet on a native Linux ARM64 runner. Default native tests enforce exact NEON parity. Cross-builds and QEMU remain functional checks, not native architecture evidence.

## Canonical PCM checkpoints

`audio/store` is an optional single-writer store in a caller-owned private directory. Create it with a source SHA256, exact decoder/configuration identifier and PCM metadata; append interleaved S16 segments up to1MiB. Segment files are synced and installed with no-clobber links, then the manifest is replaced atomically and its directory synced. An uncertain final sync returns `ErrUncertainDurability` and makes that instance unusable until reopened.

`Open` validates contiguous counts and committed segment hashes before exposing `Frames()`. Resume by re-verifying source identity, reopening the same decoder configuration and seeking to that canonical frame. Orphan segments are not counted; identical orphans can be reused, conflicting bytes are not overwritten. There is no concurrent-writer/symlink sandbox guarantee, nor hardware power-loss certification. Tests inject cancellation/sync failures and verify WAV replay-to-checkpoint equivalence. Upload retention and admission/disk quotas belong to the application.

## Audio-only command

`CGO_ENABLED=0 go build ./cmd/decodeaudio` builds an audio-only consumer with no video imports. Run `decodeaudio -rate 16000 -channels 1 input.wav > output.s16le`. The command owns the input file and honours interrupt cancellation; output is raw little-endian PCM, not a WAV container.

## Licensing

The entire go-264 project, including these audio packages, is licensed under the root [MIT License](../LICENSE). Imported material retains its upstream copyright and MIT notices; see [third-party notices](../THIRD_PARTY_NOTICES.md). Referenced external recordings keep their separately documented dataset licences.

New dependencies must be MIT-licensed. A non-MIT implementation can be replaced only through a genuine clean-room process; translating it to Go or assembly does not change its provenance. No FAAD2/LGPL/Apache implementation is included. The filterbank, bitreader and Huffman primitives are attributed MIT ports from OxideAV at `7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885`; their MIT notices are retained in each internal package. `go generate ./audio/aac/internal/huffman` regenerates 1,241 spectral codewords with decoded tuples and 121 scalefactor entries from checksum-pinned upstream sources. Set `GO264_MIT_AAC_REFERENCE` to a checkout of that revision when regenerating outside the development workspace. The FFT reduction is derived from the direct cosine equation and checked against that scalar oracle. `internal/lc` parser and reconstruction code retains MIT attribution. A corrected first-noise-delta rule avoids sign-extending a value whose bias was already applied; synthetic PCM regression tests verify the result. This narrow decoder does not establish general AAC conformance.

## Fixtures and gates

Unit fixtures are synthetic, authored in the tests and distributed under the repository licence. They contain literal PCM extrema, tones, impulses, silence and malformed containers. No private recordings are used. WAV PCM requires exact results. AAC acceptance is limited to the declared mono/stereo AAC-LC subset and the oracle/public-speech evidence below; broader encoder and corpus coverage is optional qualification, not permission to accept unsupported tools. FFmpeg may generate offline oracle output, never serve runtime decoding.

Eight synthetic FFmpeg M4A cases (mono/stereo, 44.1/48kHz, front/tail moov) check every packet's bytes, offset, size and unedited timestamp against ffprobe. Eight additional source-rate public-API MP4 PCM tests check explicit priming/end trim, caller chunking, exact backward/forward/EOF seek and S16 error≤0.501LSB against FFmpeg's retained media interval. FFmpeg's raw output includes end padding beyond that interval; total lengths are compared to the declared edit plan rather than silently retaining it. Forty-eight separately decoded mono/stereo cases at8/16/22.05/24/32/44.1/48/96kHz across tone/transient/PNS fixtures require SNR≥70dB and maxabs≤1e-5 against FFmpeg float PCM. A second eight-case, 1.25-second matrix varies 16–96kHz, mono/stereo,24–192kb/s and encoder PNS/M/S/intensity/TNS policies; all cases exceed137dB SNR and maxabs stays below1.53e-7. Four handcrafted TNS cases cover both directions and coefficient signs (maxabs<8e-11). Six intensity-stereo sign/M/S-mode cases and independent or common-window masked stereo PNS are checked against FFmpeg. The optional go-pherence backend now passes scoped paired public-speech checks: six MINDS PCM/AAC pairs have identical Tiny windows after the interpolation correction; one annotated30s sample has DERdelta0pp(WAV)/+0.08514pp(AAC) within0.5pp. The DER run overlaps six seconds of another session's setup, so its timings are not isolated evidence. See `testdata/public-fixtures.json` for hashes, licences and limits. This does not establish broad codec, production ASR or meeting quality. Filterbank tests compare FFT output to the direct equation across window sequences, shapes and coefficient scales.

Four-hour sparse-WAV and actual SIGKILL checkpoint stages pass along with scalar/assembly, malformed-input and replay tests. The full `audio/...` race suite passes on linux/amd64 in Go 1.26.2. Synthetic two-second canonical decode measures about 138× realtime and isolated resampling about 750×; these exceed the provisional 50×/100× targets on those fixtures only. Generalisation to other encoders, tools, long workloads and physical power-loss behaviour has not been established. No tag or serving-default switch has been made.
