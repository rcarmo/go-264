#!/usr/bin/env bash
set -euo pipefail

# Opt-in pre-release/diagnostic profiling. Analyse each package, retain concise
# findings, and dispose of raw captures, matching binaries and run logs.
source "$(dirname "$0")/project-env.sh"
# Validate the explicit root and resolve fallback before changing child TMPDIR.
ROOT="$(project_tmp_resolve go-264)"
if [[ -n "${GO264_EVIDENCE_ROOT:-}" ]]; then EVIDENCE=$GO264_EVIDENCE_ROOT
elif project_is_ci; then EVIDENCE="$ROOT"
elif project_path_usable /workspace/reports/go-264; then EVIDENCE=/workspace/reports/go-264
else EVIDENCE="$ROOT"; fi
project_path_usable "$EVIDENCE" || { echo "Unsafe evidence root: $EVIDENCE" >&2; exit 1; }

race=0 tags= run='.' bench= benchtime=1x timeout=10m
declare -a specs=()
while (($#)); do
  case "$1" in
    -race) race=1; shift ;;
    -tags|-run|-bench|-benchtime|-timeout)
      [[ $# -ge 2 ]] || { echo "missing value for $1" >&2; exit 2; }
      case "$1" in -tags) tags=$2 ;; -run) run=$2 ;; -bench) bench=$2 ;; -benchtime) benchtime=$2 ;; -timeout) timeout=$2 ;; esac
      shift 2 ;;
    -tags=*) tags=${1#*=}; shift ;;
    -run=*) run=${1#*=}; shift ;;
    -bench=*) bench=${1#*=}; shift ;;
    -benchtime=*) benchtime=${1#*=}; shift ;;
    -timeout=*) timeout=${1#*=}; shift ;;
    -*) echo "unsupported profile test flag: $1" >&2; exit 2 ;;
    *) specs+=("$1"); shift ;;
  esac
done
((${#specs[@]})) || specs=(./...)
build_flags=(); [[ -z "$tags" ]] || build_flags+=(-tags "$tags"); ((race==0)) || build_flags+=(-race)
run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM
# Reuse the validated root; never re-resolve after TMPDIR points at a child.
export PROJECT_TMP_ROOT="$ROOT"
go264_init_paths tests "$run_id"
out="$GO264_TEST_ROOT/$run_id"
findings="$EVIDENCE/conclusions/$run_id.txt"
project_path_usable "$out" && project_path_usable "${findings%/*}" || { echo 'Unsafe test scratch or conclusions path' >&2; exit 1; }
mkdir -p "$out" "${findings%/*}"
printf '%s\n' "revision=$(git rev-parse HEAD)" "toolchain=$(go version)" "flags=race=$race tags=$tags run=$run bench=$bench benchtime=$benchtime timeout=$timeout" "packages=${specs[*]}" "cpu_profile_hz=100" "memprofilerate=524288" >"$findings"
# Dispose of this owned run on success, test failure, build failure or abort.
# Never remove captures if a child is still running; finish it at a safe boundary.
cleanup_run() {
  local result=$? scratch="$ROOT/runs/tests/$run_id" active
  trap - EXIT
  active=$(jobs -pr || true)
  if [[ -n "$active" ]]; then
    printf 'raw capture cleanup deferred: child still active (%s)\n' "$active" | tee -a "$findings" >&2
    return "$result"
  fi
  if [[ "$out" == "$ROOT/tests/"* && -d "$out" && ! -L "$out" && -O "$out" ]]; then
    rm -rf -- "$out"
  fi
  if [[ "$scratch" == "$ROOT/runs/tests/"* && -d "$scratch" && ! -L "$scratch" && -O "$scratch" ]]; then
    rm -rf -- "$scratch"
  fi
  return "$result"
}
trap cleanup_run EXIT
if ! go list "${build_flags[@]}" -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' "${specs[@]}" >"$out/packages.txt" 2>"$out/list.log"; then
  printf 'package discovery failed: %s\n' "$(tail -n 1 "$out/list.log")" | tee -a "$findings" >&2
  exit 1
fi
status=0
while IFS= read -r pkg; do
  [[ -n "$pkg" ]] || continue
  name=${pkg#github.com/rcarmo/go-264/}; [[ "$name" != "$pkg" ]] || name=root
  name=${name//\//__}; dir="$out/$name"; mkdir -p "$dir"
  bin="$dir/test.bin"
  pkg_dir=$(go list -f '{{.Dir}}' "$pkg")
  printf 'go test %s -c -o %s %s\npackage_dir=%s\n' "${build_flags[*]}" "$bin" "$pkg" "$pkg_dir" >"$dir/command.txt"
  if ! go test "${build_flags[@]}" -c -o "$bin" "$pkg" >"$dir/build.log" 2>&1; then
    printf 'BUILD FAILED %s: missing profiles; %s\n' "$pkg" "$(tail -n 1 "$dir/build.log")" | tee -a "$findings" >&2
    status=1; rm -rf -- "$dir"; continue
  fi
  sha256sum "$bin" >"$dir/binary.sha256"
  workload_flags=()
  if [[ -n "$bench" ]]; then workload_flags+=(-test.bench="$bench" -test.benchtime="$benchtime" -test.benchmem); fi
  printf '%s\n' "$bin -test.run=$run -test.count=1 ${workload_flags[*]} -test.cpuprofile=$dir/cpu.pprof -test.memprofile=$dir/heap.pprof -test.memprofilerate=524288" >>"$dir/command.txt"
  if ! (cd "$pkg_dir" && "$bin" -test.run="$run" -test.count=1 -test.timeout="$timeout" "${workload_flags[@]}" -test.cpuprofile="$dir/cpu.pprof" -test.memprofile="$dir/heap.pprof" -test.memprofilerate=524288) >"$dir/test.log" 2>&1; then
    printf 'TEST FAILED %s\n' "$pkg" | tee -a "$findings" >&2
    grep -m 5 -E '^--- FAIL|^panic:|\.go:[0-9]+:' "$dir/test.log" >>"$findings" || true
    status=1
  fi
  if [[ ! -s "$dir/cpu.pprof" || ! -s "$dir/heap.pprof" ]]; then
    echo "CAPTURE FAILED $pkg: missing CPU or heap profile" | tee -a "$findings" >&2
    status=1; rm -rf -- "$dir"; continue
  fi
  if ! go tool pprof -top -cum -nodecount=60 "$bin" "$dir/cpu.pprof" >"$dir/cpu-cum.txt" 2>"$dir/cpu-error.log"; then
    echo "CPU ANALYSIS FAILED $pkg" | tee -a "$findings" >&2
    status=1
  fi
  for sample in alloc_space alloc_objects; do
    if ! go tool pprof -sample_index="$sample" -top -cum -nodecount=60 "$bin" "$dir/heap.pprof" >"$dir/$sample-cum.txt" 2>"$dir/$sample-error.log"; then
      echo "$sample ANALYSIS FAILED $pkg" | tee -a "$findings" >&2
      status=1
    fi
  done
  result=$(tail -n 1 "$dir/test.log")
  case "$result" in PASS|FAIL) ;; *) result=FAIL ;; esac
  printf '\npackage=%s result=%s\n' "$pkg" "$result" >>"$findings"
  if [[ -n "$bench" ]]; then grep '^Benchmark' "$dir/test.log" >>"$findings" || true; fi
  for report in cpu alloc_space alloc_objects; do
    file="$dir/$report-cum.txt"
    if [[ ! -s "$file" ]]; then printf '%s analysis unavailable\n' "$report" >>"$findings"; continue; fi
    printf '%s: %s\n' "$report" "$(grep -m1 -E 'Total samples|total$' "$file" || echo 'samples unavailable')" >>"$findings"
    awk 'index($0,"github.com/rcarmo/go-264/") {print; if (++n == 3) exit}' "$file" >>"$findings"
  done
  if grep -q 'Total samples = 0' "$dir/cpu-cum.txt"; then
    echo "EMPTY CPU SAMPLES $pkg: obtain a representative profile before performance acceptance" | tee -a "$findings" >&2
  fi
  printf 'Analysed %s; disposing raw profiles, binary and logs\n' "$pkg"
  rm -rf -- "$dir"
done <"$out/packages.txt"
printf '\nexit_status=%d\n' "$status" >>"$findings"
printf 'retained concise findings: %s\n' "$findings"
exit "$status"
