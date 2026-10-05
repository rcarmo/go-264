#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/project-env.sh"
go264_init_paths cabac-parity-baseline

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INPUT="${1:-$GO264_FIXTURE_ROOT/testsrc_cabac_p.h264}"
OUTDIR="${2:-$GO264_RUN_ROOT/cabac-parity/$(date -u +%Y%m%dT%H%M%SZ)-$$}"
FFSRC="${FFMPEG_SRC:-$GO264_CACHE_ROOT/ffmpeg/ffmpeg-7.1.3}"
FFBUILD="$GO264_BUILD_ROOT/ffmpeg-7.1.3"
FFMPEG="${FFMPEG:-${GO264_FFMPEG_BIN:-}}"
mkdir -p "$OUTDIR/go" "$OUTDIR/ffmpeg" "$TMPDIR" "$GOTMPDIR"

has_rawvideo_encoder() {
  "$1" -hide_banner -encoders 2>/dev/null | grep -q '^ V..... rawvideo'
}

if [[ -z "$FFMPEG" ]]; then
  if command -v ffmpeg >/dev/null 2>&1 && has_rawvideo_encoder "$(command -v ffmpeg)"; then
    FFMPEG="$(command -v ffmpeg)"
  elif [[ -x "$FFBUILD/ffmpeg" ]] && has_rawvideo_encoder "$FFBUILD/ffmpeg"; then
    FFMPEG="$FFBUILD/ffmpeg"
  else
    echo "ffmpeg not found with rawvideo encoder; building a minimal local binary in $FFBUILD" >&2
    mkdir -p "$FFBUILD"
    (cd "$FFBUILD" && "$FFSRC/configure" \
      --disable-x86asm --disable-doc --disable-debug --disable-network \
      --disable-everything --enable-ffmpeg \
      --enable-protocol=file --enable-demuxer=h264 --enable-parser=h264 \
      --enable-decoder=h264 --enable-encoder=rawvideo --enable-muxer=rawvideo --enable-muxer=null \
      --enable-filter=null --enable-filter=showinfo >"$OUTDIR/ffmpeg-configure.log" && \
      make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >"$OUTDIR/ffmpeg-build.log")
    FFMPEG="$FFBUILD/ffmpeg"
  fi
fi


cd "$ROOT"
echo "input=$INPUT" | tee "$OUTDIR/summary.txt"
echo "ffmpeg=$FFMPEG" | tee -a "$OUTDIR/summary.txt"

go run ./cmd/decode264 -i "$INPUT" -o "$OUTDIR/go" -f yuv 2>&1 | tee "$OUTDIR/go/decode-yuv.log"
go run ./cmd/decode264 -i "$INPUT" -o "$OUTDIR/go" -f color 2>&1 | tee "$OUTDIR/go/decode-png.log"

DIM=$(sed -n 's/^Stream: \([0-9][0-9]*x[0-9][0-9]*\),.*/\1/p' "$OUTDIR/go/decode-yuv.log" | tail -1)
if [[ -z "$DIM" ]]; then
  echo "could not determine decoded dimensions from Go log" >&2
  exit 1
fi
W="${DIM%x*}"
H="${DIM#*x}"
echo "dimensions=${W}x${H}" | tee -a "$OUTDIR/summary.txt"

"$FFMPEG" -hide_banner -y -i "$INPUT" -frames:v 1 -pix_fmt yuv420p -f rawvideo "$OUTDIR/ffmpeg/frame_0000.yuv" \
  >"$OUTDIR/ffmpeg/decode.log" 2>&1 || { cat "$OUTDIR/ffmpeg/decode.log" >&2; exit 1; }
"$FFMPEG" -hide_banner -i "$INPUT" -vf showinfo -frames:v 1 -f null - \
  >"$OUTDIR/ffmpeg/showinfo.log" 2>&1 || true

python3 - "$OUTDIR/go/frame_0000.yuv" "$OUTDIR/ffmpeg/frame_0000.yuv" "$W" "$H" <<'PY' | tee "$OUTDIR/psnr.txt"
import math, sys
from pathlib import Path

go = Path(sys.argv[1]).read_bytes()
ref = Path(sys.argv[2]).read_bytes()
w = int(sys.argv[3]); h = int(sys.argv[4])
ys = w*h
cs = (w//2)*(h//2)
planes = [("Y",0,ys), ("U",ys,cs), ("V",ys+cs,cs)]
for name, off, n in planes:
    a = go[off:off+n]
    b = ref[off:off+n]
    if len(a) != n or len(b) != n:
        print(f"{name}=NaN short plane go={len(a)} ref={len(b)} want={n}")
        continue
    mse = sum((x-y)*(x-y) for x,y in zip(a,b)) / n
    psnr = 99.0 if mse < 1e-10 else 10*math.log10((255*255)/mse)
    maxd = max((abs(x-y) for x,y in zip(a,b)), default=0)
    print(f"{name}={psnr:.2f}dB maxdiff={maxd}")
PY
cat "$OUTDIR/psnr.txt" >> "$OUTDIR/summary.txt"

echo "go_yuv=$OUTDIR/go/frame_0000.yuv" | tee -a "$OUTDIR/summary.txt"
echo "go_png=$OUTDIR/go/frame_0000.png" | tee -a "$OUTDIR/summary.txt"
echo "ffmpeg_yuv=$OUTDIR/ffmpeg/frame_0000.yuv" | tee -a "$OUTDIR/summary.txt"
echo "ffmpeg_showinfo=$OUTDIR/ffmpeg/showinfo.log" | tee -a "$OUTDIR/summary.txt"
echo "summary=$OUTDIR/summary.txt"
