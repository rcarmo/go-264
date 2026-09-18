#!/usr/bin/env bash
set -euo pipefail

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/go264-fixture-status-XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
SCRIPT=$(cd "$(dirname "$0")" && pwd)/fixture_gate_status.sh

output=$($SCRIPT --root "$ROOT")
grep -q '^UNAVAILABLE pinned-bbb' <<<"$output"
grep -q 'required_missing=5 invalid=0 strict=0' <<<"$output"
if $SCRIPT --strict --root "$ROOT" >/dev/null 2>&1; then
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
output=$($SCRIPT --root "$ROOT")
grep -q '^INVALID     pinned-bbb' <<<"$output"
grep -q '^INVALID     ffmpeg-7.1.3' <<<"$output"
grep -q 'required_missing=3 invalid=2 strict=0' <<<"$output"

echo 'fixture gate status tests pass'
