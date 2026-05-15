#!/usr/bin/env python3
"""Compare Go and FFmpeg luma Intra_4x4 reconstruction trace lines."""

from __future__ import annotations

import argparse
import re
from pathlib import Path

GO_RE = re.compile(r"GORECON part=i4x4 (?:frame=(\d+) )?.*?mb=(\d+) blk=(\d+) x=(\d+) y=(\d+) mode=(-?\d+) .*?predsum=(-?\d+) .*?outsum=(-?\d+) .*?top_right=\[([^\]]*)\] .*?right=\[([^\]]*)\]")
FF_RE = re.compile(r"FFRECON part=i4x4 (?:frame=(\d+) )?.*?mb=(\d+) blk=(\d+) x=(\d+) y=(\d+) mode=(-?\d+) .*?predsum=(-?\d+) .*?outsum=(-?\d+) .*?top_right=\[([^\]]*)\] .*?right=\[([^\]]*)\]")


def ints(value: str) -> list[int]:
    return [int(v) for v in value.split()]


def parse(path: Path, pattern: re.Pattern[str]) -> dict[tuple[int, int, int], dict[str, object]]:
    rows = {}
    for line in path.read_text(errors="replace").splitlines():
        match = pattern.search(line)
        if not match:
            continue
        frame = int(match.group(1)) if match.group(1) is not None else 0
        mb = int(match.group(2))
        blk = int(match.group(3))
        rows[(frame, mb, blk)] = {
            "frame": frame,
            "mb": mb,
            "blk": blk,
            "x": int(match.group(4)),
            "y": int(match.group(5)),
            "mode": int(match.group(6)),
            "predsum": int(match.group(7)),
            "outsum": int(match.group(8)),
            "top_right": ints(match.group(9)),
            "right": ints(match.group(10)),
            "line": line,
        }
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("go_log", type=Path)
    parser.add_argument("ffmpeg_log", type=Path)
    parser.add_argument("--frame", type=int, default=0)
    parser.add_argument("--mb", type=int)
    parser.add_argument("--blk", type=int)
    parser.add_argument("--limit", type=int, default=20)
    parser.add_argument("--sort", choices=("out", "pred", "topright", "right"), default="out")
    args = parser.parse_args()

    go = parse(args.go_log, GO_RE)
    ff = parse(args.ffmpeg_log, FF_RE)
    common = sorted(set(go) & set(ff))
    print(f"go_blocks={len(go)} ffmpeg_blocks={len(ff)} common={len(common)}")
    rows = []
    for key in common:
        frame, mb, blk = key
        if args.frame is not None and frame != args.frame:
            continue
        if args.mb is not None and mb != args.mb:
            continue
        if args.blk is not None and blk != args.blk:
            continue
        g = go[key]
        f = ff[key]
        pred_delta = int(g["predsum"]) - int(f["predsum"])
        out_delta = int(g["outsum"]) - int(f["outsum"])
        tr_delta = sum(abs(a - b) for a, b in zip(g["top_right"], f["top_right"]))
        right_delta = sum(abs(a - b) for a, b in zip(g["right"], f["right"]))
        rows.append((out_delta, pred_delta, tr_delta, right_delta, key, g, f))
    if not rows:
        print("no matching blocks after filters")
        return 1
    sort_index = {"out": 0, "pred": 1, "topright": 2, "right": 3}[args.sort]
    rows.sort(reverse=True, key=lambda row: abs(row[sort_index]))
    for out_delta, pred_delta, tr_delta, right_delta, (_frame, mb, blk), g, f in rows[: args.limit]:
        print(
            f"frame={g['frame']} mb={mb:04d} blk={blk:02d} x={g['x']} y={g['y']} "
            f"go_mode={g['mode']} ff_mode={f['mode']} out_delta={out_delta:+d} "
            f"pred_delta={pred_delta:+d} tr_delta={tr_delta} right_delta={right_delta} "
            f"go_tr={g['top_right']} ff_tr={f['top_right']} go_right={g['right']} ff_right={f['right']}"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
