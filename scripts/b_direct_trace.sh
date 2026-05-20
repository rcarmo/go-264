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
import re
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
    int mb = sl->mb_x + sl->mb_y * h->mb_width;
    if (sl->direct_spatial_mv_pred)
        pred_spatial_direct_motion(h, sl, mb_type);
    else
        pred_temp_direct_motion(h, sl, mb_type);
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
# Replace the whole wrapper function, whether it is pristine or has an older
# trace patch, then add a tiny in-scope colocated-MV trace inside
# pred_spatial_direct_motion where l1mv/l1ref variables exist.
s = s[:start] + new + s[end:]
# Older runs leave in-scope colocated hooks in FFmpeg's working tree. Keep the
# caller's current MB_LIMIT authoritative instead of silently reusing the limit
# baked into a previous patch.
s = re.sub(r'\(sl->mb_x \+ sl->mb_y \* h->mb_width\) < \d+\)',
           f'(sl->mb_x + sl->mb_y * h->mb_width) < {mb_limit})', s)
s = re.sub(
    r'fprintf\(stderr, "FFCOLZERO8 mb=%04d i8=%d coltype0=.*?\);',
    'fprintf(stderr, "FFCOLZERO8 mb=%04d i8=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} zero=%d ref0=%d ref1=%d mv0={%d,%d} mv1={%d,%d} is_b8x8=%d sub_type=%u mb_type=%d\\\\n",\\n                                sl->mb_x + sl->mb_y * h->mb_width, i8, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1, ref[0], ref[1],\\n                                (int16_t)mv[0], (int16_t)(mv[0] >> 16), (int16_t)mv[1], (int16_t)(mv[1] >> 16), is_b8x8, sub_mb_type, *mb_type);',
    s,
    flags=re.S)
s = s.replace(
    'fprintf(stderr, "FFCOLZERO8 mb=%04d i8=%d colref0=%d colref1=%d colmv={%d,%d} ref0=%d ref1=%d\\n",\n                                sl->mb_x + sl->mb_y * h->mb_width, i8, l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], ref[0], ref[1]);',
    'fprintf(stderr, "FFCOLZERO8 mb=%04d i8=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} ref0=%d ref1=%d is_b8x8=%d sub_type=%u mb_type=%d\\n",\n                                sl->mb_x + sl->mb_y * h->mb_width, i8, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], ref[0], ref[1], is_b8x8, sub_mb_type, *mb_type);')
s = re.sub(
    r'fprintf\(stderr, "FFCOLZERO mb=%04d i8=%d i4=%d coltype0=.*?\);',
    'fprintf(stderr, "FFCOLZERO mb=%04d i8=%d i4=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} zero=%d ref0=%d ref1=%d mv0={%d,%d} mv1={%d,%d} is_b8x8=%d sub_type=%u mb_type=%d\\\\n",\\n                                    sl->mb_x + sl->mb_y * h->mb_width, i8, i4, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1, ref[0], ref[1],\\n                                    (int16_t)mv[0], (int16_t)(mv[0] >> 16), (int16_t)mv[1], (int16_t)(mv[1] >> 16), is_b8x8, sub_mb_type, *mb_type);',
    s,
    flags=re.S)
s = s.replace(
    'fprintf(stderr, "FFCOLZERO mb=%04d i8=%d i4=%d colref0=%d colref1=%d colmv={%d,%d} ref0=%d ref1=%d\\n",\n                                    sl->mb_x + sl->mb_y * h->mb_width, i8, i4, l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], ref[0], ref[1]);',
    'fprintf(stderr, "FFCOLZERO mb=%04d i8=%d i4=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} ref0=%d ref1=%d is_b8x8=%d sub_type=%u mb_type=%d\\n",\n                                    sl->mb_x + sl->mb_y * h->mb_width, i8, i4, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], ref[0], ref[1], is_b8x8, sub_mb_type, *mb_type);')
if 'FFCOLZERO8 mb=' not in s:
    needle8 = '''                    const int16_t *mv_col = l1mv[x8 * 3 + y8 * 3 * b4_stride];
                    if (FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1) {
'''
    repl8 = '''                    const int16_t *mv_col = l1mv[x8 * 3 + y8 * 3 * b4_stride];
                    if (getenv("GO264_FFMPEG_DIRECT_TRACE") && (sl->mb_x + sl->mb_y * h->mb_width) < ''' + mb_limit + ''')
                        fprintf(stderr, "FFCOLZERO8 mb=%04d i8=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} zero=%d ref0=%d ref1=%d mv0={%d,%d} mv1={%d,%d} is_b8x8=%d sub_type=%u mb_type=%d\\n",
                                sl->mb_x + sl->mb_y * h->mb_width, i8, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1, ref[0], ref[1],
                                (int16_t)mv[0], (int16_t)(mv[0] >> 16), (int16_t)mv[1], (int16_t)(mv[1] >> 16), is_b8x8, sub_mb_type, *mb_type);
                    if (FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1) {
'''
    if needle8 not in s:
        raise SystemExit('ffmpeg h264_direct.c spatial 8x8 colocated-zero hook target not found')
    s = s.replace(needle8, repl8, 1)
if 'FFCOLZERO mb=' not in s:
    needle = '''                    const int16_t *mv_col = l1mv[x8 * 2 + (i4 & 1) +
                                                     (y8 * 2 + (i4 >> 1)) * b4_stride];
                        if (FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1) {
'''
    repl = '''                    const int16_t *mv_col = l1mv[x8 * 2 + (i4 & 1) +
                                                     (y8 * 2 + (i4 >> 1)) * b4_stride];
                        if (getenv("GO264_FFMPEG_DIRECT_TRACE") && (sl->mb_x + sl->mb_y * h->mb_width) < ''' + mb_limit + ''')
                            fprintf(stderr, "FFCOLZERO mb=%04d i8=%d i4=%d coltype0=%d coltype1=%d colref0=%d colref1=%d colmv={%d,%d} zero=%d ref0=%d ref1=%d mv0={%d,%d} mv1={%d,%d} is_b8x8=%d sub_type=%u mb_type=%d\\n",
                                    sl->mb_x + sl->mb_y * h->mb_width, i8, i4, mb_type_col[0], mb_type_col[1], l1ref0[i8], l1ref1[i8], mv_col[0], mv_col[1], FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1, ref[0], ref[1],
                                    (int16_t)mv[0], (int16_t)(mv[0] >> 16), (int16_t)mv[1], (int16_t)(mv[1] >> 16), is_b8x8, sub_mb_type, *mb_type);
                        if (FFABS(mv_col[0]) <= 1 && FFABS(mv_col[1]) <= 1) {
'''
    if needle not in s:
        raise SystemExit('ffmpeg h264_direct.c spatial sub-8x8 colocated-zero hook target not found')
    s = s.replace(needle, repl, 1)
if 'FFCOLZERO8 mb=%04d i8=%d coltype0=' not in s or 'FFCOLZERO mb=%04d i8=%d i4=%d coltype0=' not in s:
    raise SystemExit('ffmpeg h264_direct.c colocated-zero trace postcondition failed')
p.write_text(s)
PY
}

mkdir -p "$OUTDIR"
patch_ffmpeg_direct_trace
(cd "$FFSRC" && make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >/tmp/go264-ffmpeg-direct-build.log)
GO264_FFMPEG_DIRECT_TRACE=1 "$FFMPEG" -y -threads 1 -hide_banner \
  -i "$INPUT" -frames:v "$FRAMES" -pix_fmt yuv420p -f rawvideo /dev/null \
  >"$OUTDIR/ffmpeg.stdout" 2>"$OUTDIR/ffmpeg.direct.trace" || true

grep '^FFDIRECT' "$OUTDIR/ffmpeg.direct.trace" >"$OUTDIR/ffdirect.rows" || true
grep -E '^FFCOLZERO(8)?' "$OUTDIR/ffmpeg.direct.trace" >"$OUTDIR/ffcolzero.rows" || true

rm -rf "$OUTDIR/go-frames"
mkdir -p "$OUTDIR/go-frames"
mkdir -p "${GOTMPDIR:-/workspace/tmp/gotmp}"
GOTMPDIR="${GOTMPDIR:-/workspace/tmp/gotmp}" GO264_DIRECT_TRACE=1 go run ./cmd/decode264 -f yuv -i "$INPUT" -o "$OUTDIR/go-frames" \
  >"$OUTDIR/go.stdout" 2>"$OUTDIR/go.direct.trace"
grep '^GODIRECT' "$OUTDIR/go.direct.trace" >"$OUTDIR/godirect.rows" || true
grep '^GOCOLZERO' "$OUTDIR/go.direct.trace" >"$OUTDIR/gocolzero.rows" || true
grep '^GOMOTSAVE' "$OUTDIR/go.direct.trace" >"$OUTDIR/gomotsave.rows" || true
grep '^GOMOTWRITE' "$OUTDIR/go.direct.trace" >"$OUTDIR/gomotwrite.rows" || true
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

if [[ -n "${GO_POC:-}" ]]; then
  python3 scripts/compare_direct_trace.py "$OUTDIR/ffdirect.rows" "$OUTDIR/godirect.rows" \
    --ff-frame "${FF_FRAME:-2}" --ff-occurrence "${FF_OCCURRENCE:-0}" \
    --go-poc "$GO_POC" --go-occurrence "${GO_OCCURRENCE:-0}" --limit "${LIMIT:-20}" || true
fi

echo "ffdirect=$OUTDIR/ffdirect.rows"
echo "godirect=$OUTDIR/godirect.rows"
echo "ffcolzero=$OUTDIR/ffcolzero.rows"
echo "gocolzero=$OUTDIR/gocolzero.rows"
echo "gomotsave=$OUTDIR/gomotsave.rows"
echo "gomotwrite=$OUTDIR/gomotwrite.rows"
