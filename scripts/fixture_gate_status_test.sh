#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/project-env.sh"
go264_init_paths fixture-gate-status-test

RUNS="$GO264_RUN_ROOT/fixture-status"
mkdir -p "$RUNS"
ROOT=$(mktemp -d "$RUNS/$(date -u +%Y%m%dT%H%M%SZ)-$$-XXXXXX")
trap 'rm -rf -- "$ROOT"' EXIT
SCRIPT=$(cd "$(dirname "$0")" && pwd)/fixture_gate_status.sh

output=$(GO264_CONFORMANCE_ROOT="$ROOT/h264-conformance" GO264_FFMPEG_BIN="$ROOT/ffmpeg-7.1.3/ffmpeg" $SCRIPT --root "$ROOT")
grep -q '^UNAVAILABLE pinned-bbb' <<<"$output"
grep -q 'required_missing=6 invalid=0 strict=0' <<<"$output"
if GO264_CONFORMANCE_ROOT="$ROOT/h264-conformance" GO264_FFMPEG_BIN="$ROOT/ffmpeg-7.1.3/ffmpeg" $SCRIPT --strict --root "$ROOT" >/dev/null 2>&1; then
  echo "strict mode accepted missing required gates" >&2
  exit 1
fi

printf wrong >"$ROOT/bbb_annexb.h264"
mkdir -p "$ROOT/ffmpeg-7.1.3"
cat >"$ROOT/ffmpeg-7.1.3/ffmpeg" <<'EOF'
#!/usr/bin/env sh
echo 'ffmpeg version 8.0'
EOF
chmod +x "$ROOT/ffmpeg-7.1.3/ffmpeg"
output=$(GO264_CONFORMANCE_ROOT="$ROOT/h264-conformance" GO264_FFMPEG_BIN="$ROOT/ffmpeg-7.1.3/ffmpeg" $SCRIPT --root "$ROOT")
grep -q '^INVALID     pinned-bbb' <<<"$output"
grep -q '^INVALID     ffmpeg-7.1.3' <<<"$output"
grep -q 'required_missing=4 invalid=2 strict=0' <<<"$output"

echo 'fixture gate status tests pass'
