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
    r'FFDIRECT mb=(?P<mb>\d+).*?frame=(?P<frame>\d+)(?:\s+poc=(?P<poc>-?\d+))?.*?spatial=(?P<spatial>\d+).*?'
    r'ref0=(?P<ref0>-?\d+) ref1=(?P<ref1>-?\d+) '
    r'mv0=\{(?P<mv0x>-?\d+),(?P<mv0y>-?\d+)\} mv1=\{(?P<mv1x>-?\d+),(?P<mv1y>-?\d+)\}.*?'
    r'sub0=(?P<sub0>\d+) sub1=(?P<sub1>\d+) sub2=(?P<sub2>\d+) sub3=(?P<sub3>\d+)'
    r'(?:.*?submv0=\{(?P<submv0x>-?\d+),(?P<submv0y>-?\d+)\} '
    r'submv1=\{(?P<submv1x>-?\d+),(?P<submv1y>-?\d+)\} '
    r'submv2=\{(?P<submv2x>-?\d+),(?P<submv2y>-?\d+)\} '
    r'submv3=\{(?P<submv3x>-?\d+),(?P<submv3y>-?\d+)\})?'
)
GO_RE = re.compile(
    r'GODIRECT mb=(?P<mb>\d+).*?poc=(?P<frame>\d+)\b(?:.*?spatial=(?P<spatial>\d+))?.*?mb_type=(?P<mbtype>\d+) '
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
        'spatial': iv('spatial', -1),
        'poc': normalize_poc(iv('poc', -1)),
        'ref_mv': (iv('ref0'), iv('ref1'), iv('mv0x'), iv('mv0y'), iv('mv1x'), iv('mv1y')),
        'sub': (iv('sub0'), iv('sub1'), iv('sub2'), iv('sub3')),
        'submv': (
            (iv('submv0x'), iv('submv0y')),
            (iv('submv1x'), iv('submv1y')),
            (iv('submv2x'), iv('submv2y')),
            (iv('submv3x'), iv('submv3y')),
        ),
    }

def normalize_poc(v: int) -> int:
    if v >= 32768:
        return v - 65536
    return v

def load_ff(path: str) -> dict[tuple[int, int, int, int], dict[str, object]]:
    out = {}
    occurrence: defaultdict[tuple[int, int], int] = defaultdict(int)
    last_mb: dict[tuple[int, int], int] = {}
    for line in open(path, errors='replace'):
        m = FF_RE.search(line)
        if not m:
            continue
        row = row_from_match(m)
        mb, frame = int(row['mb']), int(row['frame'])
        group = (frame, int(row.get('poc', -1)))
        if group in last_mb and mb <= last_mb[group]:
            occurrence[group] += 1
        last_mb[group] = mb
        out[(frame, int(row['poc']), occurrence[group], mb)] = row
    return out

def load_go(path: str) -> dict[tuple[int, int, int, int], dict[str, object]]:
    out = {}
    occurrence: defaultdict[int, int] = defaultdict(int)
    last_mb: dict[int, int] = {}
    for line in open(path, errors='replace'):
        m = GO_RE.search(line)
        if not m:
            continue
        row = row_from_match(m)
        mb, frame = int(row['mb']), int(row['frame'])
        if frame in last_mb and mb <= last_mb[frame]:
            occurrence[frame] += 1
        last_mb[frame] = mb
        out[(frame, frame, occurrence[frame], mb)] = row
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('ffdirect')
    ap.add_argument('godirect')
    ap.add_argument('--ff-frame', type=int, required=True)
    ap.add_argument('--ff-poc', type=int, help='optional FF picture POC filter when rows include poc=')
    ap.add_argument('--ff-occurrence', type=int, default=0, help='which repeated frame_num group to compare')
    ap.add_argument('--go-poc', type=int, required=True)
    ap.add_argument('--go-occurrence', type=int, default=0, help='which repeated Go POC group to compare')
    ap.add_argument('--spatial', type=int, choices=[0, 1], help='compare only rows with this direct_spatial flag on both sides')
    ap.add_argument('--mb', type=int, help='compare only one absolute macroblock index')
    ap.add_argument('--from-mb', type=int, dest='from_mb', help='start comparison at this absolute macroblock index')
    ap.add_argument('--to-mb', type=int, dest='to_mb', help='stop comparison after this absolute macroblock index')
    ap.add_argument('--limit', type=int, default=50)
    ap.add_argument('--compare-subtypes', action='store_true', help='also report direct sub-type shape differences; off by default because FFDIRECT is pre-explicit-MVD for mixed B_8x8 rows')
    ap.add_argument('--ignore-top-ref-mv', action='store_true', help='ignore top-level ref/mv fields and compare only spatial/direct flags and direct sub-MV representatives')
    ap.add_argument('--fail-on-diff', action='store_true', help='exit non-zero when any compared row differs or is missing')
    args = ap.parse_args()
    ff = load_ff(args.ffdirect)
    go = load_go(args.godirect)
    rows = 0
    diffs = 0
    frame_keys = sorted(k for k in ff if k[0] == args.ff_frame and k[2] == args.ff_occurrence and (args.ff_poc is None or k[1] == args.ff_poc))
    if not frame_keys:
        occurrences = sorted({k[1] for k in ff if k[0] == args.ff_frame})
        print(f'no_ff_rows frame={args.ff_frame} occurrence={args.ff_occurrence} available_occurrences={occurrences}')
        if args.fail_on_diff:
            raise SystemExit(1)
        return
    for key in frame_keys:
        _, _, _, mb = key
        if args.mb is not None and mb != args.mb:
            continue
        if args.from_mb is not None and mb < args.from_mb:
            continue
        if args.to_mb is not None and mb > args.to_mb:
            continue
        f = ff[key]
        if args.spatial is not None and f.get('spatial', -1) != args.spatial:
            continue
        g = go.get((args.go_poc, args.go_poc, args.go_occurrence, mb))
        rows += 1
        if g is None:
            print(f'mb={mb:04d} missing_go')
            diffs += 1
            continue
        if args.spatial is not None and g.get('spatial', -1) != args.spatial:
            print(f'mb={mb:04d} fields=spatial ff_spatial={f.get("spatial", -1)} go_spatial={g.get("spatial", -1)}')
            diffs += 1
            if diffs >= args.limit:
                break
            continue
        mismatch = []
        # FFmpeg may report Direct+Bi internal flags (61704) for resolved direct
        # 8x8 cache cells while Go normalizes direct cache cells to 12552. Treat
        # both as direct for direct-motion comparisons and only report sub-type
        # differences for non-direct shape disagreements.
        direct_flags = {12552, 20744, 61704}
        all_direct = all(s in direct_flags for s in f['sub']) and all(s in direct_flags for s in g['sub'])
        # For mixed B_8x8 rows, FFDIRECT is emitted before explicit sub-MB MVD
        # decode overwrites non-direct cache cells. Top-level ref/mv fields are
        # therefore stale unless the whole row is direct; compare direct sub-MVs
        # below instead.
        if g.get('spatial', -1) >= 0 and f.get('spatial', -1) >= 0 and f['spatial'] != g['spatial']:
            mismatch.append('spatial')
        if not args.ignore_top_ref_mv and all_direct and f['ref_mv'] != g['ref_mv']:
            mismatch.append('ref_mv')
        if args.compare_subtypes and any((fs not in direct_flags or gs not in direct_flags) and fs != gs for fs, gs in zip(f['sub'], g['sub'])):
            mismatch.append('sub')
        # FFmpeg's FFDIRECT rows are emitted inside pred_direct_motion, before
        # explicit B_8x8 sub-MB MVD decoding overwrites non-direct sub blocks.
        # Compare sub-MVs only for sub blocks that are direct in both traces;
        # otherwise the row is intentionally stale on the FFmpeg side.
        direct_idxs = [i for i, (fs, gs) in enumerate(zip(f['sub'], g['sub'])) if fs in direct_flags and gs in direct_flags]
        if any(f['submv'][i] != g['submv'][i] for i in direct_idxs):
            mismatch.append('submv')
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
    if args.fail_on_diff and (diffs or rows == 0):
        raise SystemExit(1)

if __name__ == '__main__':
    main()
