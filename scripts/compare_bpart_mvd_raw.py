#!/usr/bin/env python3
"""Compare FFmpeg and Go raw B-partition CABAC MVD trace rows.

This is lower-level than compare_bpart_mvd.py: it compares AMVD, decoded MVD,
and CABAC arithmetic pre/post state for each B partition/list before MVP is
applied. Use it when final MV differs and you need to know whether CABAC state
was already divergent at MVD entry.
"""
from __future__ import annotations
import argparse
import re
from collections import defaultdict

FF_RE = re.compile(
    r'FF_BPART_MVD mb=(?P<mb>\d+) frame=(?P<frame>\d+) part=(?P<part>\d+) list=(?P<list>\d+) '
    r'amvd=\{(?P<amvdx>-?\d+),(?P<amvdy>-?\d+)\} mvd_abs=\{(?P<absx>-?\d+),(?P<absy>-?\d+)\} '
    r'mvd=\{(?P<mvdx>-?\d+),(?P<mvdy>-?\d+)\}.*?pre=(?P<prelow>\d+)/(?P<prerange>\d+) post=(?P<postlow>\d+)/(?P<postrange>\d+)'
)
GO_RE = re.compile(
    r'GOBPART_MVD_RAW mb=(?P<mb>\d+) part=(?P<part>\d+) list=(?P<list>\d+) '
    r'amvd=\{(?P<amvdx>-?\d+),(?P<amvdy>-?\d+)\} mvd=\{(?P<mvdx>-?\d+),(?P<mvdy>-?\d+)\} '
    r'pre=(?P<prelow>\d+)/(?P<prerange>\d+) post=(?P<postlow>\d+)/(?P<postrange>\d+)'
)

def iv(m: re.Match[str], name: str) -> int:
    return int(m.group(name))

def row(m: re.Match[str]) -> dict[str, tuple[int, int]]:
    return {
        'amvd': (iv(m, 'amvdx'), iv(m, 'amvdy')),
        'mvd': (iv(m, 'mvdx'), iv(m, 'mvdy')),
        'pre': (iv(m, 'prelow'), iv(m, 'prerange')),
        'post': (iv(m, 'postlow'), iv(m, 'postrange')),
    }

def load_ff(path: str, frame: int, occurrence: int) -> dict[tuple[int, int, int], dict[str, tuple[int, int]]]:
    out = {}
    seen: defaultdict[tuple[int, int, int], int] = defaultdict(int)
    for line in open(path, errors='replace'):
        m = FF_RE.search(line)
        if not m or iv(m, 'frame') != frame:
            continue
        key = (iv(m, 'mb'), iv(m, 'part'), iv(m, 'list'))
        occ = seen[key]; seen[key] += 1
        if occ == occurrence:
            out[key] = row(m)
    return out

def load_go(path: str, occurrence: int) -> dict[tuple[int, int, int], dict[str, tuple[int, int]]]:
    out = {}
    seen: defaultdict[tuple[int, int, int], int] = defaultdict(int)
    for line in open(path, errors='replace'):
        m = GO_RE.search(line)
        if not m:
            continue
        key = (iv(m, 'mb'), iv(m, 'part'), iv(m, 'list'))
        occ = seen[key]; seen[key] += 1
        if occ == occurrence:
            out[key] = row(m)
    return out

def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument('ffbpart_mvd')
    ap.add_argument('gobpart_mvd')
    ap.add_argument('--ff-frame', type=int, required=True)
    ap.add_argument('--ff-occurrence', type=int, default=0)
    ap.add_argument('--go-occurrence', type=int, default=0)
    ap.add_argument('--mb', type=int)
    ap.add_argument('--from-mb', type=int, dest='from_mb')
    ap.add_argument('--to-mb', type=int, dest='to_mb')
    ap.add_argument('--limit', type=int, default=50)
    ap.add_argument('--fail-on-diff', action='store_true')
    args = ap.parse_args()
    ff = load_ff(args.ffbpart_mvd, args.ff_frame, args.ff_occurrence)
    go = load_go(args.gobpart_mvd, args.go_occurrence)
    compared = diffs = 0
    if not ff:
        print(f'no_ff_raw_mvd_rows frame={args.ff_frame} occurrence={args.ff_occurrence}')
        if args.fail_on_diff:
            raise SystemExit(1)
        return
    for key in sorted(ff):
        mb, part, list_idx = key
        if args.mb is not None and mb != args.mb:
            continue
        if args.from_mb is not None and mb < args.from_mb:
            continue
        if args.to_mb is not None and mb > args.to_mb:
            continue
        g = go.get(key)
        if g is None:
            print(f'mb={mb:04d} part={part} list={list_idx} missing_go_raw')
            diffs += 1
        else:
            compared += 1
            fields = [name for name in ('amvd', 'pre', 'mvd', 'post') if ff[key][name] != g[name]]
            if fields:
                print(f'mb={mb:04d} part={part} list={list_idx} fields={",".join(fields)} ff_amvd={ff[key]["amvd"]} go_amvd={g["amvd"]} ff_pre={ff[key]["pre"]} go_pre={g["pre"]} ff_mvd={ff[key]["mvd"]} go_mvd={g["mvd"]} ff_post={ff[key]["post"]} go_post={g["post"]}')
                diffs += 1
        if diffs >= args.limit:
            break
    print(f'compared={compared} diffs={diffs}')
    if args.fail_on_diff and (diffs or compared == 0):
        raise SystemExit(1)

if __name__ == '__main__':
    main()
