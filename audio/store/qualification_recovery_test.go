package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	qualificationCrashHelperEnv = "GO264_STORE_QUALIFICATION_CRASH_HELPER"
	qualificationCrashStageEnv  = "GO264_STORE_QUALIFICATION_CRASH_STAGE"
	qualificationCrashDirEnv    = "GO264_STORE_QUALIFICATION_CRASH_DIR"
)

func TestQualificationInterruptedAppendPreservesCommittedSegments(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 4), Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := []int16{11, 12}
	second := []int16{21, 22}
	if err := st.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	oldSyncDir := syncDirFunc
	calls := 0
	syncDirFunc = func(path string) error {
		if path == dir {
			calls++
			if calls == 1 {
				return errors.New("inject segment publication interruption")
			}
		}
		return oldSyncDir(path)
	}
	defer func() { syncDirFunc = oldSyncDir }()
	if err := st.Append(ctx, second); !errors.Is(err, ErrUncertainDurability) {
		t.Fatalf("Append(interrupted) error = %v, want ErrUncertainDurability", err)
	}
	if st.Frames() != int64(len(first)) {
		t.Fatalf("Frames() after interrupted append = %d, want %d", st.Frames(), len(first))
	}
	if err := st.Append(ctx, []int16{99}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("Append(unusable) error = %v, want ErrUnusable", err)
	}

	syncDirFunc = oldSyncDir
	reopened, err := Open(ctx, dir, validKey("cfg-a"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Frames() != int64(len(first)) {
		t.Fatalf("reopened Frames() = %d, want %d", reopened.Frames(), len(first))
	}
	orphans := reopened.Orphans()
	if len(orphans) != 1 || orphans[0].Index != 1 {
		t.Fatalf("reopened Orphans() = %v, want one orphan at index 1", orphans)
	}
	buf := make([]int16, len(first))
	n, err := reopened.ReadAt(ctx, buf, 0)
	if n != len(first) || err != nil || !reflect.DeepEqual(buf, first) {
		t.Fatalf("ReadAt(committed) = (%d, %v, %v), want (%d, nil, %v)", n, err, buf, len(first), first)
	}
	if err := reopened.Append(ctx, second); err != nil {
		t.Fatal(err)
	}
	all := make([]int16, len(first)+len(second))
	n, err = reopened.ReadAt(ctx, all, 0)
	if n != len(all) || err != nil || !reflect.DeepEqual(all, append(append([]int16(nil), first...), second...)) {
		t.Fatalf("ReadAt(full) = (%d, %v, %v)", n, err, all)
	}

	segment0 := filepath.Join(dir, segmentFileName(0))
	data, err := os.ReadFile(segment0)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 0xff
	if err := os.WriteFile(segment0, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir, validKey("cfg-a")); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("Open(corrupted committed segment) error = %v, want ErrMalformed", err)
	}
}

func TestQualificationManifestCorruptedCountsBounded(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		mutate func(*diskManifest)
		want   error
	}{
		{
			name: "max_segments_below_manifest_count",
			mutate: func(m *diskManifest) {
				m.Options.MaxSegments = 1
			},
			want: pcm.ErrLimit,
		},
		{
			name: "manifest_byte_cap_below_actual_length",
			mutate: func(m *diskManifest) {
				m.Options.MaxManifestBytes = 1
			},
			want: pcm.ErrLimit,
		},
		{
			name: "declared_output_frames_below_committed_frames",
			mutate: func(m *diskManifest) {
				m.Metadata.Output.Frames = 1
			},
			want: pcm.ErrMalformed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "checkpoint")
			st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 2), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, []int16{1}); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, []int16{2}); err != nil {
				t.Fatal(err)
			}
			m, _, err := readManifestFile(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(&m)
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, manifestName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, segmentFileName(0))); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(ctx, dir, validKey("cfg-a")); !errors.Is(err, tc.want) {
				t.Fatalf("Open() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQualificationProcessCrashRecoveryStages(t *testing.T) {
	ctx := context.Background()
	payload := []int16{101, 202}
	tests := []struct {
		name        string
		stage       string
		wantFrames  int64
		wantOrphans int
	}{
		{name: "segment_published_before_crash", stage: "segment", wantFrames: 0, wantOrphans: 1},
		{name: "manifest_published_before_crash", stage: "manifest", wantFrames: 2, wantOrphans: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "checkpoint")
			runQualificationCrashHelper(t, tc.stage, dir)
			reopened, err := Open(ctx, dir, validKey("cfg-a"))
			if err != nil {
				t.Fatal(err)
			}
			if reopened.Frames() != tc.wantFrames {
				t.Fatalf("reopened Frames() = %d, want %d", reopened.Frames(), tc.wantFrames)
			}
			if got := len(reopened.Orphans()); got != tc.wantOrphans {
				t.Fatalf("len(reopened.Orphans()) = %d, want %d", got, tc.wantOrphans)
			}
			if tc.stage == "segment" {
				if reopened.Orphans()[0].Index != 0 {
					t.Fatalf("orphan index = %d, want 0", reopened.Orphans()[0].Index)
				}
				if err := reopened.Append(ctx, payload); err != nil {
					t.Fatal(err)
				}
			}
			buf := make([]int16, len(payload))
			n, err := reopened.ReadAt(ctx, buf, 0)
			if n != len(payload) || err != nil || !reflect.DeepEqual(buf, payload) {
				t.Fatalf("ReadAt() = (%d, %v, %v), want (%d, nil, %v)", n, err, buf, len(payload), payload)
			}
		})
	}
}

func TestQualificationStoreCrashHelper(t *testing.T) {
	if os.Getenv(qualificationCrashHelperEnv) != "1" {
		return
	}

	ctx := context.Background()
	dir := os.Getenv(qualificationCrashDirEnv)
	stage := os.Getenv(qualificationCrashStageEnv)
	payload := []int16{101, 202}
	st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, int64(len(payload))), Options{})
	if err != nil {
		t.Fatal(err)
	}
	oldSyncDir := syncDirFunc
	calls := 0
	syncDirFunc = func(path string) error {
		if path == dir {
			calls++
			if (stage == "segment" && calls == 1) || (stage == "manifest" && calls == 2) {
				if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			}
		}
		return oldSyncDir(path)
	}
	defer func() { syncDirFunc = oldSyncDir }()
	if err := st.Append(ctx, payload); err == nil {
		t.Fatalf("Append() returned unexpectedly in %s crash mode", stage)
	}
}

func runQualificationCrashHelper(t *testing.T, stage, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestQualificationStoreCrashHelper$")
	cmd.Env = append(os.Environ(),
		qualificationCrashHelperEnv+"=1",
		qualificationCrashStageEnv+"="+stage,
		qualificationCrashDirEnv+"="+dir,
	)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("helper for %s crash completed without crashing", stage)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper for %s crash error = %v, want ExitError", stage, err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("helper for %s crash status = %v, want SIGKILL", stage, exitErr.Sys())
	}
}
