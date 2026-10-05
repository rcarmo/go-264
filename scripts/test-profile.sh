#!/usr/bin/env bash
set -euo pipefail

# Build and profile each Go test package separately. Evidence is retained outside
# disposable project scratch so clean-up cannot remove it.
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
out="$EVIDENCE/tests/$run_id"
mkdir -p "$out"
printf '%s\n' "revision=$(git rev-parse HEAD)" "toolchain=$(go version)" "flags=race=$race tags=$tags run=$run bench=$bench benchtime=$benchtime timeout=$timeout" "packages=${specs[*]}" "cpu_profile_hz=100" "memprofilerate=524288" "GOCACHE=$GOCACHE" "GOMODCACHE=$GOMODCACHE" "GOTMPDIR=$GOTMPDIR" "TMPDIR=$TMPDIR" >"$out/manifest.txt"
if ! go list "${build_flags[@]}" -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' "${specs[@]}" >"$out/packages.txt" 2>"$out/list.log"; then
  echo "package discovery failed: $out/list.log" >&2; exit 1
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
    echo "BUILD FAILED $pkg: missing profiles (see $dir/build.log)" | tee -a "$out/analysis.txt"; status=1; continue
  fi
  sha256sum "$bin" >"$dir/binary.sha256"
  workload_flags=()
  if [[ -n "$bench" ]]; then workload_flags+=(-test.bench="$bench" -test.benchtime="$benchtime" -test.benchmem); fi
  printf '%s\n' "$bin -test.run=$run -test.count=1 ${workload_flags[*]} -test.cpuprofile=$dir/cpu.pprof -test.memprofile=$dir/heap.pprof -test.memprofilerate=524288" >>"$dir/command.txt"
  if ! (cd "$pkg_dir" && "$bin" -test.run="$run" -test.count=1 -test.timeout="$timeout" "${workload_flags[@]}" -test.cpuprofile="$dir/cpu.pprof" -test.memprofile="$dir/heap.pprof" -test.memprofilerate=524288) >"$dir/test.log" 2>&1; then
    echo "TEST FAILED $pkg (see $dir/test.log)" | tee -a "$out/analysis.txt"; status=1
  fi
  if [[ ! -s "$dir/cpu.pprof" || ! -s "$dir/heap.pprof" ]]; then
    echo "CAPTURE FAILED $pkg: missing CPU or heap profile" | tee -a "$out/analysis.txt"; status=1; continue
  fi
  if ! go tool pprof -top -cum -nodecount=60 "$bin" "$dir/cpu.pprof" >"$dir/cpu-cum.txt" 2>"$dir/cpu-error.log"; then
    echo "CPU ANALYSIS FAILED $pkg" | tee -a "$out/analysis.txt"; status=1
  fi
  for sample in alloc_space alloc_objects; do
    if ! go tool pprof -sample_index="$sample" -top -cum -nodecount=60 "$bin" "$dir/heap.pprof" >"$dir/$sample-cum.txt" 2>"$dir/$sample-error.log"; then
      echo "$sample ANALYSIS FAILED $pkg" | tee -a "$out/analysis.txt"; status=1
    fi
  done
  if grep -q 'Total samples = 0' "$dir/cpu-cum.txt"; then
    echo "EMPTY CPU SAMPLES $pkg: obtain a representative profile before performance acceptance" | tee -a "$out/analysis.txt"
  fi
  printf 'PROFILED %s (inspect cumulative CPU, alloc_space, alloc_objects in %s)\n' "$pkg" "$dir" | tee -a "$out/analysis.txt"
done <"$out/packages.txt"
echo "retained test evidence: $out"
exit "$status"
