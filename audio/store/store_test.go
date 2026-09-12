package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func validKey(cfg string) Key {
	return Key{SourceSHA256: strings.Repeat("a", 64), DecodeConfig: cfg}
}

func validMetadata(channels int, frames int64) pcm.Metadata {
	return pcm.Metadata{
		Source: pcm.Info{SampleRate: 48000, Channels: channels, Frames: frames},
		Output: pcm.Info{SampleRate: 16000, Channels: channels, Frames: frames, BitsPerSample: 16},
	}
}

func TestCreateAppendOpenReadAt(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	meta := validMetadata(2, 4)
	st, err := Create(ctx, dir, validKey("cfg-a"), meta, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, []int16{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, []int16{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	if st.Frames() != 4 {
		t.Fatal(st.Frames())
	}
	if !reflect.DeepEqual(st.Metadata(), meta) {
		t.Fatal(st.Metadata())
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, validKey("cfg-a"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Frames() != 4 {
		t.Fatal(reopened.Frames())
	}
	if !reflect.DeepEqual(reopened.Metadata(), meta) {
		t.Fatal(reopened.Metadata())
	}
	if len(reopened.Orphans()) != 0 {
		t.Fatal(reopened.Orphans())
	}
	buf := make([]int16, 4)
	n, err := reopened.ReadAt(ctx, buf, 1)
	if n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if want := []int16{3, 4, 5, 6}; !reflect.DeepEqual(buf, want) {
		t.Fatal(buf)
	}
	buf = make([]int16, 4)
	n, err = reopened.ReadAt(ctx, buf, 3)
	if n != 2 || !errors.Is(err, io.EOF) {
		t.Fatal(n, err)
	}
	if want := []int16{7, 8}; !reflect.DeepEqual(buf[:2], want) {
		t.Fatal(buf)
	}
}

func TestOpenWrongKey(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 2), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, []int16{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir, validKey("cfg-b")); !errors.Is(err, ErrKeyMismatch) {
		t.Fatal(err)
	}
	bad := validKey("cfg-a")
	bad.SourceSHA256 = strings.Repeat("b", 64)
	if _, err := Open(ctx, dir, bad); !errors.Is(err, ErrKeyMismatch) {
		t.Fatal(err)
	}
}

func TestOpenDetectsTruncationAndCorruption(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []string{"truncate", "corrupt"} {
		t.Run(tc, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "checkpoint")
			st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 3), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, []int16{1, 2, 3}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, segmentFileName(0))
			switch tc {
			case "truncate":
				if err := os.Truncate(path, 2); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data[0] ^= 0xff
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Open(ctx, dir, validKey("cfg-a")); !errors.Is(err, pcm.ErrMalformed) {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateRejectsNonEmptyDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 1), Options{}); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatal(err)
	}
}

func TestCancelledAppendCleansTemp(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 20000), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	oldHook := afterWriteHook
	afterWriteHook = func() {
		cancel()
		afterWriteHook = nil
	}
	defer func() { afterWriteHook = oldHook }()
	if err := st.Append(ctx2, make([]int16, 20000)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if st.Frames() != 0 {
		t.Fatal(st.Frames())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != manifestName {
		t.Fatal(entries)
	}
}

func TestInterruptedAppendLeavesReusableOrphan(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 3), Options{})
	if err != nil {
		t.Fatal(err)
	}
	oldSyncDir := syncDirFunc
	fail := true
	syncDirFunc = func(path string) error {
		if fail && path == dir {
			fail = false
			return errors.New("boom")
		}
		return oldSyncDir(path)
	}
	defer func() { syncDirFunc = oldSyncDir }()
	payload := []int16{1, 2, 3}
	if err := st.Append(ctx, payload); !errors.Is(err, ErrUncertainDurability) {
		t.Fatal(err)
	}
	if st.Frames() != 0 {
		t.Fatal(st.Frames())
	}
	if err := st.Append(ctx, []int16{9}); !errors.Is(err, ErrUnusable) {
		t.Fatal(err)
	}
	if _, err := st.ReadAt(ctx, make([]int16, 1), 0); !errors.Is(err, ErrUnusable) {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, validKey("cfg-a"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Frames() != 0 {
		t.Fatal(reopened.Frames())
	}
	orphans := reopened.Orphans()
	if len(orphans) != 1 || orphans[0].Index != 0 {
		t.Fatal(orphans)
	}
	if err := reopened.Append(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if reopened.Frames() != 3 {
		t.Fatal(reopened.Frames())
	}
	if len(reopened.Orphans()) != 0 {
		t.Fatal(reopened.Orphans())
	}
	buf := make([]int16, 3)
	n, err := reopened.ReadAt(ctx, buf, 0)
	if n != 3 || err != nil || !reflect.DeepEqual(buf, payload) {
		t.Fatal(n, err, buf)
	}
}

func TestLimits(t *testing.T) {
	ctx := context.Background()
	t.Run("payload", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "checkpoint")
		st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 600000), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, make([]int16, maxSegmentPayloadByte/2+1)); !errors.Is(err, pcm.ErrLimit) {
			t.Fatal(err)
		}
	})
	t.Run("segments", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "checkpoint")
		st, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 2), Options{MaxSegments: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, []int16{1}); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, []int16{2}); !errors.Is(err, pcm.ErrLimit) {
			t.Fatal(err)
		}
		if st.Frames() != 1 {
			t.Fatal(st.Frames())
		}
	})
	t.Run("manifest", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "checkpoint")
		if _, err := Create(ctx, dir, validKey("cfg-a"), validMetadata(1, 1), Options{MaxManifestBytes: 32}); !errors.Is(err, pcm.ErrLimit) {
			t.Fatal(err)
		}
	})
	if _, err := Create(ctx, filepath.Join(t.TempDir(), "badkey"), Key{SourceSHA256: "xyz", DecodeConfig: "cfg"}, validMetadata(1, 1), Options{}); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatal(err)
	}
}

func TestManifestCommitSyncFailureReopen(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "store")
	s, e := Create(ctx, dir, validKey("v1"), validMetadata(1, 2), Options{})
	if e != nil {
		t.Fatal(e)
	}
	old := syncDirFunc
	calls := 0
	syncDirFunc = func(p string) error {
		calls++
		if calls == 2 {
			return errors.New("injected manifest sync failure")
		}
		return old(p)
	}
	defer func() { syncDirFunc = old }()
	if e = s.Append(ctx, []int16{1, 2}); !errors.Is(e, ErrUncertainDurability) {
		t.Fatal(e)
	}
	if s.Frames() != 0 {
		t.Fatal("advanced uncertain instance")
	}
	if e = s.Append(ctx, []int16{3}); !errors.Is(e, ErrUnusable) {
		t.Fatal(e)
	}
	syncDirFunc = old
	reopened, e := Open(ctx, dir, validKey("v1"))
	if e != nil || reopened.Frames() != 2 {
		t.Fatal(reopened, e)
	}
}
func TestZeroFrameCapAndOrphanConflict(t *testing.T) {
	ctx := context.Background()
	s, e := Create(ctx, t.TempDir(), validKey("v1"), validMetadata(1, 0), Options{})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Append(ctx, []int16{1}); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
	dir := t.TempDir()
	s, e = Create(ctx, dir, validKey("v1"), validMetadata(1, 2), Options{})
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, segmentFileName(0))
	if e = os.WriteFile(p, []byte{9, 0}, 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Append(ctx, []int16{1}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if !reflect.DeepEqual(b, []byte{9, 0}) {
		t.Fatal("clobbered")
	}
}
func TestManifestTrailingData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, e := Create(ctx, dir, validKey("v1"), validMetadata(1, 2), Options{})
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, manifestName)
	b, _ := os.ReadFile(p)
	for _, suffix := range []string{" null", " }", " {}"} {
		if e = os.WriteFile(p, append(append([]byte{}, b...), []byte(suffix)...), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = Open(ctx, dir, validKey("v1")); !errors.Is(e, pcm.ErrMalformed) {
			t.Fatal(suffix, e)
		}
	}
}
