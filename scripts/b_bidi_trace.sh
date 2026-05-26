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
import re
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
        int s1 = (mb_type & 16) ? scan8[8] : scan8[4];
        int s2 = scan8[8];
        int s3 = scan8[12];
        fprintf(stderr, "FFBIDI mb=%04d x=%02d y=%02d frame=%d type=%d ref0=%d ref1=%d ref0p1=%d ref1p1=%d mv0={{%d,%d}} mv1={{%d,%d}} mv0p1={{%d,%d}} mv1p1={{%d,%d}} sub0=%d sub1=%d sub2=%d sub3=%d submv0={{%d,%d}} submv1={{%d,%d}} submv2={{%d,%d}} submv3={{%d,%d}}\\n",
                sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num, mb_type,
                sl->ref_cache[0][s0], sl->ref_cache[1][s0], sl->ref_cache[0][s1], sl->ref_cache[1][s1],
                sl->mv_cache[0][s0][0], sl->mv_cache[0][s0][1],
                sl->mv_cache[1][s0][0], sl->mv_cache[1][s0][1],
                sl->mv_cache[0][s1][0], sl->mv_cache[0][s1][1],
                sl->mv_cache[1][s1][0], sl->mv_cache[1][s1][1],
                sl->sub_mb_type[0], sl->sub_mb_type[1], sl->sub_mb_type[2], sl->sub_mb_type[3],
                sl->mv_cache[0][s0][0], sl->mv_cache[0][s0][1],
                sl->mv_cache[0][scan8[4]][0], sl->mv_cache[0][scan8[4]][1],
                sl->mv_cache[0][s2][0], sl->mv_cache[0][s2][1],
                sl->mv_cache[0][s3][0], sl->mv_cache[0][s3][1]);
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
# Keep optional FF B mb_type diagnostics bounded by the caller's current MB_LIMIT.
# Older local source patches used fixed 14/15 limits while chasing the original
# MB13 divergence; preserve those diagnostics but make the focused window movable.
s = s.replace('(sl->mb_x + sl->mb_y * h->mb_width) <= 14', f'(sl->mb_x + sl->mb_y * h->mb_width) < {mb_limit}')
s = s.replace('(sl->mb_x+sl->mb_y*h->mb_width)<15', f'(sl->mb_x+sl->mb_y*h->mb_width)<{mb_limit}')
s = s.replace('mbidx==12', f'mbidx < {mb_limit}')
s = s.replace('_mbidx == 12', f'_mbidx < {mb_limit}')
s = s.replace('(sl->mb_x + sl->mb_y * h->mb_width) < 15', f'(sl->mb_x + sl->mb_y * h->mb_width) < {mb_limit}')
s = s.replace('(sl->mb_x + sl->mb_y * h->mb_width) < 20', f'(sl->mb_x + sl->mb_y * h->mb_width) < {mb_limit}')
if 'FFCBP part=' not in s:
    s = s.replace('''static int decode_cabac_mb_cbp_luma(H264SliceContext *sl)
{
    int cbp_b, cbp_a, ctx, cbp = 0;

    cbp_a = sl->left_cbp;
    cbp_b = sl->top_cbp;

    ctx = !(cbp_a & 0x02) + 2 * !(cbp_b & 0x04);
    cbp += get_cabac_noinline(&sl->cabac, &sl->cabac_state[73 + ctx]);
    ctx = !(cbp   & 0x01) + 2 * !(cbp_b & 0x08);
    cbp += get_cabac_noinline(&sl->cabac, &sl->cabac_state[73 + ctx]) << 1;
    ctx = !(cbp_a & 0x08) + 2 * !(cbp   & 0x01);
    cbp += get_cabac_noinline(&sl->cabac, &sl->cabac_state[73 + ctx]) << 2;
    ctx = !(cbp   & 0x04) + 2 * !(cbp   & 0x02);
    cbp += get_cabac_noinline(&sl->cabac, &sl->cabac_state[73 + ctx]) << 3;
    return cbp;
}
''', '''static int decode_cabac_mb_cbp_luma(H264SliceContext *sl)
{
    int cbp_b, cbp_a, ctx, cbp = 0;
    cbp_a = sl->left_cbp;
    cbp_b = sl->top_cbp;
#define TRACE_CBP_BIN(name, idxexpr) do { \\
    int _idx = (idxexpr); unsigned _low = (unsigned)sl->cabac.low >> 1, _range = (unsigned)sl->cabac.range; \\
    int _state = (int)sl->cabac_state[_idx]; int _bin = get_cabac_noinline(&sl->cabac, &sl->cabac_state[_idx]); \\
    if (getenv("GO264_FFMPEG_CBP_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P && sl->mb_y == 0 && sl->mb_x < ''' + mb_limit + ''') \\
        fprintf(stderr, "FFCBP part=%s mb=%04d poc=%d ctx=%d idx=%d state=%d low=%u range=%u bin=%d post_state=%d post_low=%u post_range=%u left=%03x top=%03x cbp_before=%02x\\n", \\
                (name), sl->mb_x, sl->h264->poc.poc_lsb, ctx, _idx, _state, _low, _range, _bin, (int)sl->cabac_state[_idx], (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range, cbp_a, cbp_b, cbp); \\
    cbp += _bin; \\
} while (0)
    ctx = !(cbp_a & 0x02) + 2 * !(cbp_b & 0x04);
    TRACE_CBP_BIN("luma0", 73 + ctx);
    ctx = !(cbp   & 0x01) + 2 * !(cbp_b & 0x08);
    { int _idx = 73 + ctx; unsigned _low = (unsigned)sl->cabac.low >> 1, _range = (unsigned)sl->cabac.range; int _state = (int)sl->cabac_state[_idx]; int _bin = get_cabac_noinline(&sl->cabac, &sl->cabac_state[_idx]); if (getenv("GO264_FFMPEG_CBP_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P && sl->mb_y == 0 && sl->mb_x < ''' + mb_limit + ''') fprintf(stderr, "FFCBP part=luma1 mb=%04d poc=%d ctx=%d idx=%d state=%d low=%u range=%u bin=%d post_state=%d post_low=%u post_range=%u left=%03x top=%03x cbp_before=%02x\\n", sl->mb_x, sl->h264->poc.poc_lsb, ctx, _idx, _state, _low, _range, _bin, (int)sl->cabac_state[_idx], (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range, cbp_a, cbp_b, cbp); cbp += _bin << 1; }
    ctx = !(cbp_a & 0x08) + 2 * !(cbp   & 0x01);
    { int _idx = 73 + ctx; unsigned _low = (unsigned)sl->cabac.low >> 1, _range = (unsigned)sl->cabac.range; int _state = (int)sl->cabac_state[_idx]; int _bin = get_cabac_noinline(&sl->cabac, &sl->cabac_state[_idx]); if (getenv("GO264_FFMPEG_CBP_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P && sl->mb_y == 0 && sl->mb_x < ''' + mb_limit + ''') fprintf(stderr, "FFCBP part=luma2 mb=%04d poc=%d ctx=%d idx=%d state=%d low=%u range=%u bin=%d post_state=%d post_low=%u post_range=%u left=%03x top=%03x cbp_before=%02x\\n", sl->mb_x, sl->h264->poc.poc_lsb, ctx, _idx, _state, _low, _range, _bin, (int)sl->cabac_state[_idx], (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range, cbp_a, cbp_b, cbp); cbp += _bin << 2; }
    ctx = !(cbp   & 0x04) + 2 * !(cbp   & 0x02);
    { int _idx = 73 + ctx; unsigned _low = (unsigned)sl->cabac.low >> 1, _range = (unsigned)sl->cabac.range; int _state = (int)sl->cabac_state[_idx]; int _bin = get_cabac_noinline(&sl->cabac, &sl->cabac_state[_idx]); if (getenv("GO264_FFMPEG_CBP_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P && sl->mb_y == 0 && sl->mb_x < ''' + mb_limit + ''') fprintf(stderr, "FFCBP part=luma3 mb=%04d poc=%d ctx=%d idx=%d state=%d low=%u range=%u bin=%d post_state=%d post_low=%u post_range=%u left=%03x top=%03x cbp_before=%02x\\n", sl->mb_x, sl->h264->poc.poc_lsb, ctx, _idx, _state, _low, _range, _bin, (int)sl->cabac_state[_idx], (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range, cbp_a, cbp_b, cbp); cbp += _bin << 3; }
#undef TRACE_CBP_BIN
    return cbp;
}
''')
# Upgrade older local FFCBP injections to include POC without requiring a
# pristine FFmpeg tree between diagnostic iterations.
s = s.replace('"FFCBP part=%s mb=%04d ctx=', '"FFCBP part=%s mb=%04d poc=%d ctx=')
s = s.replace('(name), sl->mb_x, ctx,', '(name), sl->mb_x, sl->h264->poc.poc_lsb, ctx,')
s = s.replace('"FFCBP part=luma1 mb=%04d ctx=', '"FFCBP part=luma1 mb=%04d poc=%d ctx=')
s = s.replace('"FFCBP part=luma2 mb=%04d ctx=', '"FFCBP part=luma2 mb=%04d poc=%d ctx=')
s = s.replace('"FFCBP part=luma3 mb=%04d ctx=', '"FFCBP part=luma3 mb=%04d poc=%d ctx=')
s = s.replace('\\n", sl->mb_x, ctx, _idx, _state', '\\n", sl->mb_x, sl->h264->poc.poc_lsb, ctx, _idx, _state')
if 'FFPTYPE mb=' not in s:
    s = s.replace('''        if( get_cabac_noinline( &sl->cabac, &sl->cabac_state[14] ) == 0 ) {
            /* P-type */
            if( get_cabac_noinline( &sl->cabac, &sl->cabac_state[15] ) == 0 ) {
                /* P_L0_D16x16, P_8x8 */
                mb_type= 3 * get_cabac_noinline( &sl->cabac, &sl->cabac_state[16] );
            } else {
                /* P_L0_D8x16, P_L0_D16x8 */
                mb_type= 2 - get_cabac_noinline( &sl->cabac, &sl->cabac_state[17] );
            }
            partition_count = ff_h264_p_mb_type_info[mb_type].partition_count;
            mb_type         = ff_h264_p_mb_type_info[mb_type].type;
        } else {
''', '''        if (getenv("GO264_P_TYPE_TRACE") && sl->mb_x + sl->mb_y * h->mb_width < ''' + mb_limit + ''') {
            int _mbt = sl->mb_x + sl->mb_y * h->mb_width;
            char _tr[512]; int _off = 0; unsigned _pl, _pr; int _bin;
#define TRACE_PTYPE_BIN(ctxidx) do { _pl=(unsigned)sl->cabac.low>>1; _pr=(unsigned)sl->cabac.range; _bin=get_cabac_noinline(&sl->cabac, &sl->cabac_state[(ctxidx)]); _off += snprintf(_tr+_off, sizeof(_tr)-_off, " ctx=%d pre=%u/%u bin=%d post=%u/%u", (ctxidx), _pl, _pr, _bin, (unsigned)sl->cabac.low>>1, (unsigned)sl->cabac.range); } while(0)
            TRACE_PTYPE_BIN(14);
            if (_bin == 0) {
                TRACE_PTYPE_BIN(15);
                if (_bin == 0) { TRACE_PTYPE_BIN(16); mb_type = 3 * _bin; }
                else { TRACE_PTYPE_BIN(17); mb_type = 2 - _bin; }
                fprintf(stderr, "FFPTYPE mb=%04d poc=%d raw=%d%s\\n", _mbt, h->poc.poc_lsb, mb_type, _tr);
            } else {
                fprintf(stderr, "FFPTYPE mb=%04d poc=%d raw=intra%s\\n", _mbt, h->poc.poc_lsb, _tr);
                mb_type = decode_cabac_intra_mb_type(sl, 17, 0);
                goto decode_intra_mb;
            }
#undef TRACE_PTYPE_BIN
            partition_count = ff_h264_p_mb_type_info[mb_type].partition_count;
            mb_type         = ff_h264_p_mb_type_info[mb_type].type;
        } else if( get_cabac_noinline( &sl->cabac, &sl->cabac_state[14] ) == 0 ) {
            /* P-type */
            if( get_cabac_noinline( &sl->cabac, &sl->cabac_state[15] ) == 0 ) {
                /* P_L0_D16x16, P_8x8 */
                mb_type= 3 * get_cabac_noinline( &sl->cabac, &sl->cabac_state[16] );
            } else {
                /* P_L0_D8x16, P_L0_D16x8 */
                mb_type= 2 - get_cabac_noinline( &sl->cabac, &sl->cabac_state[17] );
            }
            partition_count = ff_h264_p_mb_type_info[mb_type].partition_count;
            mb_type         = ff_h264_p_mb_type_info[mb_type].type;
        } else {
''')
# Keep optional FF_BPART_MVD diagnostics frame-qualified. Older local patches
# emitted rows keyed only by mb/part/list, which made repeated B-picture groups
# ambiguous for compare_bpart_mvd.py.
s = s.replace('"FF_B8x8_MVD mb=%04d sub=%d j=%d list=%d ', '"FF_B8x8_MVD mb=%04d frame=%d sub=%d j=%d list=%d ')
s = s.replace('"FF_BPART_MVD mb=%04d part=0 list=%d ', '"FF_BPART_MVD mb=%04d frame=%d part=0 list=%d ')
s = s.replace('"FF_BPART_MVD mb=%04d part=%d list=%d ', '"FF_BPART_MVD mb=%04d frame=%d part=%d list=%d ')
s = s.replace('sl->mb_x + sl->mb_y*h->mb_width, i, list, mpx, mpy,', 'sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, i, list, mpx, mpy,')
s = s.replace('sl->mb_x + sl->mb_y*h->mb_width, list, mpx, mpy,', 'sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, list, mpx, mpy,')
s = s.replace('sl->mb_x + sl->mb_y*h->mb_width, i, j, list,\n                                _amvdX', 'sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, i, j, list,\n                                _amvdX')
s = re.sub(r'(GO264_FFMPEG_CABAC_TRACE"\) && sl->slice_type_nos == AV_PICTURE_TYPE_B && \(sl->mb_x \+ sl->mb_y \* h->mb_width\) < )\d+(\)\n\s*fprintf\(stderr, "FF_B(?:8x8|PART)_MVD)', rf'\g<1>{mb_limit}\g<2>', s)
if 'FFMVD_COMP mb=' not in s:
    s = s.replace('''    int mxd = decode_cabac_mb_mvd(sl, 40, amvd0, &mpx);\\
    int myd = decode_cabac_mb_mvd(sl, 47, amvd1, &mpy);\\
''', '''    unsigned _mvdx_pre_low = (unsigned)sl->cabac.low >> 1, _mvdx_pre_range = (unsigned)sl->cabac.range;\\
    int mxd = decode_cabac_mb_mvd(sl, 40, amvd0, &mpx);\\
    if (getenv("GO264_FFMPEG_CABAC_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_B && (sl->mb_x + sl->mb_y * h->mb_width) < ''' + mb_limit + ''')\\
        fprintf(stderr, "FFMVD_COMP mb=%04d frame=%d poc=%d n=%d list=%d comp=x amvd=%d mvd=%d pre=%u/%u post=%u/%u\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, h->poc.poc_lsb, n, list, amvd0, mxd, _mvdx_pre_low, _mvdx_pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\\
    unsigned _mvdy_pre_low = (unsigned)sl->cabac.low >> 1, _mvdy_pre_range = (unsigned)sl->cabac.range;\\
    int myd = decode_cabac_mb_mvd(sl, 47, amvd1, &mpy);\\
    if (getenv("GO264_FFMPEG_CABAC_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_B && (sl->mb_x + sl->mb_y * h->mb_width) < ''' + mb_limit + ''')\\
        fprintf(stderr, "FFMVD_COMP mb=%04d frame=%d poc=%d n=%d list=%d comp=y amvd=%d mvd=%d pre=%u/%u post=%u/%u\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, h->poc.poc_lsb, n, list, amvd1, myd, _mvdy_pre_low, _mvdy_pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\\
''')
# Add AMVD context sums to the non-B_8x8 B-partition diagnostics. The old
# mvd_abs field is the decoded absolute MVD, not the CABAC neighbour context.
s = s.replace('{ int _pmx = mx, _pmy = my; DECODE_CABAC_MB_MVD(sl, list, 0)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _amvdX = sl->mvd_cache[list][scan8[0]-1][0] + sl->mvd_cache[list][scan8[0]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[0]-1][1] + sl->mvd_cache[list][scan8[0]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 0)\n')
s = s.replace('{ int _pmx = mx, _pmy = my; int _amvdX = sl->mvd_cache[list][scan8[0]-1][0] + sl->mvd_cache[list][scan8[0]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[0]-1][1] + sl->mvd_cache[list][scan8[0]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 0)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _amvdX = sl->mvd_cache[list][scan8[0]-1][0] + sl->mvd_cache[list][scan8[0]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[0]-1][1] + sl->mvd_cache[list][scan8[0]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 0)\n')
s = s.replace('{ int _pmx = mx, _pmy = my; DECODE_CABAC_MB_MVD(sl, list, 8*i)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _idx = 8*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 8*i)\n')
s = s.replace('{ int _pmx = mx, _pmy = my; int _idx = 8*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 8*i)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _idx = 8*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 8*i)\n')
s = s.replace('{ int _pmx = mx, _pmy = my; DECODE_CABAC_MB_MVD(sl, list, 4*i)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _idx = 4*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 4*i)\n')
s = s.replace('{ int _pmx = mx, _pmy = my; int _idx = 4*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 4*i)\n', '{ int _pmx = mx, _pmy = my; unsigned _pre_low = (unsigned)sl->cabac.low >> 1, _pre_range = (unsigned)sl->cabac.range; int _idx = 4*i; int _amvdX = sl->mvd_cache[list][scan8[_idx]-1][0] + sl->mvd_cache[list][scan8[_idx]-8][0]; int _amvdY = sl->mvd_cache[list][scan8[_idx]-1][1] + sl->mvd_cache[list][scan8[_idx]-8][1]; DECODE_CABAC_MB_MVD(sl, list, 4*i)\n')
s = s.replace('FF_BPART_MVD mb=%04d frame=%d part=0 list=%d mvd_abs=', 'FF_BPART_MVD mb=%04d frame=%d part=0 list=%d amvd={%d,%d} mvd_abs=')
s = s.replace('FF_BPART_MVD mb=%04d frame=%d part=%d list=%d mvd_abs=', 'FF_BPART_MVD mb=%04d frame=%d part=%d list=%d amvd={%d,%d} mvd_abs=')
s = s.replace(' final={%d,%d}\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, list,', ' final={%d,%d} pre=%u/%u post=%u/%u\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, list,')
s = s.replace(' final={%d,%d}\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, i, list,', ' final={%d,%d} pre=%u/%u post=%u/%u\\n", sl->mb_x + sl->mb_y*h->mb_width, h->poc.frame_num, i, list,')
s = s.replace('list, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my);', 'list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my, _pre_low, _pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);')
s = s.replace('i, list, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my);', 'i, list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my, _pre_low, _pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);')
s = s.replace('list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my);', 'list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my, _pre_low, _pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);')
s = s.replace('i, list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my);', 'i, list, _amvdX, _amvdY, mpx, mpy, mx-_pmx, my-_pmy, _pmx, _pmy, mx, my, _pre_low, _pre_range, (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);')
if 'FFBSTATE mb=' not in s:
    skip_state = '''            if (getenv("GO264_FFMPEG_B_STATE_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_B)\n                fprintf(stderr, "FFBSTATE mb=%04d x=%02d y=%02d frame=%d kind=skip low=%u range=%u\\n",\n                        sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num,\n                        (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\n            if (getenv("GO264_P_STATE_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P)\n                fprintf(stderr, "FFPSTATE mb=%04d x=%02d y=%02d frame=%d poc=%d kind=skip low=%u range=%u\\n",\n                        sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num, h->poc.poc_lsb,\n                        (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\n\n'''
    final_state = '''    if (getenv("GO264_FFMPEG_B_STATE_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_B)\n        fprintf(stderr, "FFBSTATE mb=%04d x=%02d y=%02d frame=%d kind=%s low=%u range=%u\\n",\n                sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num,\n                IS_INTRA(mb_type) ? "intra" : (IS_DIRECT(mb_type) ? "direct" : "inter"),\n                (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\n    if (getenv("GO264_P_STATE_TRACE") && sl->slice_type_nos == AV_PICTURE_TYPE_P)\n        fprintf(stderr, "FFPSTATE mb=%04d x=%02d y=%02d frame=%d poc=%d kind=%s low=%u range=%u\\n",\n                sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num, h->poc.poc_lsb,\n                IS_INTRA(mb_type) ? "intra" : (IS_SKIP(mb_type) ? "skip" : "inter"),\n                (unsigned)sl->cabac.low >> 1, (unsigned)sl->cabac.range);\n\n'''
    if 'FFCABAC mb=%04d x=%02d y=%02d frame=%d kind=%c_SKIP' in s:
        s = s.replace('''\n            return 0;\n\n        }\n    }\n    if (FRAME_MBAFF(h)) {''', '\n' + skip_state + '''            return 0;\n\n        }\n    }\n    if (FRAME_MBAFF(h)) {''', 1)
    else:
        s = s.replace('''            sl->last_qscale_diff = 0;\n\n            return 0;\n''', '''            sl->last_qscale_diff = 0;\n\n''' + skip_state + '''            return 0;\n''')
    s = s.replace('''\n    return 0;\n}\n''', '\n' + final_state + '''    return 0;\n}\n''', 1)
# Add picture POC to FF rows so repeated frame_num B-pictures can be matched
# without occurrence guessing.
s = s.replace('h->cur_pic_ptr ? h->cur_pic_ptr->poc : h->cur_pic.poc', 'h->poc.poc_lsb')
s = s.replace('frame=%d type=%d ref0=', 'frame=%d poc=%d type=%d ref0=')
s = s.replace('h->poc.frame_num, mb_type,', 'h->poc.frame_num, h->poc.poc_lsb, mb_type,')
s = s.replace('FFBSTATE mb=%04d x=%02d y=%02d frame=%d kind=', 'FFBSTATE mb=%04d x=%02d y=%02d frame=%d poc=%d kind=')
s = s.replace('h->poc.frame_num,\n                        (unsigned)sl->cabac.low', 'h->poc.frame_num, h->poc.poc_lsb,\n                        (unsigned)sl->cabac.low')
s = s.replace('h->poc.frame_num,\n                IS_INTRA(mb_type)', 'h->poc.frame_num, h->poc.poc_lsb,\n                IS_INTRA(mb_type)')
# Do not alter the older FFCABAC summary format; it has no poc field.
s = s.replace("h->poc.frame_num, h->poc.poc_lsb,\n                IS_INTRA(mb_type) ? 'I'", "h->poc.frame_num,\n                IS_INTRA(mb_type) ? 'I'")
s = s.replace('FF_B8x8_MVD mb=%04d frame=%d sub=', 'FF_B8x8_MVD mb=%04d frame=%d poc=%d sub=')
s = s.replace('FF_BPART_MVD mb=%04d frame=%d part=', 'FF_BPART_MVD mb=%04d frame=%d poc=%d part=')
s = s.replace('h->poc.frame_num, i, j, list,', 'h->poc.frame_num, h->poc.poc_lsb, i, j, list,')
s = s.replace('h->poc.frame_num, list,', 'h->poc.frame_num, h->poc.poc_lsb, list,')
s = s.replace('h->poc.frame_num, i, list,', 'h->poc.frame_num, h->poc.poc_lsb, i, list,')
p.write_text(s)
PY
}

mkdir -p "$OUTDIR/go" "$OUTDIR/ffmpeg"
patch_ffmpeg_bidi_trace
(cd "$FFSRC" && make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >/tmp/go264-ffmpeg-bidi-build.log)
ff_env=(GO264_FFMPEG_B_MB_TRACE=1)
# FFmpeg's C-side getenv() treats an empty environment variable as enabled, so
# only pass GO264_FFMPEG_CABAC_TRACE when the caller explicitly requests MVD rows.
if [[ -n "${GO264_FFMPEG_B_MVD_TRACE:-}" || -n "${GO264_B_CABAC_TRACE:-}" || -n "${GO264_P_CABAC_TRACE:-}" ]]; then
  ff_env+=(GO264_FFMPEG_CABAC_TRACE=1)
fi
[[ -n "${GO264_P_TYPE_TRACE:-}" ]] && ff_env+=(GO264_P_TYPE_TRACE=1)
[[ -n "${GO264_FFMPEG_CBP_TRACE:-}" ]] && ff_env+=(GO264_FFMPEG_CBP_TRACE=1)
[[ -n "${GO264_B_STATE_TRACE:-}" ]] && ff_env+=(GO264_FFMPEG_B_STATE_TRACE=1)
env "${ff_env[@]}" "$FFMPEG" -y -threads 1 -hide_banner \
  -i "$INPUT" -frames:v "$FRAMES" -pix_fmt yuv420p -f rawvideo /dev/null \
  >"$OUTDIR/ffmpeg/stdout.log" 2>"$OUTDIR/ffmpeg/bidi.log" || true

grep '^FFBIDI' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffbidi.rows" || true
grep '^FF_BPART_MVD' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffbpart_mvd.rows" || true
grep '^FFBSTATE' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffbstate.rows" || true
rm -rf "$OUTDIR/go/frames"
mkdir -p "$OUTDIR/go/frames" "${GOTMPDIR:-/workspace/tmp/gotmp}"
go_env=(GOTMPDIR="${GOTMPDIR:-/workspace/tmp/gotmp}" GO264_B_MB_TRACE=1)
[[ -n "${GO264_B_MVD_TRACE:-}" ]] && go_env+=(GO264_B_MVD_TRACE=1)
[[ -n "${GO264_B_MVD_COMP_TRACE:-}" ]] && go_env+=(GO264_B_MVD_COMP_TRACE=1)
[[ -n "${GO264_B_STATE_TRACE:-}" ]] && go_env+=(GO264_B_STATE_TRACE=1)
[[ -n "${GO264_B_CABAC_TRACE:-}" ]] && go_env+=(GO264_B_CABAC_TRACE=1)
[[ -n "${GO264_B_RESIDUAL_TRACE:-}" ]] && go_env+=(GO264_B_RESIDUAL_TRACE=1)
if [[ -n "${GO264_B_TYPE_TRACE:-}" ]]; then
  go_env+=(GO264_B_TYPE_TRACE=1 GO264_B_TYPE_TRACE_LIMIT="$MB_LIMIT")
fi
if [[ -n "${GO264_P_TYPE_TRACE:-}" ]]; then
  go_env+=(GO264_P_TYPE_TRACE=1 GO264_P_TYPE_TRACE_LIMIT="$MB_LIMIT")
fi
[[ -n "${GO264_P_CABAC_TRACE:-}" ]] && go_env+=(GO264_P_CABAC_TRACE=1 GO264_CABAC_CBP_TRACE=1)
[[ -n "${GO264_CABAC_TERMINATE_TRACE:-}" ]] && go_env+=(GO264_CABAC_TERMINATE_TRACE=1)
env "${go_env[@]}" go run ./cmd/decode264 -frames "$FRAMES" -f yuv -i "$INPUT" -o "$OUTDIR/go/frames" \
  >"$OUTDIR/go/stdout.log" 2>"$OUTDIR/go/bidi.log"
grep '^GOBIDI' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobidi.rows" || true
grep '^GOBPART_MVD' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobpart_mvd.rows" || true
grep '^GOBSTATE' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobstate.rows" || true
grep '^GOCABACB' "$OUTDIR/go/bidi.log" >"$OUTDIR/gocabacb.rows" || true
grep '^GOBTYPE' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobtype.rows" || true
grep '^GOBRES' "$OUTDIR/go/bidi.log" >"$OUTDIR/gobres.rows" || true
grep '^FFCABAC' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffcabac.rows" || true
grep '^FFPTYPE' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffptype.rows" || true
grep '^FFCBP' "$OUTDIR/ffmpeg/bidi.log" >"$OUTDIR/ffcbp.rows" || true
grep '^GOPTYPE' "$OUTDIR/go/bidi.log" >"$OUTDIR/goptype.rows" || true
grep '^GOP_PRE_CBP' "$OUTDIR/go/bidi.log" >"$OUTDIR/gop_pre_cbp.rows" || true
grep '^GOP_POST_CBP' "$OUTDIR/go/bidi.log" >"$OUTDIR/gop_post_cbp.rows" || true
grep '^GOP_PRE_DQP' "$OUTDIR/go/bidi.log" >"$OUTDIR/gop_pre_dqp.rows" || true
grep '^GOP_POST_DQP' "$OUTDIR/go/bidi.log" >"$OUTDIR/gop_post_dqp.rows" || true
grep '^GOCBP' "$OUTDIR/go/bidi.log" >"$OUTDIR/gocbp.rows" || true
grep '^GOTERMINATE' "$OUTDIR/go/bidi.log" >"$OUTDIR/goterminate.rows" || true

FF_POC_VALUE="${FF_POC:-${GO_POC:-6}}"
bidi_args=(
  --ff-frame "${FF_FRAME:-2}"
  --ff-poc "$FF_POC_VALUE"
  --ff-occurrence "${FF_OCCURRENCE:-0}"
  --go-poc "${GO_POC:-6}"
  --go-occurrence "${GO_OCCURRENCE:-0}"
  --limit "${LIMIT:-20}"
)
[[ -n "${FROM_MB:-}" ]] && bidi_args+=(--from-mb "$FROM_MB")
[[ -n "${TO_MB:-}" ]] && bidi_args+=(--to-mb "$TO_MB")
python3 scripts/compare_bidi_trace.py "$OUTDIR/ffbidi.rows" "$OUTDIR/gobidi.rows" \
  "${bidi_args[@]}" || true

: >"$OUTDIR/bstate.diff"
: >"$OUTDIR/bpart_mvd.diff"
: >"$OUTDIR/bpart_mvd_raw.diff"
if [[ -s "$OUTDIR/ffbstate.rows" && -s "$OUTDIR/gobstate.rows" ]]; then
  bstate_args=(
    --ff-frame "${FF_FRAME:-2}"
    --go-poc "${GO_POC:-6}"
    --ff-occurrence "${FF_OCCURRENCE:-0}"
    --go-occurrence "${GO_OCCURRENCE:-0}"
    --limit "${LIMIT:-20}"
  )
  bstate_args+=(--ff-poc "$FF_POC_VALUE")
  [[ -n "${FROM_MB:-}" ]] && bstate_args+=(--from-mb "$FROM_MB")
  [[ -n "${TO_MB:-}" ]] && bstate_args+=(--to-mb "$TO_MB")
  python3 scripts/compare_bstate.py "$OUTDIR/ffbstate.rows" "$OUTDIR/gobstate.rows" \
    "${bstate_args[@]}" >"$OUTDIR/bstate.diff" || true
fi

if [[ -s "$OUTDIR/ffbpart_mvd.rows" && -s "$OUTDIR/gobidi.rows" ]]; then
  bpart_args=(
    --ff-frame "${FF_FRAME:-2}"
    --go-poc "${GO_POC:-6}"
    --ff-occurrence "${FF_OCCURRENCE:-0}"
    --go-occurrence "${GO_OCCURRENCE:-0}"
    --limit "${LIMIT:-20}"
  )
  bpart_args+=(--ff-poc "$FF_POC_VALUE")
  [[ -n "${FROM_MB:-}" ]] && bpart_args+=(--from-mb "$FROM_MB")
  [[ -n "${TO_MB:-}" ]] && bpart_args+=(--to-mb "$TO_MB")
  python3 scripts/compare_bpart_mvd.py "$OUTDIR/ffbpart_mvd.rows" "$OUTDIR/gobidi.rows" \
    "${bpart_args[@]}" >"$OUTDIR/bpart_mvd.diff" || true
  if [[ -s "$OUTDIR/gobpart_mvd.rows" ]]; then
    raw_args=(
      --ff-frame "${FF_FRAME:-2}"
      --ff-occurrence "${FF_OCCURRENCE:-0}"
      --go-poc "${GO_POC:-6}"
      --go-occurrence "${GO_OCCURRENCE:-0}"
      --limit "${LIMIT:-20}"
    )
    raw_args+=(--ff-poc "$FF_POC_VALUE")
    [[ -n "${FROM_MB:-}" ]] && raw_args+=(--from-mb "$FROM_MB")
    [[ -n "${TO_MB:-}" ]] && raw_args+=(--to-mb "$TO_MB")
    python3 scripts/compare_bpart_mvd_raw.py "$OUTDIR/ffbpart_mvd.rows" "$OUTDIR/gobpart_mvd.rows" \
      "${raw_args[@]}" >"$OUTDIR/bpart_mvd_raw.diff" || true
  fi
fi

echo "ffbidi=$OUTDIR/ffbidi.rows"
echo "ffbpart_mvd=$OUTDIR/ffbpart_mvd.rows"
echo "ffbstate=$OUTDIR/ffbstate.rows"
echo "gobidi=$OUTDIR/gobidi.rows"
echo "gobpart_mvd=$OUTDIR/gobpart_mvd.rows"
echo "bpart_mvd_diff=$OUTDIR/bpart_mvd.diff"
echo "bstate_diff=$OUTDIR/bstate.diff"
echo "bpart_mvd_raw_diff=$OUTDIR/bpart_mvd_raw.diff"
echo "gobstate=$OUTDIR/gobstate.rows"
echo "goterminate=$OUTDIR/goterminate.rows"
