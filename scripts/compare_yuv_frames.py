#!/usr/bin/env python3
"""Compare display-order YUV420p frames emitted by go-264 with a raw reference.

The Go input is a directory containing frame_0000.yuv, frame_0001.yuv, ...;
the reference is a single raw YUV420p stream, such as FFmpeg output. The first
mismatch is reported as frame, plane, luma macroblock, plane coordinate, and
sample values. Exit status is 0 for exact parity, 1 for pixel differences, and
2 for malformed/missing input.
"""
from __future__ import annotations

import argparse
import math
from pathlib import Path
import sys


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-dir", type=Path, required=True,
                        help="directory containing display-order frame_%%04d.yuv files")
    parser.add_argument("--reference", type=Path, required=True,
                        help="display-order raw YUV420p reference stream")
    parser.add_argument("--width", type=int, required=True)
    parser.add_argument("--height", type=int, required=True)
    parser.add_argument("--frames", type=int, default=0,
                        help="frames to compare (0: infer from reference size)")
    parser.add_argument("--verbose-frames", action="store_true",
                        help="print per-frame/per-plane difference statistics")
    return parser.parse_args()


def psnr(sse: int, samples: int) -> str:
    if sse == 0:
        return "inf"
    value = 10.0 * math.log10((255.0 * 255.0 * samples) / sse)
    return f"{value:.4f}"


def fail(message: str) -> int:
    print(f"ERROR {message}", file=sys.stderr)
    return 2


def main() -> int:
    args = parse_args()
    if args.width <= 0 or args.height <= 0 or args.width % 2 or args.height % 2:
        return fail("width and height must be positive even values for YUV420p")
    if not args.go_dir.is_dir():
        return fail(f"Go frame directory not found: {args.go_dir}")
    if not args.reference.is_file():
        return fail(f"reference stream not found: {args.reference}")

    y_size = args.width * args.height
    c_width, c_height = args.width // 2, args.height // 2
    c_size = c_width * c_height
    frame_size = y_size + 2 * c_size
    reference_size = args.reference.stat().st_size
    if reference_size % frame_size:
        return fail(
            f"reference size {reference_size} is not a multiple of frame size {frame_size}"
        )
    available = reference_size // frame_size
    frame_count = args.frames or available
    if frame_count <= 0:
        return fail("no reference frames")
    if frame_count > available:
        return fail(f"requested {frame_count} frames but reference contains {available}")

    planes = (
        ("Y", 0, y_size, args.width, args.height, 16),
        ("U", y_size, c_size, c_width, c_height, 8),
        ("V", y_size + c_size, c_size, c_width, c_height, 8),
    )
    total_sse = {name: 0 for name, *_ in planes}
    total_samples = {name: 0 for name, *_ in planes}
    differing_frames = 0
    first = None

    with args.reference.open("rb") as ref_file:
        for frame_index in range(frame_count):
            go_path = args.go_dir / f"frame_{frame_index:04d}.yuv"
            if not go_path.is_file():
                return fail(f"missing Go frame: {go_path}")
            go_data = go_path.read_bytes()
            if len(go_data) != frame_size:
                return fail(
                    f"Go frame {frame_index} size {len(go_data)}, expected {frame_size}: {go_path}"
                )
            ref_data = ref_file.read(frame_size)
            if len(ref_data) != frame_size:
                return fail(f"short reference read at frame {frame_index}")

            frame_differs = False
            for name, offset, size, stride, _height, mb_span in planes:
                plane_sse = 0
                plane_diffs = 0
                go_plane = go_data[offset:offset + size]
                ref_plane = ref_data[offset:offset + size]
                for sample_index, (got, want) in enumerate(zip(go_plane, ref_plane)):
                    delta = got - want
                    if delta:
                        frame_differs = True
                        plane_diffs += 1
                        plane_sse += delta * delta
                        if first is None:
                            x = sample_index % stride
                            y = sample_index // stride
                            first = {
                                "frame": frame_index,
                                "plane": name,
                                "x": x,
                                "y": y,
                                "mb_x": x // mb_span,
                                "mb_y": y // mb_span,
                                "got": got,
                                "want": want,
                                "delta": delta,
                            }
                total_sse[name] += plane_sse
                total_samples[name] += size
                if plane_diffs and args.verbose_frames:
                    print(
                        f"FRAME frame={frame_index} plane={name} diffs={plane_diffs} "
                        f"samples={size} sse={plane_sse} psnr={psnr(plane_sse, size)}"
                    )
            if frame_differs:
                differing_frames += 1

    if first is None:
        print(f"EXACT frames={frame_count} width={args.width} height={args.height} format=yuv420p")
        return 0

    print(
        "FIRST_DIFFERENCE "
        f"frame={first['frame']} plane={first['plane']} "
        f"mb=({first['mb_x']},{first['mb_y']}) "
        f"pixel=({first['x']},{first['y']}) "
        f"go={first['got']} reference={first['want']} delta={first['delta']}"
    )
    summary = " ".join(
        f"{name}_psnr={psnr(total_sse[name], total_samples[name])}"
        for name, *_ in planes
    )
    print(
        f"SUMMARY frames={frame_count} differing_frames={differing_frames} "
        f"exact_frames={frame_count - differing_frames} {summary}"
    )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
