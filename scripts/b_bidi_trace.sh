#!/usr/bin/env bash
set -euo pipefail

INPUT="${1:-/workspace/tmp/bbb_annexb.h264}"
OUTDIR="${2:-/workspace/tmp/go264-b-bidi-trace}"
FRAMES="${FRAMES:-10}"
MB_LIMIT="${MB_LIMIT:-40}"
FFSRC="${FFMPEG_SRC:-/workspace/tmp/ffmpeg-7.1.3}"
FFMPEG="${FFMPEG:-$FFSRC/ffmpeg}"

patch_ffmpeg_bidi_trace() {
  python3 - "$FFSRC/libavcodec/h264_cabac.c" "$MB_LIMIT" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
mb_limit = sys.argv[2]
s = p.read_text()
if '#include <stdlib.h>' not in s:
    s = s.replace('#include "cabac_functions.h"', '#include "cabac_functions.h"\n#include <stdlib.h>\n#include <stdio.h>')
needle = '''   if( IS_INTER( mb_type ) ) {
        h->chroma_pred_mode_table[mb_xy] = 0;
        write_back_motion(h, sl, mb_type);
   }
'''
trace = f'''   if( IS_INTER( mb_type ) ) {{
        h->chroma_pred_mode_table[mb_xy] = 0;
        write_back_motion(h, sl, mb_type);
   }}
   if (getenv("GO264_FFMPEG_B_MB_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_B && (sl->mb_x + sl->mb_y * h->mb_width) < {mb_limit}) {{
        int s0 = scan8[0];
        fprintf(stderr, "FFBIDI mb=%04d x=%02d y=%02d frame=%d type=%d ref0=%d ref1=%d mv0={{%d,%d}} mv1={{%d,%d}} mv0p1={{%d,%d}} mv1p1={{%d,%d}} sub0=%d sub1=%d sub2=%d sub3=%d\\n",
                sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num, mb_type,
                sl->ref_cache[0][s0], sl->ref_cache[1][s0],
                sl->mv_cache[0][s0][0], sl->mv_cache[0][s0][1],
                sl->mv_cache[1][s0][0], sl->mv_cache[1][s0][1],
                sl->mv_cache[0][scan8[4]][0], sl->mv_cache[0][scan8[4]][1],
                sl->mv_cache[1][scan8[4]][0], sl->mv_cache[1][scan8[4]][1],
                sl->sub_mb_type[0], sl->sub_mb_type[1], sl->sub_mb_type[2], sl->sub_mb_type[3]);
   }}
'''
if 'GO264_FFMPEG_B_MB_TRACE' in s:
    # Replace an older injected block by locating it after write_back_motion.
    start = s.find('   if( IS_INTER( mb_type ) ) {\n        h->chroma_pred_mode_table[mb_xy] = 0;\n        write_back_motion(h, sl, mb_type);\n   }\n   if (getenv("GO264_FFMPEG_B_MB_TRACE")')
    if start >= 0:
        brace = s.find('{', s.find('if (getenv("GO264_FFMPEG_B_MB_TRACE")', start))
        depth = 0
        end = None
        for i in range(brace, len(s)):
            if s[i] == '{': depth += 1
            elif s[i] == '}':
                depth -= 1
                if depth == 0:
                    end = i + 2
                    break
        if end is None: raise SystemExit('unterminated existing FFBIDI trace block')
        # Include the preceding IS_INTER block in replacement.
        s = s[:start] + trace + s[end:]
    else:
        print('existing GO264_FFMPEG_B_MB_TRACE found but block shape not recognized; leaving as-is')
else:
    if needle not in s:
        raise SystemExit('FFBIDI write_back_motion hook target not found')
    s = s.replace(needle, trace, 1)
p.write_text(s)
PY
}

mkdir -p "$OUTDIR/go" "$OUTDIR/ffmpeg"
patch_ffmpeg_bidi_trace
(cd "$FFSRC" && make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >/tmp/go264-ffmpeg-bidi-build.log)
GO264_FFMPEG_B_MB_TRACE=1 "$FFMPEG" -y -threads 1 -hide_banner \
  -i "$INPUT" -frames:v "$FRAMES" -pix_fmt yuv420p -f rawvideo /dev/null \
  >"$OUTDIR/ffmpeg/stdout.log" 2>"$OUTDIR/ffmpeg/bidi.log" || true

grep '^FFBIDI' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffbidi.rows" || true
rm -rf "$OUTDIR/go/frames"
mkdir -p "$OUTDIR/go/frames"
GO264_B_MB_TRACE=1 go run ./cmd/decode264 -f yuv -i "$INPUT" -o "$OUTDIR/go/frames" \
  >"$OUTDIR/go/stdout.log" 2>"$OUTDIR/go/bidi.log"
grep '^GOBIDI' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobidi.rows" || true

python3 scripts/compare_bidi_trace.py "$OUTDIR/ffbidi.rows" "$OUTDIR/gobidi.rows" \
  --ff-frame "${FF_FRAME:-2}" --ff-occurrence "${FF_OCCURRENCE:-0}" \
  --go-poc "${GO_POC:-6}" --go-occurrence "${GO_OCCURRENCE:-0}" --limit "${LIMIT:-20}" || true

echo "ffbidi=$OUTDIR/ffbidi.rows"
echo "gobidi=$OUTDIR/gobidi.rows"
