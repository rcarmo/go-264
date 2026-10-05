#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/profile_matrix.sh [options]

Prepare disposable go-264 CPU/allocation profiling runs and retain concise conclusions.
Workloads run only when both --run and GO264_PROFILE_RUN=1 are supplied.

Options:
  --run                 Run the prepared six-workload matrix.
  --output DIR          Disposable run directory under the resolved project runs/.
  --cpu-list LIST       taskset CPU list (default: 0,1).
  --video PATH          Diagnostic Annex B fixture.
  --m4a PATHS           Colon-separated immutable AAC/MP4 fixtures.
  --wav PATH            Immutable PCM WAV fixture.
  -h, --help            Show this help.

The caller must obtain compute admission before --run. Profile-instrumented
ns/op values are not speed evidence. Use a separate non-test harness for speed
comparisons; compare only identical binaries, fixtures, affinity and commands.
EOF
}

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workspace=$(cd "$repo/../.." && pwd)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
source "$repo/scripts/project-tmp.sh"
root=$(project_tmp_resolve go-264)
out="$root/runs/profile-matrix/$stamp-$$"
cpu_list=0,1
run=0
fixtures="${GO264_FIXTURE_ROOT:-${GO264_EVIDENCE_ROOT:-/workspace/reports/go-264}/fixtures}"
video="${GO264_PROFILE_VIDEO:-$fixtures/bbb_annexb.h264}"
m4a="${GO264_PROFILE_M4A:-$fixtures/audio/tone.m4a:$fixtures/audio/noise.m4a:$fixtures/audio/transient.m4a}"
wav="${GO264_PROFILE_WAV:-$fixtures/audio/tone_stereo_48000.src.wav}"

while (($#)); do
  case "$1" in
    --run) run=1; shift ;;
    --output) out=$2; shift 2 ;;
    --cpu-list) cpu_list=$2; shift 2 ;;
    --video) video=$2; shift 2 ;;
    --m4a) m4a=$2; shift 2 ;;
    --wav) wav=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for tool in go git taskset timeout sha256sum /usr/bin/time; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done

case "$out" in "$root/runs/profile-matrix/"*) ;; *) echo 'profile output must be under the resolved project runs/profile-matrix/' >&2; exit 2;; esac
project_path_usable "$out" || { echo "Unsafe profile run path: $out" >&2; exit 2; }
[[ ! -e "$out" && ! -L "$out" ]] || { echo "profile run path already exists; refusing to reuse: $out" >&2; exit 2; }
source "$repo/scripts/project-env.sh"
export PROJECT_TMP_ROOT="$root"
go264_init_paths profile-matrix "$stamp-$$"
mkdir -p "$out/bin"
out=$(cd "$out" && pwd)
# All preparation/build failures must dispose of this run's scratch too.
cleanup_matrix() {
  local code=$? active
  trap - EXIT
  active=$(jobs -pr || true)
  if [[ -n "$active" ]]; then
    echo "profile child still active ($active); preserving $out until safe cleanup" >&2
    return "$code"
  fi
  if ((code != 0)) && [[ "$out" == "$root/runs/profile-matrix/"* ]]; then
    local conclusion="$GO264_EVIDENCE_ROOT/conclusions/profile-matrix-$stamp-$$.txt"
    if project_path_usable "${conclusion%/*}" && [[ ! -s "$conclusion" ]]; then
      mkdir -p "${conclusion%/*}"
      printf 'revision=%s\ntoolchain=%s\nstatus=failed (exit %d)\nlimitations=matrix did not complete; partial or empty captures are not passing profiling evidence.\n' \
        "$(git -C "$repo" rev-parse HEAD)" "$(go version)" "$code" >"$conclusion"
    fi
  fi
  if [[ "$out" == "$root/runs/profile-matrix/"* && -d "$out" && ! -L "$out" && -O "$out" ]]; then
    rm -rf -- "$out"
  fi
  return "$code"
}
trap cleanup_matrix EXIT
export CGO_ENABLED=0 GOTOOLCHAIN=local GOMAXPROCS=2 GOPROXY=off

mapfile -t m4a_files < <(printf '%s' "$m4a" | tr ':' '\n')
fixtures=("$video" "$wav" "${m4a_files[@]}")
for fixture in "${fixtures[@]}"; do
  [[ -n "$fixture" && -f "$fixture" ]] || { echo "missing fixture: $fixture" >&2; exit 1; }
done

head=$(git -C "$repo" rev-parse HEAD)
status=$(git -C "$repo" status --porcelain)
{
  printf 'revision=%s\n' "$head"
  printf 'go=%s\n' "$(go version)"
  printf 'cgo=%s\n' "$CGO_ENABLED"
  printf 'gomaxprocs=%s\n' "$GOMAXPROCS"
  printf 'cpu_list=%s\n' "$cpu_list"
  printf 'run_requested=%s\n' "$run"
  printf 'working_tree_clean=%s\n' "$([[ -z "$status" ]] && echo true || echo false)"
  printf 'video=%s\n' "$video"
  printf 'm4a=%s\n' "$m4a"
  printf 'wav=%s\n' "$wav"
} > "$out/MANIFEST.txt"
sha256sum "${fixtures[@]}" > "$out/FIXTURES.SHA256SUMS"

(
  cd "$repo"
  go test -p=1 -c -o "$out/bin/video.test" ./decode
  go test -p=1 -c -o "$out/bin/audio.test" ./audio
  go test -p=1 -c -o "$out/bin/resample.test" ./audio/resample
)
sha256sum "$out"/bin/*.test > "$out/BINARIES.SHA256SUMS"

# Escape reports are compile-time evidence and do not execute benchmarks.
(
  cd "$repo"
  go build -gcflags='github.com/rcarmo/go-264/...=-m=2' ./decode ./audio ./audio/resample
) > "$out/escape-analysis.log" 2>&1

cat > "$out/COMMANDS.txt" <<EOF
All workload commands use: taskset -c $cpu_list env GOMAXPROCS=2 CGO_ENABLED=0
Profiled timings are NOT speed evidence.
video: GO264_BBB_BENCH_FIXTURE=$video video.test BenchmarkDecodeBBBbaseline benchtime=1x
AAC source: GO264_PROFILE_M4A=$m4a audio.test BenchmarkProfileDecode/aac-source-stereo benchtime=1s
AAC canonical: GO264_PROFILE_M4A=$m4a audio.test BenchmarkProfileDecode/aac-canonical-mono benchtime=1s
WAV source: GO264_PROFILE_WAV=$wav audio.test BenchmarkProfileDecode/wav-source-stereo benchtime=1s
WAV canonical: GO264_PROFILE_WAV=$wav audio.test BenchmarkProfileDecode/wav-canonical-mono benchtime=1s
resampler: resample.test Benchmark48000To16000 benchtime=1s
Each workload emits: CPU profile, normal-rate heap profile, alloc_space,
alloc_objects, inuse_space, inuse_objects and profiled benchmem. Independent
speed evidence needs a separate non-test harness with equivalent inputs.
EOF

if ((run == 0)); then
  echo "prepared matrix metadata; disposable preparation files will be deleted on exit"
  echo "workloads not run (pass --run with GO264_PROFILE_RUN=1 after admission)"
  exit 0
fi
if [[ ${GO264_PROFILE_RUN:-} != 1 ]]; then
  echo "refusing to run: set GO264_PROFILE_RUN=1 after compute admission" >&2
  exit 1
fi

# Refuse overlap with another material process already on the admitted CPUs.
IFS=, read -r cpu_a cpu_b <<<"$cpu_list"
if ps -eo pid=,psr=,pcpu=,comm=,args= | awk -v a="$cpu_a" -v b="$cpu_b" '$2==a || $2==b { if ($3+0 >= 20 && $4 !~ /^go264-/) { print; busy=1 } } END { exit busy ? 1 : 0 }'; then
  :
else
  echo "refusing to run: admitted CPUs are busy" >&2
  exit 1
fi

log="$out/window.log"
: > "$log"
start=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)

run_one() {
  local label=$1 binary=$2 benchmark=$3 benchtime=$4 env_name=$5 env_value=$6
  local -a prefix=(taskset -c "$cpu_list" env GOMAXPROCS=2 CGO_ENABLED=0)
  if [[ -n "$env_name" ]]; then
    prefix+=("$env_name=$env_value")
  fi
  echo "BEGIN $label $(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" | tee -a "$log"
  (cd "$repo" && timeout --signal=KILL 12s "${prefix[@]}" "$binary" \
    -test.run='^$' -test.bench="$benchmark" -test.benchtime="$benchtime" -test.count=1 -test.timeout=10s -test.benchmem \
    -test.cpuprofile="$out/$label.cpu.pprof" -test.memprofile="$out/$label.heap.pprof" -test.memprofilerate=524288) \
    2>&1 | tee -a "$log"
  if ! awk -v target="$label" '$1=="BEGIN" {on=($2==target); next} $1=="END" && $2==target {on=0} on && /^Benchmark/ {found=1; exit} END {exit found ? 0 : 1}' "$log"; then
    echo "benchmark $label produced no measurements; missing/empty capture" | tee -a "$log" >&2
    return 1
  fi
  # Avoid a second run while profiling; use an equivalent non-test harness for speed comparisons.
  for sample in alloc_space alloc_objects inuse_space inuse_objects; do
    go tool pprof -sample_index="$sample" -top -nodecount=100 "$binary" "$out/$label.heap.pprof" > "$out/$label.$sample.txt"
  done
  go tool pprof -top -cum -nodecount=120 "$binary" "$out/$label.cpu.pprof" > "$out/$label.cpu-top.txt"
  for sample in alloc_space alloc_objects; do
    go tool pprof -sample_index="$sample" -top -cum -nodecount=100 "$binary" "$out/$label.heap.pprof" >"$out/$label.$sample-cum.txt"
  done
  for report in "$out/$label.cpu-top.txt" "$out/$label.alloc_space-cum.txt" "$out/$label.alloc_objects-cum.txt"; do
    if grep -q 'Total samples = 0' "$report"; then
      echo "EMPTY PROFILE SAMPLES $label ${report##*/}: not passing profiling evidence" | tee -a "$log" >&2
      return 1
    fi
  done
  echo "END $label $(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" | tee -a "$log"
}

run_one video "$out/bin/video.test" '^BenchmarkDecodeBBBbaseline$' 1x GO264_BBB_BENCH_FIXTURE "$video"
run_one aac-source "$out/bin/audio.test" '^BenchmarkProfileDecode/aac-source-stereo$' 1s GO264_PROFILE_M4A "$m4a"
run_one aac-canonical "$out/bin/audio.test" '^BenchmarkProfileDecode/aac-canonical-mono$' 1s GO264_PROFILE_M4A "$m4a"
run_one wav-source "$out/bin/audio.test" '^BenchmarkProfileDecode/wav-source-stereo$' 1s GO264_PROFILE_WAV "$wav"
run_one wav-canonical "$out/bin/audio.test" '^BenchmarkProfileDecode/wav-canonical-mono$' 1s GO264_PROFILE_WAV "$wav"
run_one resampler "$out/bin/resample.test" '^Benchmark48000To16000$' 1s '' ''

end=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
printf 'window_start=%s\nwindow_end=%s\n' "$start" "$end" >> "$out/MANIFEST.txt"
if ps -eo pid=,comm=,args= | awk '$2 ~ /^go264-/ { print; found=1 } END { exit found ? 1 : 0 }' > "$out/SURVIVORS.txt"; then
  :
else
  echo "profile process survived workload" >&2
  exit 1
fi
conclusions="$GO264_EVIDENCE_ROOT/conclusions/profile-matrix-$stamp-$$.txt"
project_path_usable "${conclusions%/*}" || { echo 'Unsafe conclusions path' >&2; exit 1; }
mkdir -p "${conclusions%/*}"
{
  printf 'revision=%s\ntoolchain=%s\nwindow_start=%s\nwindow_end=%s\n' "$head" "$(go version)" "$start" "$end"
  printf 'fixtures: %s\n' "$(sha256sum "${fixtures[@]}" | tr '\n' ';')"
  for label in video aac-source aac-canonical wav-source wav-canonical resampler; do
    printf '\nworkload=%s\n' "$label"
    awk -v target="$label" '$1=="BEGIN" {on=($2==target)} on && /^Benchmark/ {print; exit}' "$out/window.log"
    for metric in cpu alloc_space alloc_objects; do
      report="$out/$label.cpu-top.txt"
      [[ "$metric" == cpu ]] || report="$out/$label.$metric-cum.txt"
      printf '%s: %s\n' "$metric" "$(grep -m1 'Total samples' "$report" || echo 'capture missing or empty')"
      awk 'index($0,"github.com/rcarmo/go-264/") {print; if (++n == 3) exit}' "$report"
    done
  done
  printf '\nlimitations=Profile-instrumented benchmark timings are attribution only; no equivalent unprofiled speed comparison.\n'
} >"$conclusions"
echo "profile analysis complete; concise findings: $conclusions"
echo 'raw profiles, matching binaries and disposable logs deleted on exit'
