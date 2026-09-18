#!/usr/bin/env bash
set -euo pipefail

ROOT="${GO264_CONFORMANCE_ROOT:-/workspace/tmp/h264-conformance}"
BASE_URL="${GO264_CONFORMANCE_URL:-https://fate-suite.ffmpeg.org/h264-conformance}"
mkdir -p "$ROOT"

fetch() {
  local name=$1 want=$2 path="$ROOT/$1" got
  if [[ ! -f "$path" ]]; then
    curl -L --fail --retry 2 -o "$path" "$BASE_URL/$name"
  fi
  got=$(sha256sum "$path" | awk '{print $1}')
  if [[ "$got" != "$want" ]]; then
    printf 'fixture hash mismatch for %s: got=%s want=%s\n' "$name" "$got" "$want" >&2
    exit 1
  fi
  printf 'READY %-24s sha256=%s\n' "$name" "$got"
}

fetch CVWP2_TOSHIBA_E.264 6b2b6205398d2cfebbee5708c4d4d8c67bb56cd4991f1cc4d90836a14215e257
fetch HCMP1_HHI_A.264 f9bf6d36a7250dd86cf325cd458e92f3319236dcde075000e70f73028f05111a
fetch MR1_BT_A.h264 20dc67331c81adcf40048bb37357883a69b3ab002b0927e599f43d86be9c3d8b
