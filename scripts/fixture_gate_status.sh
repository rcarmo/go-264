#!/usr/bin/env bash
set -euo pipefail

STRICT=0
ROOT="${GO264_FIXTURE_ROOT:-/workspace/tmp}"
while (($#)); do
  case "$1" in
    --strict) STRICT=1 ;;
    --root) shift; ROOT="${1:?--root requires a directory}" ;;
    -h|--help)
      cat <<'EOF'
usage: scripts/fixture_gate_status.sh [--strict] [--root DIR]

Reports fixture-dependent decoder gates without downloading or generating media.
--strict exits non-zero unless the pinned BBB stream, three Phase 4 vectors and FFmpeg 7.1.3 are ready.
GO264_FIXTURE_ROOT defaults to /workspace/tmp.
EOF
      exit 0
      ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

PINNED_BBB_SHA=1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff
CONFORMANCE_ROOT="${GO264_CONFORMANCE_ROOT:-$ROOT/h264-conformance}"
missing=0
invalid=0
ready=0

status_file() {
  local gate=$1 path=$2 requirement=${3:-optional}
  if [[ -f "$path" ]]; then
    printf 'READY       %-24s %s\n' "$gate" "$path"
    ready=$((ready+1))
  else
    printf 'UNAVAILABLE %-24s %s\n' "$gate" "$path"
    if [[ "$requirement" == required ]]; then
      missing=$((missing+1))
    fi
  fi
}

bbb="$ROOT/bbb_annexb.h264"
if [[ -f "$bbb" ]]; then
  got=$(sha256sum "$bbb" | awk '{print $1}')
  if [[ "$got" == "$PINNED_BBB_SHA" ]]; then
    printf 'READY       %-24s %s sha256=%s\n' pinned-bbb "$bbb" "$got"
    ready=$((ready+1))
  else
    printf 'INVALID     %-24s %s sha256=%s want=%s\n' pinned-bbb "$bbb" "$got" "$PINNED_BBB_SHA"
    invalid=$((invalid+1))
  fi
else
  printf 'UNAVAILABLE %-24s %s sha256=%s\n' pinned-bbb "$bbb" "$PINNED_BBB_SHA"
  missing=$((missing+1))
fi

ffmpeg="${GO264_FFMPEG_BIN:-$ROOT/ffmpeg-7.1.3/ffmpeg}"
if [[ -x "$ffmpeg" ]]; then
  version=$({ "$ffmpeg" -version || true; } | head -1)
  if [[ "$version" == *"ffmpeg version 7.1.3"* || "$version" == *"ffmpeg version n7.1.3"* ]]; then
    printf 'READY       %-24s %s (%s)\n' ffmpeg-7.1.3 "$ffmpeg" "$version"
    ready=$((ready+1))
  else
    printf 'INVALID     %-24s %s (%s)\n' ffmpeg-7.1.3 "$ffmpeg" "${version:-no version output}"
    invalid=$((invalid+1))
  fi
else
  printf 'UNAVAILABLE %-24s %s\n' ffmpeg-7.1.3 "$ffmpeg"
  missing=$((missing+1))
fi

status_hash() {
  local gate=$1 path=$2 want=$3 requirement=${4:-optional} got
  if [[ ! -f "$path" ]]; then
    printf 'UNAVAILABLE %-24s %s sha256=%s\n' "$gate" "$path" "$want"
    [[ "$requirement" == required ]] && missing=$((missing+1))
    return
  fi
  got=$(sha256sum "$path" | awk '{print $1}')
  if [[ "$got" == "$want" ]]; then
    printf 'READY       %-24s %s sha256=%s\n' "$gate" "$path" "$got"
    ready=$((ready+1))
  else
    printf 'INVALID     %-24s %s sha256=%s want=%s\n' "$gate" "$path" "$got" "$want"
    invalid=$((invalid+1))
  fi
}

status_hash phase4-weighted-b "$CONFORMANCE_ROOT/CVWP2_TOSHIBA_E.264" 6b2b6205398d2cfebbee5708c4d4d8c67bb56cd4991f1cc4d90836a14215e257 required
status_hash phase4-b-list "$CONFORMANCE_ROOT/HCMP1_HHI_A.264" f9bf6d36a7250dd86cf325cd458e92f3319236dcde075000e70f73028f05111a required
status_hash phase4-long-term "$CONFORMANCE_ROOT/MR1_BT_A.h264" 20dc67331c81adcf40048bb37357883a69b3ab002b0927e599f43d86be9c3d8b required

status_file cabac-trace "$ROOT/testsrc_cabac_p.h264"
status_file baseline-cavlc "$ROOT/testsrc_bl.h264"
status_file gray16 "$ROOT/gray16.h264"
status_file dark64 "$ROOT/dark64.h264"
status_file bbb-baseline "$ROOT/bbb_baseline.h264"
status_file baseline-reference "$ROOT/bl_ref_yuv/ref.yuv"
status_file bbb-reference-png "$ROOT/bbb_ref_0001.png"

printf '\nready=%d required_missing=%d invalid=%d strict=%d\n' "$ready" "$missing" "$invalid" "$STRICT"
printf 'ordinary go test: optional fixture tests may skip; this report names those inputs.\n'
printf 'pinned parity: GO264_FFMPEG_REGRESSION=1 makes missing or invalid required inputs fail.\n'

if ((STRICT && (missing || invalid))); then
  exit 1
fi
