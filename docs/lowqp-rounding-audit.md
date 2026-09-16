# Low-QP 8x8 inverse scaling regression

Baseline: upstream master `8c844f3`. Branch: `fix/bounded-cabac`.
Reference: FFmpeg 8.1.2 (Homebrew, libx264 enabled), software H.264 decode,
8-bit YUV420P, default loop filtering. Decoder: NewDecoder defaults, all frames,
presentation ordering by FullPOC. No error tolerance.

## Fixture and reproduction

`decode/testdata/cabac-b-lowqp.h264` contains only synthetic FFmpeg testsrc2
imagery, generated locally; no downloaded film or third-party media.
Input SHA256: `a43c35d1cd83195c4af4ad010aebf0da77fe174ec49194b5996eb5fcb238af4b`.
It is the first four VCL NALs (plus original SPS/PPS/SEI) of:

```sh
ffmpeg -v error -f lavfi -i testsrc2=size=128x96:rate=10 -frames:v 12 \
  -c:v libx264 -qp 6 \
  -x264-params 'cabac=1:bframes=3:b-adapt=0:threads=1:keyint=30' \
  -y input.h264
```

Original hash: `71eb7e4167befb0ece3f07598b68517c78671b1c8e1233c2f7b5d079ae0c2b13`.
Encoding can vary with libx264 version; use the checked-in hashed bytes for
reproduction. Three VCL pictures did not reproduce; four do.

```sh
go run ./cmd/decode264 -i decode/testdata/cabac-b-lowqp.h264 -o /tmp/go264-lowqp -f yuv
cat /tmp/go264-lowqp/frame_*.yuv > /tmp/go264-lowqp.yuv
ffmpeg -v error -i decode/testdata/cabac-b-lowqp.h264 -pix_fmt yuv420p \
  -f rawvideo -y /tmp/go264-reference.yuv
cmp /tmp/go264-lowqp.yuv /tmp/go264-reference.yuv
go test ./decode ./transform -run 'TestCABACBLowQPExact|TestDequant8x8Rounding'
```

Baseline output SHA256: `470addf6673f97a3ba641a30f823a81f60c6fde5f191fd0830a9578926b089f8`.
Reference/fixed output SHA256: `54bdddd49d3ec6f13f6147abb300f1d96e3e0159944cc7142800ad667cb3944b`.
Only presentation frame 1 differs: luma offsets 3139, 3776, 3908 have
(reference, baseline) values (21,22), (52,51), (45,46).

## Diagnosis

Disabling both decoders' loop filters preserved the first mismatch. Disabling
Go transform SIMD also preserved it. The mismatch is not permissible tolerance:
H.264 integer reconstruction here is deterministic.

Temporary instrumentation immediately before/after Dequant8x8 identified POC 2,
MB (4,1), group 2, QP 8, with these nonzero raster coefficients:
`0:3, 2:1, 8:-4, 16:2, 17:-1, 24:-1, 25:1`.
At coefficient index 2 the scale factor is 33 and QP quotient is 1:
`level * factor << quotient = 66`. Baseline divides by four, yielding 16;
rounded inverse scaling yields `(66+2)>>2 = 17`.
This is the first differing coefficient in that block's inverse-scaling stage.
The trace instrumentation was removed from the patch.

H.264 section 8.5's 8x8 inverse scaling process requires adding the rounding
term before the right shift for low QP. With the supported flat scaling list,
FFmpeg n8.1.2 `libavcodec/h264_ps.c:594-617` constructs
`qmul = factor * 16 << (QP/6)`, and `h264_cabac.c:1733,1760` computes
`(signed_level*qmul + 32)>>6`. This reduces exactly to `(scaled+2)>>2`.
Signed truncating division in Go is not equivalent. The old explanatory comment
incorrectly asserted that it matched FFmpeg.

Evidence limitation: this is a reference-source arithmetic comparison and a
Go coefficient trace, not a dual-decoder full CABAC-bin trace. No claim is made
that every earlier syntax/state value was independently traced. Fixing only
this arithmetic makes every sample match in both failing sequences (QP 1,6).

## Verification

- New tests fail on the original transform: exact output hash fails; +1 and +3
  unit coefficients produce 16/49 instead of 17/50.
- With the one-line arithmetic change, both regression tests pass.
- `go test ./...`, `go vet ./...`, `go test -race ./decode ./transform`, and
  `git diff --check` pass.
- 19 generated lossy streams match FFmpeg byte-for-byte: single-frame CABAC
  16/32/48/64 square pixels at QP 12/26/40 (12 streams); eight-frame B sequences
  16/32/64 square at QP 26 (3); twelve-frame 128x96 B sequences at QP 1/6/18/38 (4).
- Existing external-corpus tests skip because /workspace/tmp fixtures are absent:
  gray16, dark64, baseline CAVLC, BBB, plane references and CABAC trace fixture.
  Thus the historical BBB exact gate was NOT rerun. All available built-in tests
  passed. No full external conformance corpus, ARM64 execution or fuzz campaign.
- A separate QP=0 lossless-transform-bypass probe fails; that unsupported path
  is not addressed. No general H.264 conformance claim.
- Independent delegate review was attempted but blocked by the session's
  unclassified model/approved delegate selection policy; no review result exists.
