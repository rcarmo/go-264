# Audio fixture policy

The current test fixtures are generated directly in Go tests from literal PCM extrema, deterministic integer sequences, tones, silence and impulses. They are authored for this repository and share its MIT licence. No recordings or private media are included in testdata. Separately attributed MIT codec tables live under the AAC internal packages.

`GO264_AUDIO_ORACLE=1 go test ./audio/wav -run TestFFmpegPCMOracle -count=1 -v` writes isolated temporary WAV fixtures and compares every decoded scalar to offline FFmpeg S32 output. It covers all 40 combinations of 8/16/24/32 bits, mono/stereo and 8/16/22.05/44.1/48 kHz. The fixture recipe is pinned by the test source commit; the command logs the exact FFmpeg version. No external download is required.

`TestCountsChunksSeek`, `TestPassbandAndAlias` and `TestImpulseSilenceCancel` in `audio/resample` cover exact finite output counts, chunk independence, seeking, passband/alias rejection, impulse alignment and zero signal. They are numerical unit gates, not speech-corpus or codec conformance qualification.

The AAC/MP4 opt-in tests record synthetic encode recipes and exercise the supported tool/timing subset. `public-fixtures.json` pins public source provenance, transcripts and annotated diarization input used by the optional go-pherence backend's paired frontend checks. Those tests are in consumer commit09d2db2; it pins providerfaa324c. No general AAC conformance, production ASR or meeting-corpus DER claim follows from the small fixture set. Retain source hashes, encode versions/commands and expected timing for any added corpus.
