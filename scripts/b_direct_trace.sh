#!/usr/bin/env bash
set -euo pipefail

INPUT="${1:-/workspace/tmp/bbb_annexb.h264}"
OUTDIR="${2:-/workspace/tmp/go264-b-direct-trace}"
FRAMES="${FRAMES:-10}"
MB_LIMIT="${MB_LIMIT:-40}"
if ! [[ "$FRAMES" =~ ^[0-9]+$ ]] || (( FRAMES < 1 )); then
  echo "FRAMES must be a positive integer, got: $FRAMES" >&2
  exit 2
fi
if ! [[ "$MB_LIMIT" =~ ^[0-9]+$ ]] || (( MB_LIMIT < 1 )); then
  echo "MB_LIMIT must be a positive integer, got: $MB_LIMIT" >&2
  exit 2
fi
FFSRC="${FFMPEG_SRC:-/workspace/tmp/ffmpeg-7.1.3}"
FFMPEG="${FFMPEG:-$FFSRC/ffmpeg}"

patch_ffmpeg_direct_trace() {
  python3 - "$FFSRC/libavcodec/h264_direct.c" "$MB_LIMIT" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
mb_limit = sys.argv[2]
s = p.read_text()
if '#include <stdlib.h>' not in s:
    s = s.replace('#include "h264dec.h"', '#include "h264dec.h"\n#include <stdlib.h>\n#include <stdio.h>')
signature = '''void ff_h264_pred_direct_motion(const H264Context *const h, H264SliceContext *sl,
                                int *mb_type)'''
new = f'''void ff_h264_pred_direct_motion(const H264Context *const h, H264SliceContext *sl,
                                int *mb_type)
{{
    if (sl->direct_spatial_mv_pred)
        pred_spatial_direct_motion(h, sl, mb_type);
    else
        pred_temp_direct_motion(h, sl, mb_type);
    int mb = sl->mb_x + sl->mb_y * h->mb_width;
    if (getenv("GO264_FFMPEG_DIRECT_TRACE") && mb < {mb_limit}) {{
        int s0 = scan8[0];
        int s1 = scan8[4];
        int s2 = scan8[8];
        int s3 = scan8[12];
        int a = scan8[0] - 1;
        int b = scan8[0] - 8;
        int c = scan8[0] - 8 + 4;
        int d = scan8[0] - 8 - 1;
        fprintf(stderr,
                "FFDIRECT mb=%04d x=%02d y=%02d frame=%d spatial=%d mb_type=%d "
                "ref0=%d ref1=%d mv0={{%d,%d}} mv1={{%d,%d}} "
                "ctxA0=%d/{{%d,%d}} ctxB0=%d/{{%d,%d}} ctxC0=%d/{{%d,%d}} ctxD0=%d/{{%d,%d}} "
                "ctxA1=%d/{{%d,%d}} ctxB1=%d/{{%d,%d}} ctxC1=%d/{{%d,%d}} ctxD1=%d/{{%d,%d}} "
                "sub0=%d sub1=%d sub2=%d sub3=%d "
                "submv0={{%d,%d}} submv1={{%d,%d}} submv2={{%d,%d}} submv3={{%d,%d}}\\n",
                mb, sl->mb_x, sl->mb_y, h->poc.frame_num, sl->direct_spatial_mv_pred, *mb_type,
                sl->ref_cache[0][s0], sl->ref_cache[1][s0],
                sl->mv_cache[0][s0][0], sl->mv_cache[0][s0][1],
                sl->mv_cache[1][s0][0], sl->mv_cache[1][s0][1],
                sl->ref_cache[0][a], sl->mv_cache[0][a][0], sl->mv_cache[0][a][1],
                sl->ref_cache[0][b], sl->mv_cache[0][b][0], sl->mv_cache[0][b][1],
                sl->ref_cache[0][c], sl->mv_cache[0][c][0], sl->mv_cache[0][c][1],
                sl->ref_cache[0][d], sl->mv_cache[0][d][0], sl->mv_cache[0][d][1],
                sl->ref_cache[1][a], sl->mv_cache[1][a][0], sl->mv_cache[1][a][1],
                sl->ref_cache[1][b], sl->mv_cache[1][b][0], sl->mv_cache[1][b][1],
                sl->ref_cache[1][c], sl->mv_cache[1][c][0], sl->mv_cache[1][c][1],
                sl->ref_cache[1][d], sl->mv_cache[1][d][0], sl->mv_cache[1][d][1],
                sl->sub_mb_type[0], sl->sub_mb_type[1], sl->sub_mb_type[2], sl->sub_mb_type[3],
                sl->mv_cache[0][s0][0], sl->mv_cache[0][s0][1],
                sl->mv_cache[0][s1][0], sl->mv_cache[0][s1][1],
                sl->mv_cache[0][s2][0], sl->mv_cache[0][s2][1],
                sl->mv_cache[0][s3][0], sl->mv_cache[0][s3][1]);
    }}
}}'''
start = s.find(signature)
if start < 0:
    raise SystemExit('ffmpeg h264_direct.c direct-motion hook target not found')
brace = s.find('{', start)
if brace < 0:
    raise SystemExit('ffmpeg h264_direct.c malformed direct-motion function')
depth = 0
end = None
for i in range(brace, len(s)):
    if s[i] == '{':
        depth += 1
    elif s[i] == '}':
        depth -= 1
        if depth == 0:
            end = i + 1
            break
if end is None:
    raise SystemExit('ffmpeg h264_direct.c unterminated direct-motion function')
# Replace the whole function, whether it is pristine or has an older trace patch.
p.write_text(s[:start] + new + s[end:])
PY
}

mkdir -p "$OUTDIR"
patch_ffmpeg_direct_trace
(cd "$FFSRC" && make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >/tmp/go264-ffmpeg-direct-build.log)
GO264_FFMPEG_DIRECT_TRACE=1 "$FFMPEG" -y -threads 1 -hide_banner \
  -i "$INPUT" -frames:v "$FRAMES" -pix_fmt yuv420p -f rawvideo /dev/null \
  >"$OUTDIR/ffmpeg.stdout" 2>"$OUTDIR/ffmpeg.direct.trace" || true

grep '^FFDIRECT' "$OUTDIR/ffmpeg.direct.trace" >"$OUTDIR/ffdirect.rows" || true
python3 - "$OUTDIR/ffdirect.rows" <<'PY'
import re, sys
from collections import Counter
rows = []
pat = re.compile(r'FFDIRECT mb=(\d+)(?: x=(\d+) y=(\d+))? frame=(\d+) spatial=(\d+) mb_type=([^ ]+) ref0=([^ ]+) ref1=([^ ]+) mv0=\{(-?\d+),(-?\d+)\} mv1=\{(-?\d+),(-?\d+)\}')
for line in open(sys.argv[1], errors='replace'):
    m = pat.search(line)
    if m:
        mb, x, y, frame, spatial, mbtype, ref0, ref1, mv0x, mv0y, mv1x, mv1y = m.groups()
        rows.append({
            'mb': int(mb), 'frame': int(frame), 'spatial': int(spatial),
            'mbtype': mbtype, 'ref0': int(ref0), 'ref1': int(ref1),
            'mv0': (int(mv0x), int(mv0y)), 'mv1': (int(mv1x), int(mv1y)),
        })
print(f'direct_rows={len(rows)}')
by_frame = Counter(r['frame'] for r in rows)
for frame, count in sorted(by_frame.items())[:20]:
    spatial = Counter(r['spatial'] for r in rows if r['frame'] == frame)
    refs = Counter((r['ref0'], r['ref1']) for r in rows if r['frame'] == frame)
    mv0 = Counter(r['mv0'] for r in rows if r['frame'] == frame)
    print(f'frame={frame} rows={count} spatial={dict(spatial)} refs={dict(refs.most_common(4))} mv0={dict(mv0.most_common(4))}')
PY

echo "trace=$OUTDIR/ffdirect.rows"
