#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/profile_matrix.sh [options]

Prepare reproducible go-264 CPU/allocation profiling binaries and evidence.
Workloads run only when both --run and GO264_PROFILE_RUN=1 are supplied.

Options:
  --run                 Run the prepared six-workload matrix.
  --output DIR          Evidence directory (default: ../../reports/go264-profile-<UTC>).
  --cpu-list LIST       taskset CPU list (default: 0,1).
  --video PATH          Diagnostic Annex B fixture.
  --m4a PATHS           Colon-separated immutable AAC/MP4 fixtures.
  --wav PATH            Immutable PCM WAV fixture.
  -h, --help            Show this help.

The caller must obtain compute admission before --run. Profile-instrumented
ns/op values are not speed evidence. Use the separate benchmem and RSS logs for
same-window comparisons, and compare only identical binaries/fixtures/commands.
EOF
}

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workspace=$(cd "$repo/../.." && pwd)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
out="$workspace/reports/go264-profile-$stamp"
cpu_list=0,1
run=0
video="$workspace/tmp/go264-fixtures/bbb_annexb.h264"
m4a="$workspace/tmp/go264-simd-codec-parity/tone.m4a:$workspace/tmp/go264-simd-codec-parity/noise.m4a:$workspace/tmp/go264-simd-codec-parity/transient.m4a"
wav="$workspace/reports/go264-aac-candidate-20260912/artifacts/tone_stereo_48000/tone_stereo_48000.src.wav"

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

mkdir -p "$out/bin"
out=$(cd "$out" && pwd)
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
  go test -run '^$' -count=0 -gcflags='github.com/rcarmo/go-264/...=-m=2' ./decode ./audio ./audio/resample
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
alloc_objects, inuse_space, inuse_objects, unprofiled benchmem, and max-RSS log.
EOF

if ((run == 0)); then
  echo "prepared profile matrix in $out"
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
  timeout --signal=KILL 12s "${prefix[@]}" "$binary" \
    -test.run='^$' -test.bench="$benchmark" -test.benchtime="$benchtime" -test.count=1 -test.timeout=10s \
    -test.cpuprofile="$out/$label.cpu.pprof" -test.memprofile="$out/$label.heap.pprof" -test.memprofilerate=524288 \
    2>&1 | tee -a "$log"
  timeout --signal=KILL 12s /usr/bin/time -v -o "$out/$label.rss.txt" \
    "${prefix[@]}" "$binary" -test.run='^$' -test.bench="$benchmark" -test.benchtime="$benchtime" -test.count=1 -test.timeout=10s -test.benchmem \
    > "$out/$label.benchmem.txt" 2>&1
  for sample in alloc_space alloc_objects inuse_space inuse_objects; do
    go tool pprof -sample_index="$sample" -top -nodecount=100 "$binary" "$out/$label.heap.pprof" > "$out/$label.$sample.txt"
  done
  go tool pprof -top -nodecount=120 "$binary" "$out/$label.cpu.pprof" > "$out/$label.cpu-top.txt"
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
find "$out" -maxdepth 1 -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > "$out/SHA256SUMS"
echo "completed profile matrix in $out"
