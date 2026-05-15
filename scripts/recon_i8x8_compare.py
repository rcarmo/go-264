#!/usr/bin/env python3
"""Compare Go and FFmpeg luma Intra_8x8 reconstruction trace lines.

Reads stderr logs produced with GO264_RECON_TRACE=1 and
GO264_FFMPEG_RECON_TRACE=1 and reports the largest per-block output-sum
mismatches. The trace formats use different field names, but both carry
mb/b8/predsum/outsum/block_out for luma I8x8 blocks.
"""

from __future__ import annotations

import argparse
import re
from pathlib import Path

GO_RE = re.compile(r"GORECON part=i8x8 (?:frame=(\d+) )?.*?mb=(\d+) b8=(\d+) .*?predsum=(-?\d+) .*?outsum=(-?\d+) .*?block_out=\[([^\]]*)\]")
FF_RE = re.compile(r"FFRECON part=i8x8 (?:frame=(\d+) )?.*?mb=(\d+) b8=(\d+) .*?predsum=(-?\d+) .*?outsum=(-?\d+) .*?block_out=\[([^\]]*)\]")


def parse_blocks(path: Path, pattern: re.Pattern[str]) -> list[dict[str, object]]:
    blocks: list[dict[str, object]] = []
    occurrence_by_key: dict[tuple[int, int], int] = {}
    for line in path.read_text(errors="replace").splitlines():
        match = pattern.search(line)
        if not match:
            continue
        frame = int(match.group(1)) if match.group(1) is not None else None
        mb = int(match.group(2))
        b8 = int(match.group(3))
        key = (mb, b8)
        occurrence = occurrence_by_key.get(key, 0)
        occurrence_by_key[key] = occurrence + 1
        predsum = int(match.group(4))
        outsum = int(match.group(5))
        block_out = [int(v) for v in match.group(6).split()]
        blocks.append({
            "key": key,
            "frame_key": frame if frame is not None else occurrence,
            "frame": frame,
            "occurrence": occurrence,
            "predsum": predsum,
            "outsum": outsum,
            "block_out": block_out,
            "line": line,
        })
    return blocks


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("go_log", type=Path)
    parser.add_argument("ffmpeg_log", type=Path)
    parser.add_argument("--limit", type=int, default=20)
    parser.add_argument("--frame", type=int, help="only compare one decoded frame index when present in Go trace")
    parser.add_argument("--mb", type=int, help="only compare one macroblock address")
    parser.add_argument("--b8", type=int, choices=range(4), metavar="0..3", help="only compare one luma 8x8 block")
    parser.add_argument("--occurrence", type=int, help="only compare one occurrence index for repeated mb/b8 keys")
    args = parser.parse_args()

    go_blocks = parse_blocks(args.go_log, GO_RE)
    ff_blocks = parse_blocks(args.ffmpeg_log, FF_RE)
    go = {(block["frame_key"], block["key"], block["occurrence"]): block for block in go_blocks}
    ff = {(block["frame_key"], block["key"], block["occurrence"]): block for block in ff_blocks}
    common = sorted(set(go) & set(ff))
    print(f"go_blocks={len(go_blocks)} ffmpeg_blocks={len(ff_blocks)} common={len(common)}")
    if not common:
        return 1

    rows = []
    for key in common:
        frame_key, (mb, b8), occurrence = key
        occurrence = int(occurrence)
        frame = go[key]["frame"]
        if args.frame is not None and frame != args.frame:
            continue
        if args.mb is not None and mb != args.mb:
            continue
        if args.b8 is not None and b8 != args.b8:
            continue
        if args.occurrence is not None and occurrence != args.occurrence:
            continue
        g = go[key]
        f = ff[key]
        out_delta = int(g["outsum"]) - int(f["outsum"])
        pred_delta = int(g["predsum"]) - int(f["predsum"])
        gb = g["block_out"]
        fb = f["block_out"]
        block_delta = [int(gb[i]) - int(fb[i]) for i in range(min(len(gb), len(fb)))]
        rows.append((abs(out_delta), key, out_delta, pred_delta, block_delta, g, f))

    if not rows:
        print("no matching blocks after filters")
        return 1

    rows.sort(reverse=True, key=lambda item: item[0])
    for _, (frame_key, (mb, b8), _occurrence), out_delta, pred_delta, block_delta, g, f in rows[: args.limit]:
        occurrence = int(g["occurrence"])
        frame = g["frame"]
        frame_label = f"frame={frame}" if frame is not None else f"occ={occurrence}"
        print(
            f"{frame_label} occ={occurrence} mb={mb:04d} b8={b8} out_delta={out_delta:+d} "
            f"pred_delta={pred_delta:+d} block_delta={block_delta} "
            f"go_out={g['outsum']} ff_out={f['outsum']}"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
