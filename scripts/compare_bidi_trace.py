#!/usr/bin/env python3
"""Compare post-motion B-slice rows from FFmpeg FFBIDI and Go GOBIDI traces.

Unlike FFDIRECT, these rows are emitted after explicit B MB ref/MVD/MVP handling,
so mixed direct/explicit B_8x8 and Bi partition rows can be compared without
FFmpeg's pred_direct_motion staleness.
"""
from __future__ import annotations
import argparse, re
from collections import defaultdict

FF_RE = re.compile(
    r'FFBIDI mb=(?P<mb>\d+).*?frame=(?P<frame>\d+)\b.*?type=(?P<mbtype>-?\d+) '
    r'ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+) '
    r'mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\}'
    r'(?: mv0p1=\{(?P<mv0p1x>-?\d+),(?P<mv0p1y>-?\d+)\} mv1p1=\{(?P<mv1p1x>-?\d+),(?P<mv1p1y>-?\d+)\})?.*?'
    r'sub0=(?P<sub0>\d+) sub1=(?P<sub1>\d+) sub2=(?P<sub2>\d+) sub3=(?P<sub3>\d+)'
)
GO_RE = re.compile(
    r'GOBIDI mb=(?P<mb>\d+).*?poc=(?P<frame>\d+)\b.*?mb_type=(?P<mbtype>-?\d+) '
    r'ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+) '
    r'mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\}'
    r'(?: mv0p1=\{(?P<mv0p1x>-?\d+),(?P<mv0p1y>-?\d+)\} mv1p1=\{(?P<mv1p1x>-?\d+),(?P<mv1p1y>-?\d+)\})?.*?'
    r'sub0=(?P<sub0>\d+) sub1=(?P<sub1>\d+) sub2=(?P<sub2>\d+) sub3=(?P<sub3>\d+)'
)

def row(m: re.Match[str]) -> dict[str, object]:
    gd = m.groupdict()
    def iv(name: str, default: int = 0) -> int:
        v = gd.get(name)
        return default if v is None else int(v)
    return {
        'mb': iv('mb'), 'frame': iv('frame'), 'mbtype': iv('mbtype'),
        'ref_mv': (iv('ref0'), iv('ref1'), iv('mv0x'), iv('mv0y'), iv('mv1x'), iv('mv1y')),
        'p1': (iv('mv0p1x'), iv('mv0p1y'), iv('mv1p1x'), iv('mv1p1y')),
        'sub': (iv('sub0'), iv('sub1'), iv('sub2'), iv('sub3')),
    }

def load(path: str, regex: re.Pattern[str]) -> dict[tuple[int, int, int], dict[str, object]]:
    out = {}
    occurrence: defaultdict[int, int] = defaultdict(int)
    last_mb: dict[int, int] = {}
    for line in open(path, errors='replace'):
        m = regex.search(line)
        if not m:
            continue
        r = row(m)
        mb, frame = int(r['mb']), int(r['frame'])
        if frame in last_mb and mb <= last_mb[frame]:
            occurrence[frame] += 1
        last_mb[frame] = mb
        out[(frame, occurrence[frame], mb)] = r
    return out

def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument('ffbidi')
    ap.add_argument('gobidi')
    ap.add_argument('--ff-frame', type=int, required=True)
    ap.add_argument('--ff-occurrence', type=int, default=0)
    ap.add_argument('--go-poc', type=int, required=True)
    ap.add_argument('--go-occurrence', type=int, default=0)
    ap.add_argument('--limit', type=int, default=50)
    ap.add_argument('--fail-on-diff', action='store_true')
    args = ap.parse_args()
    ff = load(args.ffbidi, FF_RE)
    go = load(args.gobidi, GO_RE)
    keys = sorted(k for k in ff if k[0] == args.ff_frame and k[1] == args.ff_occurrence)
    rows = diffs = 0
    for _, _, mb in keys:
        f = ff[(args.ff_frame, args.ff_occurrence, mb)]
        g = go.get((args.go_poc, args.go_occurrence, mb))
        rows += 1
        if g is None:
            print(f'mb={mb:04d} missing_go')
            diffs += 1
        else:
            fields = []
            if f['ref_mv'] != g['ref_mv']:
                fields.append('ref_mv')
            if f['p1'] != g['p1']:
                fields.append('p1')
            # FF sub_mb_type cache is meaningful for B_8x8-shaped rows. For
            # direct 16x16/two-part MBs it often carries FFmpeg-internal cache
            # flags rather than H.264 sub_mb_type syntax, so do not report it as
            # a standalone mismatch unless either side is explicitly B_8x8-like.
            ff_8x8_like = any((int(v) & 64) != 0 for v in f['sub'])
            go_8x8_like = int(g['mbtype']) == 22
            if (ff_8x8_like or go_8x8_like) and f['sub'] != g['sub']:
                fields.append('sub')
            if fields:
                print(f'mb={mb:04d} fields={",".join(fields)} ff_ref_mv={f["ref_mv"]} go_ref_mv={g["ref_mv"]} ff_p1={f["p1"]} go_p1={g["p1"]} ff_sub={f["sub"]} go_sub={g["sub"]}')
                diffs += 1
        if diffs >= args.limit:
            break
    print(f'compared={rows} diffs={diffs}')
    if args.fail_on_diff and diffs:
        raise SystemExit(1)

if __name__ == '__main__':
    main()
