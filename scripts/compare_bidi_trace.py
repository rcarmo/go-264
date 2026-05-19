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

GO_L0 = {
    0: (True, True), 1: (True, False), 2: (False, False), 3: (True, False),
    4: (True, True), 5: (True, True), 6: (False, False), 7: (False, False),
    8: (True, False), 9: (True, False), 10: (False, True), 11: (False, True),
    12: (True, True), 13: (True, True), 14: (False, True), 15: (False, True),
    16: (True, True), 17: (True, True), 18: (True, False), 19: (True, False),
    20: (True, True), 21: (True, True), 22: (True, True),
}
GO_L1 = {
    0: (True, True), 1: (False, False), 2: (True, False), 3: (True, False),
    4: (False, False), 5: (False, False), 6: (True, True), 7: (True, True),
    8: (False, True), 9: (False, True), 10: (True, False), 11: (True, False),
    12: (False, True), 13: (False, True), 14: (True, True), 15: (True, True),
    16: (True, False), 17: (True, False), 18: (True, True), 19: (True, True),
    20: (True, True), 21: (True, True), 22: (True, True),
}

def go_uses(row: dict[str, object], list_idx: int, part: int) -> bool:
    mt = int(row['mbtype'])
    if part == 1 and mt in {0, 1, 2, 3}:
        part = 0
    if mt == 22:
        st = int(row['sub'][min(part, 3)])
        # GOBIDI prints FFmpeg-style internal sub flags, not raw H.264 sub_mb_type.
        if list_idx == 0:
            return (st & (4096 | 8192)) != 0
        return (st & (16384 | 32768)) != 0
    table = GO_L0 if list_idx == 0 else GO_L1
    return table.get(mt, (False, False))[0 if part == 0 else 1]

def ff_uses(row: dict[str, object], list_idx: int, part: int) -> bool:
    subs = row['sub']
    st = int(subs[min(part, 3)])
    if int(row['mbtype']) & 64:
        # B_8x8 rows: compare the representative sub-partition flags. Direct
        # sub-MBs may be reported as 12552 (no 8x8 bit), so the macroblock
        # shape rather than only the current sub flag determines this path.
        if list_idx == 0:
            return (st & (4096 | 8192)) != 0
        return (st & (16384 | 32768)) != 0
    t = int(row['mbtype'])
    if part == 1 and (t & 8):  # MB_TYPE_16x16: scan8[4] is same partition cache.
        part = 0
    if list_idx == 0:
        return (t & (4096 if part == 0 else 8192)) != 0
    return (t & (16384 if part == 0 else 32768)) != 0

def ref_mv_mismatch(f: dict[str, object], g: dict[str, object]) -> bool:
    fr = f['ref_mv']; gr = g['ref_mv']
    # Report use-mask differences before checking values. Unused-list cache cells
    # are intentionally noisy in FFmpeg and Go and should not drive bisection.
    for list_idx in (0, 1):
        fu = ff_uses(f, list_idx, 0)
        gu = go_uses(g, list_idx, 0)
        if fu != gu:
            return True
        if not fu:
            continue
        if list_idx == 0 and (fr[0], fr[2], fr[3]) != (gr[0], gr[2], gr[3]):
            return True
        if list_idx == 1 and (fr[1], fr[4], fr[5]) != (gr[1], gr[4], gr[5]):
            return True
    return False

def p1_mismatch(f: dict[str, object], g: dict[str, object]) -> bool:
    fp = f['p1']; gp = g['p1']
    for list_idx in (0, 1):
        fu = ff_uses(f, list_idx, 1)
        gu = go_uses(g, list_idx, 1)
        if fu != gu:
            return True
        if not fu:
            continue
        if list_idx == 0 and (fp[0], fp[1]) != (gp[0], gp[1]):
            return True
        if list_idx == 1 and (fp[2], fp[3]) != (gp[2], gp[3]):
            return True
    return False

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
            if ref_mv_mismatch(f, g):
                fields.append('ref_mv')
            if p1_mismatch(f, g):
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
