## Progressive state fixtures

These files contain only FFmpeg `testsrc2` output generated locally with FFmpeg 8.1.2 and libx264. They contain no downloaded media. `state_fixture_test.go` pins each input SHA-256, checks the SPS/NAL condition named by the fixture, decodes in presentation order and compares the visible YUV SHA-256 with FFmpeg 8.1.2 software decoding.

Regenerate exploratory candidates with:

```bash
ffmpeg -v error -f lavfi -i testsrc2=size=32x32:rate=10 -frames:v 12 \
  -c:v libx264 -profile:v high -qp 24 \
  -x264-params 'cabac=1:bframes=2:b-adapt=0:keyint=4:min-keyint=4:scenecut=0:threads=1' \
  -f h264 -y multiple-idr.h264

ffmpeg -v error -f lavfi -i testsrc2=size=32x32:rate=10 -frames:v 40 \
  -c:v libx264 -profile:v high -qp 24 \
  -x264-params 'cabac=1:bframes=0:keyint=100:min-keyint=100:scenecut=0:ref=1:threads=1' \
  -f h264 -y frame-poc-wrap.h264

ffmpeg -v error -f lavfi -i testsrc2=size=30x22:rate=5 -frames:v 5 \
  -c:v libx264 -profile:v high -qp 24 \
  -x264-params 'cabac=1:bframes=0:keyint=30:scenecut=0:threads=1' \
  -f h264 -y coded-edge-crop.h264
```

libx264 output can vary by version. Regeneration is documentation, not a replacement for the checked-in hash-pinned bytes. The exact reference command is:

```bash
ffmpeg -v error -i INPUT.h264 -pix_fmt yuv420p -f rawvideo -y reference.yuv
```

The decoder test hashes visible rows in Y, U, V order for every presentation-order frame. It does not store raw reference YUV in Git.
