#!/usr/bin/env python3
"""Compare FFmpeg FFCOLZERO rows with Go GOCOLZERO rows.

This is intentionally diagnostic-only: FFmpeg emits colocated-zero candidates for
both full-direct and B_8x8 direct paths, while Go currently emits only where the
Go decoder runs its colocated-zero helper. Missing Go rows are therefore useful
for finding direct-shape derivation gaps.
"""
from __future__ import annotations
import argparse
import re

FF_RE = re.compile(
    r'FFCOLZERO(?P<kind>8?) mb=(?P<mb>\d+)(?: i8=(?P<i8>\d+))?(?: i4=(?P<i4>\d+))?.*?'
    r'colref0=(?P<ref>-?\d+).*?colmv=\{(?P<x>-?\d+),(?P<y>-?\d+)\}(?: zero=(?P<zero>\d+))? ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+)'
    r'(?: mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\})?.*?'
    r'is_b8x8=(?P<is_b8x8>\d+) sub_type=(?P<sub_type>\d+) mb_type=(?P<mb_type>-?\d+)'
)
GO_RE = re.compile(
    r'GOCOLZERO mbx=(?P<mbx>\d+) mby=(?P<mby>\d+) part=(?P<part>\d+).*?'
    r'(?:curpoc=(?P<curpoc>-?\d+) )?colpoc=(?P<colpoc>-?\d+) colref0=(?P<ref>-?\d+) colmv=\{(?P<x>-?\d+),(?P<y>-?\d+)\} zero=(?P<zero>true|false)'
)


def load_ff(path: str, width: int) -> dict[tuple[int, int, int], dict[str, object]]:
    rows: dict[tuple[int, int, int], dict[str, object]] = {}
    occurrence: dict[tuple[int, int], int] = {}
    for line in open(path, errors='replace'):
        m = FF_RE.search(line)
        if not m:
            continue
        mb = int(m['mb'])
        i8 = int(m['i8'] or 0)
        key_base = (mb, i8)
        occ = occurrence.get(key_base, 0)
        occurrence[key_base] = occ + 1
        rows[(mb, i8, occ)] = {
            'ref_mv': (int(m['ref']), int(m['x']), int(m['y'])),
            'zero_flag': int(m['zero']) if m['zero'] is not None else None,
            'ref0': int(m['ref0']),
            'ref1': int(m['ref1']),
            'mv0': (int(m['mv0x'] or 0), int(m['mv0y'] or 0)),
            'mv1': (int(m['mv1x'] or 0), int(m['mv1y'] or 0)),
            'is_b8x8': int(m['is_b8x8']),
            'sub_type': int(m['sub_type']),
            'mb_type': int(m['mb_type']),
            'mbx': mb % width,
            'mby': mb // width,
        }
    return rows


def load_go(path: str, width: int) -> dict[tuple[int, int, int], dict[str, object]]:
    rows: dict[tuple[int, int, int], dict[str, object]] = {}
    occurrence: dict[tuple[int, int], int] = {}
    for line in open(path, errors='replace'):
        m = GO_RE.search(line)
        if not m:
            continue
        mb = int(m['mby']) * width + int(m['mbx'])
        part = int(m['part'])
        key_base = (mb, part)
        occ = occurrence.get(key_base, 0)
        occurrence[key_base] = occ + 1
        rows[(mb, part, occ)] = {
            'ref_mv': (int(m['ref']), int(m['x']), int(m['y'])),
            'curpoc': int(m['curpoc'] or -1),
            'colpoc': int(m['colpoc']),
            'zero': m['zero'] == 'true',
        }
    return rows


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument('ffcolzero')
    ap.add_argument('gocolzero')
    ap.add_argument('--width', type=int, default=40, help='macroblock width for bbb 640px fixture')
    ap.add_argument('--mb', type=int, help='compare only one absolute macroblock index')
    ap.add_argument('--part', type=int, help='compare only one 8x8 partition index')
    ap.add_argument('--occurrence', type=int, help='compare only one per-macroblock/part occurrence')
    ap.add_argument('--go-colpoc', type=int, help='compare only Go colocated rows that used this reference POC')
    ap.add_argument('--go-curpoc', type=int, help='compare only Go rows emitted while decoding this current POC')
    ap.add_argument('--ff-ref0', type=int, help='compare only FF rows with this resolved direct ref0')
    ap.add_argument('--ff-ref1', type=int, help='compare only FF rows with this resolved direct ref1')
    ap.add_argument('--only-diff', choices=['zero', 'motion'], help='report only zero-threshold disagreements or motion/ref disagreements')
    ap.add_argument('--zero-eligible', action='store_true', help='compare only FF rows whose colocated MV is small enough to trigger zeroing')
    ap.add_argument('--match-any-occurrence', action='store_true', help='for duplicate rows, accept any Go occurrence with the same mb/part/ref_mv')
    ap.add_argument('--limit', type=int, default=20)
    ap.add_argument('--fail-on-diff', action='store_true')
    args = ap.parse_args()
    ff = load_ff(args.ffcolzero, args.width)
    go = load_go(args.gocolzero, args.width)
    if args.go_colpoc is not None:
        go = {k: v for k, v in go.items() if v['colpoc'] == args.go_colpoc}
    if args.go_curpoc is not None:
        go = {k: v for k, v in go.items() if v['curpoc'] == args.go_curpoc}
    diffs = 0
    compared = 0
    for key in sorted(ff):
        mb, part, occ = key
        if args.mb is not None and mb != args.mb:
            continue
        if args.part is not None and part != args.part:
            continue
        if args.occurrence is not None and occ != args.occurrence:
            continue
        f = ff[key]
        if args.ff_ref0 is not None and f['ref0'] != args.ff_ref0:
            continue
        if args.ff_ref1 is not None and f['ref1'] != args.ff_ref1:
            continue
        if args.zero_eligible and (abs(f['ref_mv'][1]) > 1 or abs(f['ref_mv'][2]) > 1):
            continue
        g = go.get(key)
        if args.match_any_occurrence:
            candidates = [v for (gmb, gpart, _), v in go.items() if gmb == mb and gpart == part]
            exact = [v for v in candidates if v['ref_mv'] == f['ref_mv']]
            g = exact[0] if exact else (candidates[0] if candidates else None)
        compared += 1
        ff_zero = abs(f['ref_mv'][1]) <= 1 and abs(f['ref_mv'][2]) <= 1
        if g is None:
            if args.only_diff is None:
                print(f'mb={mb:04d} part={part} occ={occ} missing_go ff_ref_mv={f["ref_mv"]} ff_zero_flag={f["zero_flag"]} ff_mv0={f["mv0"]} ff_mv1={f["mv1"]} is_b8x8={f["is_b8x8"]} sub_type={f["sub_type"]} mb_type={f["mb_type"]}')
                diffs += 1
        elif f['ref_mv'] != g['ref_mv'] or ff_zero != g['zero']:
            kind = 'zero' if ff_zero != g['zero'] else 'motion'
            if args.only_diff is None or args.only_diff == kind:
                print(f'mb={mb:04d} part={part} occ={occ} kind={kind} ff_ref_mv={f["ref_mv"]} ff_zero={ff_zero} ff_zero_flag={f["zero_flag"]} ff_mv0={f["mv0"]} ff_mv1={f["mv1"]} go={g["ref_mv"]} go_curpoc={g["curpoc"]} go_colpoc={g["colpoc"]} go_zero={g["zero"]}')
                diffs += 1
        if diffs >= args.limit:
            break
    print(f'compared={compared} diffs={diffs}')
    if args.fail_on_diff and diffs:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
