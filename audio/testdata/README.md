# Audio fixture policy

The current test fixtures are generated directly in Go tests from literal PCM extrema, deterministic integer sequences, tones, silence and impulses. They are authored for this repository and share its MIT licence. No recordings, third-party codec tables or private media are included.

`GO264_AUDIO_ORACLE=1 go test ./audio/wav -run TestFFmpegPCMOracle -count=1 -v` writes isolated temporary WAV fixtures and compares every decoded scalar to offline FFmpeg S32 output. It covers all 40 combinations of 8/16/24/32 bits, mono/stereo and 8/16/22.05/44.1/48 kHz. The fixture recipe is pinned by the test source commit; the command logs the exact FFmpeg version. No external download is required.

`TestCountsChunksSeek`, `TestPassbandAndAlias` and `TestImpulseSilenceCancel` in `audio/resample` cover exact finite output counts, chunk independence, seeking, passband/alias rejection, impulse alignment and zero signal. They are numerical unit gates, not speech-corpus or codec conformance qualification.

Future AAC and public-speech fixtures must include origin URL, licence, source hash, exact encode command/version, ASC/container metadata, expected timing and accepted numerical bounds. Until those exist, no AAC conformance or WER/DER claim is accepted.
