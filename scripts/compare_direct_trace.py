#!/usr/bin/env python3
"""Compare FFmpeg FFDIRECT rows with Go GODIRECT rows for a chosen frame/POC pair.

Usage:
  compare_direct_trace.py ffdirect.rows go-direct.log --ff-frame 2 --go-poc 6

FFmpeg's trace uses H.264 frame_num, which repeats across display/decode-order
B pictures. The comparator therefore splits repeated frame_num groups whenever the
macroblock address wraps and selects one occurrence with --ff-occurrence.
The Go side is produced by running decode264 with GO264_DIRECT_TRACE=1.
"""
from __future__ import annotations
import argparse, re
from collections import defaultdict

FF_RE = re.compile(
    r'FFDIRECT mb=(?P<mb>\d+).*?frame=(?P<frame>\d+).*?spatial=(?P<spatial>\d+).*?'
    r'ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+) '
    r'mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\}.*?'
    r'sub0=(?P<sub0>\d+) sub1=(?P<sub1>\d+) sub2=(?P<sub2>\d+) sub3=(?P<sub3>\d+)'
    r'(?:.*?submv0=\{(?P<submv0x>-?\d+),(?P<submv0y>-?\d+)\} '
    r'submv1=\{(?P<submv1x>-?\d+),(?P<submv1y>-?\d+)\} '
    r'submv2=\{(?P<submv2x>-?\d+),(?P<submv2y>-?\d+)\} '
    r'submv3=\{(?P<submv3x>-?\d+),(?P<submv3y>-?\d+)\})?'
)
GO_RE = re.compile(
    r'GODIRECT mb=(?P<mb>\d+).*?poc=(?P<frame>\d+).*?mb_type=(?P<mbtype>\d+) '
    r'ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+) '
    r'mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\}.*?'
    r'sub0=(?P<sub0>\d+) sub1=(?P<sub1>\d+) sub2=(?P<sub2>\d+) sub3=(?P<sub3>\d+)'
    r'(?:.*?submv0=\{(?P<submv0x>-?\d+),(?P<submv0y>-?\d+)\} '
    r'submv1=\{(?P<submv1x>-?\d+),(?P<submv1y>-?\d+)\} '
    r'submv2=\{(?P<submv2x>-?\d+),(?P<submv2y>-?\d+)\} '
    r'submv3=\{(?P<submv3x>-?\d+),(?P<submv3y>-?\d+)\})?'
)

def row_from_match(m: re.Match[str]) -> dict[str, object]:
    gd = m.groupdict()
    def iv(name: str, default: int = 0) -> int:
        v = gd.get(name)
        return default if v is None else int(v)
    return {
        'mb': iv('mb'),
        'frame': iv('frame'),
        'ref_mv': (iv('ref0'), iv('ref1'), iv('mv0x'), iv('mv0y'), iv('mv1x'), iv('mv1y')),
        'sub': (iv('sub0'), iv('sub1'), iv('sub2'), iv('sub3')),
        'submv': (
            (iv('submv0x'), iv('submv0y')),
            (iv('submv1x'), iv('submv1y')),
            (iv('submv2x'), iv('submv2y')),
            (iv('submv3x'), iv('submv3y')),
        ),
    }

def load_ff(path: str) -> dict[tuple[int, int, int], dict[str, object]]:
    out = {}
    occurrence: defaultdict[int, int] = defaultdict(int)
    last_mb: dict[int, int] = {}
    for line in open(path, errors='replace'):
        m = FF_RE.search(line)
        if not m:
            continue
        row = row_from_match(m)
        mb, frame = int(row['mb']), int(row['frame'])
        if frame in last_mb and mb <= last_mb[frame]:
            occurrence[frame] += 1
        last_mb[frame] = mb
        out[(frame, occurrence[frame], mb)] = row
    return out

def load_go(path: str) -> dict[tuple[int, int], dict[str, object]]:
    out = {}
    for line in open(path, errors='replace'):
        m = GO_RE.search(line)
        if not m:
            continue
        row = row_from_match(m)
        out[(int(row['frame']), int(row['mb']))] = row
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('ffdirect')
    ap.add_argument('godirect')
    ap.add_argument('--ff-frame', type=int, required=True)
    ap.add_argument('--ff-occurrence', type=int, default=0, help='which repeated frame_num group to compare')
    ap.add_argument('--go-poc', type=int, required=True)
    ap.add_argument('--limit', type=int, default=50)
    args = ap.parse_args()
    ff = load_ff(args.ffdirect)
    go = load_go(args.godirect)
    rows = 0
    diffs = 0
    frame_keys = sorted(k for k in ff if k[0] == args.ff_frame and k[1] == args.ff_occurrence)
    if not frame_keys:
        occurrences = sorted({k[1] for k in ff if k[0] == args.ff_frame})
        print(f'no_ff_rows frame={args.ff_frame} occurrence={args.ff_occurrence} available_occurrences={occurrences}')
        return
    for _, _, mb in frame_keys:
        f = ff[(args.ff_frame, args.ff_occurrence, mb)]
        g = go.get((args.go_poc, mb))
        rows += 1
        if g is None:
            print(f'mb={mb:04d} missing_go')
            diffs += 1
            continue
        mismatch = [name for name in ('ref_mv', 'sub', 'submv') if f[name] != g[name]]
        if mismatch:
            print(
                f'mb={mb:04d} fields={",".join(mismatch)} '
                f'ff_ref_mv={f["ref_mv"]} go_ref_mv={g["ref_mv"]} '
                f'ff_sub={f["sub"]} go_sub={g["sub"]} '
                f'ff_submv={f["submv"]} go_submv={g["submv"]}'
            )
            diffs += 1
            if diffs >= args.limit:
                break
    print(f'compared={rows} diffs={diffs}')

if __name__ == '__main__':
    main()
