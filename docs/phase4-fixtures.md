# Phase 4 external fixtures

These official H.264 conformance vectors remain outside Git. `scripts/bootstrap_phase4_fixtures.sh` downloads them from `https://fate-suite.ffmpeg.org/h264-conformance/` and verifies the input SHA-256 before use.

| Syntax family | Fixture | Input SHA-256 | FFmpeg 7.1.3 filtered YUV SHA-256 | FFmpeg 7.1.3 no-deblock YUV SHA-256 |
|---|---|---|---|---|
| Explicit weighted B prediction | `CVWP2_TOSHIBA_E.264` | `6b2b6205398d2cfebbee5708c4d4d8c67bb56cd4991f1cc4d90836a14215e257` | `33de403fa82d429124757614417fef90e4842ac058d1417f788cb2b33919a30b` | `4da3d73720c05a3df98953b1d34ba1145f89f9aa4be51f668f5e704d0b9cc4fd` |
| B-slice list modification | `HCMP1_HHI_A.264` | `f9bf6d36a7250dd86cf325cd458e92f3319236dcde075000e70f73028f05111a` | `9320cb8e1ee8d626c25ccb858c1800db5ad74ed7dcbd234c56258ae5d84aff7a` | `a795f3a4f0f12afd013bba1f0af59fbfdf407fb752c3eb4b16506f63351ed23e` |
| Long-term references (MMCO 3/4) | `MR1_BT_A.h264` | `20dc67331c81adcf40048bb37357883a69b3ab002b0927e599f43d86be9c3d8b` | `006f1add133b34369942f5ccfd350152aecfb010a2e7254ce3d9ef89234f0028` | `15aad2e0564afbe7a879dd2193cc7db76d741dce00d695732f6c50a07c90c577` |

Run the exact gate with:

```bash
./scripts/bootstrap_phase4_fixtures.sh
GO264_PHASE4_REGRESSION=1 \
GO264_FFMPEG_BIN=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
GO264_CONFORMANCE_ROOT=/workspace/tmp/h264-conformance \
go test ./cmd/decode264 -run TestFFmpegReferenceParityPhase4 -count=1 -v
```

The test independently verifies the oracle version, input hashes, FFmpeg output hashes and Go display-order output hashes. It tests filtering and `GO264_DISABLE_DEBLOCK=1` separately. The historical BBB fixture is a separate gate and is not substituted when its pinned bytes are unavailable.
