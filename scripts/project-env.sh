#!/usr/bin/env bash
# Source from repository helpers before assigning TMPDIR; never moves old data.
source "$(dirname "${BASH_SOURCE[0]}")/project-tmp.sh"

go264_init_paths() {
  local purpose="$1" id="${2:-$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM}" root expected
  root="$(project_tmp_resolve go-264)" || return 1
  project_tmp_init "$root" || return 1
  for expected in GO264_CACHE_ROOT GO264_BUILD_ROOT GO264_TEST_ROOT GO264_LOG_ROOT GO264_RUN_ROOT; do
    local value="${!expected-}"
    local suffix
    case "$expected" in GO264_CACHE_ROOT) suffix=cache;; GO264_BUILD_ROOT) suffix=build;; GO264_TEST_ROOT) suffix=tests;; GO264_LOG_ROOT) suffix=logs;; *) suffix=runs;; esac
    if [[ -n "$value" && "$value" != "$root/$suffix" ]]; then
      echo "$expected must be $root/$suffix" >&2; return 1
    fi
  done
  export PROJECT_TMP_ROOT="$root" GO264_CACHE_ROOT="$root/cache" GO264_BUILD_ROOT="$root/build" GO264_TEST_ROOT="$root/tests" GO264_LOG_ROOT="$root/logs" GO264_RUN_ROOT="$root/runs"
  if [[ -z "${GO264_EVIDENCE_ROOT:-}" ]]; then
    if project_is_ci; then GO264_EVIDENCE_ROOT="$root"
    elif project_path_usable /workspace/reports/go-264; then GO264_EVIDENCE_ROOT=/workspace/reports/go-264
    else GO264_EVIDENCE_ROOT="$root"; fi
  fi
  project_path_usable "$GO264_EVIDENCE_ROOT" || { echo "Unsafe evidence root: $GO264_EVIDENCE_ROOT" >&2; return 1; }
  export GO264_EVIDENCE_ROOT
  local fixture_default="$GO264_EVIDENCE_ROOT/fixtures"
  [[ "$GO264_EVIDENCE_ROOT" != "$root" ]] || fixture_default="$root/tests/fixtures"
  export GO264_FIXTURE_ROOT="${GO264_FIXTURE_ROOT:-$fixture_default}"
  export GO264_CONFORMANCE_ROOT="${GO264_CONFORMANCE_ROOT:-$GO264_FIXTURE_ROOT/h264-conformance}"
  export GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/go-path" XDG_CACHE_HOME="$root/cache/xdg"
  export PYTHONPYCACHEPREFIX="$root/cache/python/pycache" PIP_CACHE_DIR="$root/cache/python/pip" UV_CACHE_DIR="$root/cache/uv"
  export TMPDIR="$root/runs/$purpose/$id/temp" TMP="$root/runs/$purpose/$id/temp" TEMP="$root/runs/$purpose/$id/temp" GOTMPDIR="$root/runs/$purpose/$id/go"
  for expected in "$GOCACHE" "$GOMODCACHE" "$GOPATH" "$XDG_CACHE_HOME" "$PYTHONPYCACHEPREFIX" "$PIP_CACHE_DIR" "$UV_CACHE_DIR" "$TMPDIR" "$GOTMPDIR"; do
    project_path_usable "$expected" || { echo "Unsafe project path: $expected" >&2; return 1; }
    mkdir -p "$expected"
  done
}
