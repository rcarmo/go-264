#!/usr/bin/env python3
"""Compare Go trace264 I8x8 predicted/final modes with FFmpeg FFMODE rows."""

from __future__ import annotations

import argparse
import re
from pathlib import Path

GO_RE = re.compile(r"mb=(\d+) x=(\d+) y=(\d+) .*?8x8=true .*?(?:i8left=\[([^\]]+)\] i8top=\[([^\]]+)\] )?i8pred=\[([^\]]+)\] i8mode=\[([^\]]+)\]")
FF_RE = re.compile(r"FFMODE part=i8x8 mb=(\d+) b8=(\d+) x=(\d+) y=(\d+) pred=(-?\d+) mode=(-?\d+)")


def ints(text: str) -> list[int]:
    return [int(v) for v in text.split()]


def parse_go(path: Path) -> dict[tuple[int, int], dict[str, int]]:
    rows = {}
    for line in path.read_text(errors="replace").splitlines():
        match = GO_RE.search(line)
        if not match:
            continue
        mb = int(match.group(1))
        x = int(match.group(2))
        y = int(match.group(3))
        left = ints(match.group(4)) if match.group(4) else []
        top = ints(match.group(5)) if match.group(5) else []
        pred = ints(match.group(6))
        mode = ints(match.group(7))
        for b8 in range(min(4, len(pred), len(mode))):
            rows[(mb, b8)] = {
                "mb": mb,
                "b8": b8,
                "x": x,
                "y": y,
                "left": left[b8 // 2] if b8 // 2 < len(left) else None,
                "top": top[b8 % 2] if b8 % 2 < len(top) else None,
                "pred": pred[b8],
                "mode": mode[b8],
            }
    return rows


def parse_ff(path: Path) -> dict[tuple[int, int], dict[str, int]]:
    rows = {}
    for line in path.read_text(errors="replace").splitlines():
        match = FF_RE.search(line)
        if not match:
            continue
        mb = int(match.group(1))
        b8 = int(match.group(2))
        # Keep the first occurrence when FFmpeg logs are produced by repeated decode attempts.
        rows.setdefault((mb, b8), {
            "mb": mb,
            "b8": b8,
            "x": int(match.group(3)),
            "y": int(match.group(4)),
            "pred": int(match.group(5)),
            "mode": int(match.group(6)),
        })
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("go_trace", type=Path)
    parser.add_argument("ffmpeg_trace", type=Path)
    parser.add_argument("--mb", type=int)
    parser.add_argument("--limit", type=int, default=50)
    parser.add_argument("--mismatches-only", action="store_true")
    args = parser.parse_args()

    go = parse_go(args.go_trace)
    ff = parse_ff(args.ffmpeg_trace)
    common = sorted(set(go) & set(ff))
    print(f"go_rows={len(go)} ffmpeg_rows={len(ff)} common={len(common)}")
    count = 0
    for key in common:
        g = go[key]
        f = ff[key]
        if args.mb is not None and g["mb"] != args.mb:
            continue
        pred_delta = g["pred"] - f["pred"]
        mode_delta = g["mode"] - f["mode"]
        if args.mismatches_only and pred_delta == 0 and mode_delta == 0:
            continue
        edge = ""
        if g.get("left") is not None or g.get("top") is not None:
            edge = f" go_left={g.get('left')} go_top={g.get('top')}"
        print(
            f"mb={g['mb']:04d} b8={g['b8']} x={g['x']} y={g['y']} "
            f"go_pred={g['pred']} ff_pred={f['pred']} go_mode={g['mode']} ff_mode={f['mode']}"
            f"{edge} pred_delta={pred_delta:+d} mode_delta={mode_delta:+d}"
        )
        count += 1
        if count >= args.limit:
            break
    return 0 if count or not args.mismatches_only else 1


if __name__ == "__main__":
    raise SystemExit(main())
