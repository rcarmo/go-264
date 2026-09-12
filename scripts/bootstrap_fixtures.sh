#!/usr/bin/env bash
set -euo pipefail

# Recreate transient local fixtures/tooling expected by the parity scripts.
# These files intentionally live under /workspace/tmp and are not checked in.
#
# Notes:
# - testsrc_cabac_p.h264 is deterministic and suitable for CABAC first-divergence gates.
# - bbb_annexb.h264 is regenerated from Blender's public BBB 640x360 source when
#   the historical local fixture is missing. It is CABAC+B-frame capable and good
#   for smoke gates, but historical POC/MB debugging notes may refer to an older
#   transient encoding.

ROOT="${ROOT:-/workspace/tmp}"
FFMPEG_SRC="${FFMPEG_SRC:-$ROOT/ffmpeg-7.1.3}"
FFMPEG_TARBALL="${FFMPEG_TARBALL:-$ROOT/ffmpeg-7.1.3.tar.xz}"
FFMPEG_BIN="${FFMPEG_BIN:-ffmpeg}"
BBB_SRC="${BBB_SRC:-$ROOT/BigBuckBunny_640x360.m4v}"
BBB_ZIP="${BBB_ZIP:-$ROOT/BigBuckBunny_640x360.m4v.zip}"
BBB_URL="${BBB_URL:-https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_640x360.m4v.zip}"
BBB_SOURCE_SHA256="${BBB_SOURCE_SHA256:-738e2f999860553d056dd79c952f58f63cbb73892a57c72342ce9e5330d9d2d7}"
TESTSRC_SHA256="${TESTSRC_SHA256:-99e3355a9d52f67de53ff7b10b0fed084a1ebbf0ecc929410ac1a4cae0d2ab52}"
BBB_SHA256="${BBB_SHA256:-1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff}"

# The source download moved to a ZIP. The historical bitstream hash remains the
# acceptance gate: current MPEG-4 source decoding is not byte-deterministic even
# with one decoder/x264/lookahead thread, so regenerating it is not equivalent.
# FFMPEG_BIN must therefore include libx264 and reproduce BBB_SHA256 exactly.

mkdir -p "$ROOT"

verify_fixture_hash() {
  local path="$1"
  local want="$2"
  local name="$3"
  local got
  got="$(sha256sum "$path" | awk '{print $1}')"
  if [[ "$got" != "$want" ]]; then
    echo "fixture hash mismatch for $name: got=$got want=$want" >&2
    echo "remove $path and rerun, or set ALLOW_FIXTURE_HASH_MISMATCH=1 for exploratory local traces" >&2
    if [[ "${ALLOW_FIXTURE_HASH_MISMATCH:-}" != "1" ]]; then
      exit 1
    fi
  fi
}

if [[ ! -f "$FFMPEG_SRC/libavcodec/h264_cabac.c" ]]; then
  if [[ ! -f "$FFMPEG_TARBALL" ]]; then
    curl -L --fail --retry 2 -o "$FFMPEG_TARBALL" https://ffmpeg.org/releases/ffmpeg-7.1.3.tar.xz
  fi
  tar -C "$ROOT" -xf "$FFMPEG_TARBALL"
fi

if [[ ! -f "$ROOT/testsrc_cabac_p.h264" ]]; then
  "$FFMPEG_BIN" -hide_banner -y \
    -f lavfi -i testsrc=size=320x240:rate=30 \
    -frames:v 30 \
    -c:v libx264 -threads:v 1 -flags +bitexact -profile:v main -preset veryfast \
    -x264-params cabac=1:bframes=0:keyint=30:min-keyint=30:scenecut=0:threads=1:lookahead_threads=1 \
    -pix_fmt yuv420p -f h264 "$ROOT/testsrc_cabac_p.h264"
fi
verify_fixture_hash "$ROOT/testsrc_cabac_p.h264" "$TESTSRC_SHA256" "testsrc_cabac_p.h264"

if [[ ! -f "$ROOT/bbb_annexb.h264" ]]; then
  if [[ ! -f "$BBB_SRC" ]]; then
    if [[ ! -f "$BBB_ZIP" ]]; then
      curl -L --fail --retry 2 -o "$BBB_ZIP" "$BBB_URL"
    fi
    unzip -o "$BBB_ZIP" -d "$ROOT"
  fi
  verify_fixture_hash "$BBB_SRC" "$BBB_SOURCE_SHA256" "BigBuckBunny_640x360.m4v"
  "$FFMPEG_BIN" -hide_banner -y \
    -threads:v 1 -i "$BBB_SRC" -an \
    -vf 'scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2' \
    -frames:v 300 \
    -c:v libx264 -threads:v 1 -flags +bitexact -profile:v high -preset medium -crf 23 -g 300 -bf 3 \
    -pix_fmt yuv420p \
    -x264-params cabac=1:ref=3:b-adapt=1:keyint=300:min-keyint=300:scenecut=0:threads=1:lookahead_threads=1 \
    -f h264 "$ROOT/bbb_annexb.h264"
fi
verify_fixture_hash "$ROOT/bbb_annexb.h264" "$BBB_SHA256" "bbb_annexb.h264"

printf 'ffmpeg_src=%s\n' "$FFMPEG_SRC"
printf 'testsrc=%s sha256=%s\n' "$ROOT/testsrc_cabac_p.h264" "$(sha256sum "$ROOT/testsrc_cabac_p.h264" | awk '{print $1}')"
printf 'bbb=%s sha256=%s\n' "$ROOT/bbb_annexb.h264" "$(sha256sum "$ROOT/bbb_annexb.h264" | awk '{print $1}')"
