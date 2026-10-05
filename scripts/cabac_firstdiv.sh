#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/project-env.sh"
go264_init_paths cabac-firstdiv

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INPUT="${1:-$GO264_FIXTURE_ROOT/testsrc_cabac_p.h264}"
OUTDIR="${2:-$GO264_RUN_ROOT/cabac-firstdiv/$(date -u +%Y%m%dT%H%M%SZ)-$$}"
# Trace at least a full first frame for common fixtures. The comparator filters Go
# events to FFmpeg's decoded frame range, so too-small limits cause false event-count
# failures before a real divergence can be reported.
LIMIT="${LIMIT:-4096}"
FFSRC="${FFMPEG_SRC:-$GO264_CACHE_ROOT/ffmpeg/ffmpeg-7.1.3}"
FFMPEG="${FFMPEG:-${GO264_FFMPEG_BIN:-$GO264_BUILD_ROOT/ffmpeg-7.1.3/ffmpeg}}"
FFBUILD="$GO264_BUILD_ROOT/ffmpeg-7.1.3"
mkdir -p "$TMPDIR" "$GOTMPDIR"

patch_ffmpeg_trace() {
  python3 - "$FFSRC/libavcodec/h264_cabac.c" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
s = p.read_text()
if 'GO264_FFMPEG_CABAC_TRACE' in s:
    sys.exit(0)
s = s.replace('#define INT_BIT (CHAR_BIT * sizeof(int))\n\n#include "libavutil/attributes.h"', '#define INT_BIT (CHAR_BIT * sizeof(int))\n\n#include <stdio.h>\n#include <stdlib.h>\n\n#include "libavutil/attributes.h"')
s = s.replace('''            sl->last_qscale_diff = 0;\n\n            return 0;\n''', '''            sl->last_qscale_diff = 0;\n\n            if (getenv("GO264_FFMPEG_CABAC_TRACE"))\n                fprintf(stderr, "FFCABAC mb=%04d x=%02d y=%02d frame=%d kind=%c_SKIP type=%d skip=1 cbp=00 qpd=0 qp=%d 8x8=0 chromaMode=0 tc=[0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0]\\n",\n                        sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num,\n                        sl->slice_type_nos == AV_PICTURE_TYPE_B ? 'B' : 'P', h->cur_pic.mb_type[mb_xy], sl->qscale);\n\n            return 0;\n''')
s = s.replace('''    h->cur_pic.qscale_table[mb_xy] = sl->qscale;\n    write_back_non_zero_count(h, sl);\n\n    return 0;\n''', '''    h->cur_pic.qscale_table[mb_xy] = sl->qscale;\n    write_back_non_zero_count(h, sl);\n\n    if (getenv("GO264_FFMPEG_CABAC_TRACE")) {\n        const uint8_t *nnz = h->non_zero_count[mb_xy];\n        fprintf(stderr, "FFCABAC mb=%04d x=%02d y=%02d frame=%d kind=%c type=%d skip=0 cbp=%02x qpd=%d qp=%d 8x8=%d chromaMode=%d tc=[%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d]\\n",\n                sl->mb_x + sl->mb_y * h->mb_width, sl->mb_x, sl->mb_y, h->poc.frame_num,\n                IS_INTRA(mb_type) ? 'I' : (sl->slice_type_nos == AV_PICTURE_TYPE_B ? 'B' : 'P'),\n                mb_type, cbp, sl->last_qscale_diff, sl->qscale, !!IS_8x8DCT(mb_type),\n                h->chroma_pred_mode_table[mb_xy],\n                nnz[0], nnz[1], nnz[2], nnz[3], nnz[4], nnz[5], nnz[6], nnz[7],\n                nnz[8], nnz[9], nnz[10], nnz[11], nnz[12], nnz[13], nnz[14], nnz[15]);\n    }\n\n    return 0;\n''')
p.write_text(s)
PY
}

build_ffmpeg() {
  patch_ffmpeg_trace
  if [[ ! -x "$FFMPEG" ]] || ! GO264_FFMPEG_CABAC_TRACE=1 "$FFMPEG" -hide_banner -i "$INPUT" -frames:v 1 -pix_fmt yuv420p -f rawvideo /dev/null >/dev/null 2>"$OUTDIR/ffmpeg-probe.log" || ! grep -q '^FFCABAC' "$OUTDIR/ffmpeg-probe.log"; then
    mkdir -p "$FFBUILD"
    (cd "$FFBUILD" && "$FFSRC/configure" \
      --disable-x86asm --disable-doc --disable-debug --disable-network \
      --disable-everything --enable-ffmpeg \
      --enable-protocol=file --enable-demuxer=h264 --enable-parser=h264 \
      --enable-decoder=h264 --enable-encoder=rawvideo --enable-muxer=rawvideo \
      >"$OUTDIR/ffmpeg-configure.log" && \
      make -j"${MAKE_JOBS:-$(nproc 2>/dev/null || echo 2)}" ffmpeg >"$OUTDIR/ffmpeg-build.log")
  fi
}

mkdir -p "$OUTDIR"
build_ffmpeg

cd "$ROOT"
go run ./cmd/trace264 -i "$INPUT" -cabac -limit "$LIMIT" >"$OUTDIR/go.trace" 2>"$OUTDIR/go.stderr"
GO264_FFMPEG_CABAC_TRACE=1 "$FFMPEG" -hide_banner -i "$INPUT" -frames:v 1 -pix_fmt yuv420p -f rawvideo /dev/null >"$OUTDIR/ffmpeg.stdout" 2>"$OUTDIR/ffmpeg.trace" || true

python3 - "$OUTDIR/go.trace" "$OUTDIR/ffmpeg.trace" <<'PY'
import re, sys
from pathlib import Path
pat = re.compile(r'(?:FFCABAC\s+)?mb=(\d+).*?frame=(\d+).*?kind=([^\s]+).*?type=([^\s]+).*?skip=([^\s]+).*?cbp=([0-9a-fA-F]+).*?qpd=([^\s]+).*?qp=([^\s]+).*?8x8=([^\s]+).*?chromaMode=([^\s]+).*?tc=\[([^\]]*)\]')

def load(path):
    out=[]
    for line in Path(path).read_text(errors='replace').splitlines():
        m=pat.search(line)
        if not m: continue
        mb, frame, kind, typ, skip, cbp, qpd, qp, t8, chroma, tc = m.groups()
        cbp_val = int(cbp,16)
        out.append({
            'line': line, 'mb': int(mb), 'frame': int(frame), 'kind': kind, 'type': typ,
            'skip': skip in ('true','1'), 'cbp': cbp_val & 0x3f, 'cbp_raw': cbp_val, 'qpd': int(qpd),
            'qp': int(qp), 't8': t8 in ('true','1'), 'chroma': int(chroma),
            'tc': [int(x) for x in tc.split()] if tc.strip() else [],
        })
    return out

go=load(sys.argv[1]); ff=load(sys.argv[2])
if ff:
    min_ff_frame = min(e['frame'] for e in ff)
    max_ff_frame = max(e['frame'] for e in ff)
    go = [e for e in go if min_ff_frame <= e['frame'] <= max_ff_frame]
print(f'go_events={len(go)} ffmpeg_events={len(ff)}')
fields=['frame','mb','skip','cbp','qpd','qp','t8','chroma','tc']
for i,(g,f) in enumerate(zip(go,ff)):
    diffs=[k for k in fields if g[k]!=f[k]]
    if diffs:
        print(f'FIRST_DIVERGENCE index={i} frame={g["frame"]}/{f["frame"]} mb={g["mb"]}/{f["mb"]} fields={",".join(diffs)}')
        print('GO     '+g['line'])
        print('FFMPEG '+f['line'])
        sys.exit(1)
if len(go)!=len(ff):
    print(f'FIRST_DIVERGENCE event_count go={len(go)} ffmpeg={len(ff)}')
    sys.exit(1)
print('NO_DIVERGENCE in compared fields')
PY
