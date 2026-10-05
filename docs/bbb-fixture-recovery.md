# Historical BBB fixture recovery

The 300-frame `bbb_annexb.h264` regression requires SHA-256 `1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff`. Its exact bytes have not been recovered. Keep `$GO264_FIXTURE_ROOT/bbb_annexb.h264` empty until a candidate passes that input-integrity check. `scripts/fixture_gate_status.sh --strict` must continue to report the missing fixture.

On 5 October 2026, a search of local assets, Git history, notes, public hash results and known backup pointers found no copy. The verified Blender source is retained outside Git:

| Asset | Retained location | SHA-256 |
|---|---|---|
| Source ZIP | `/workspace/reports/go-264/fixtures/sources/BigBuckBunny_640x360.m4v.zip` | `7118242b6728d40c871479c5b3c0f0fb27d748089df15d7f1b469f297c74a2d6` |
| Extracted source (ZIP member) | `BigBuckBunny_640x360.m4v` | `738e2f999860553d056dd79c952f58f63cbb73892a57c72342ce9e5330d9d2d7` |

Source URL: `https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_640x360.m4v.zip`. The ZIP is retained media, not a disposable build cache. The extracted member was verified against the source hash in `scripts/bootstrap_fixtures.sh`.

The documented 300-frame libx264 recipe in that script was tried with one encoder thread, bitexact flags, High profile, CRF 23 and three B-frames. Neither result identifies the pinned historical input:

| Encoder | Diagnostic SHA-256 | Bytes |
|---|---|---:|
| FFmpeg 8.1.2, libx264 0.165.3222 | `42e233436ccdd4193513734a97774c1a6cfb5f119e0272d8054cd068daaf0d7d` | 828,641 |
| FFmpeg 7.1.3, libx264 0.165.3222 | `63665e3d7d6285b6ff42dbcc5d43cf1fcd205968b13e7f284ad539684e0d6db4` | 797,551 |

Concise provenance and the two diagnostic hashes are recorded here. The failed/probe bitstreams, raw logs and strict-status captures are disposable after analysis; do not archive them as evidence. Their hashes identify the attempts but do **not** establish numerical decoder equivalence or replace the historical fixture. The four separately pinned official conformance vectors live in `/workspace/reports/go-264/fixtures/h264-conformance/` and pass their own exact FFmpeg gate.

If a backup yields `bbb_annexb.h264`, verify its SHA-256 **before** copying it to the retained fixture directory. Then run `make fixture-status-strict` with a suitable `GO264_FFMPEG_BIN` and `TestFFmpegReferenceParityBBB` as documented in [README.md](../README.md#ffmpeg-parity-test). During pre-release verification, also run `make prerelease-profile` and analyse CPU and allocation behaviour before the helper deletes raw captures. Do not change the pinned hash or use `ALLOW_FIXTURE_HASH_MISMATCH=1` for acceptance.
