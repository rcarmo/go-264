package audio_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/rcarmo/go-264/audio"
	"github.com/rcarmo/go-264/audio/store"
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCheckpointResumeWAV(t *testing.T) {
	ctx := context.Background()
	samples := make([]int16, 12000)
	for i := range samples {
		samples[i] = int16(i * 79)
	}
	data := fixture(2, 48000, samples)
	sum := sha256.Sum256(data)
	key := store.Key{SourceSHA256: hex.EncodeToString(sum[:]), DecodeConfig: "go264-test-v1:16000:mono:s16"}
	open := func() *audio.Decoder {
		d, e := audio.Open(ctx, bytes.NewReader(data), int64(len(data)), audio.Options{})
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	d := open()
	meta := d.Metadata()
	dir := filepath.Join(t.TempDir(), "checkpoint")
	s, e := store.Create(ctx, dir, key, meta, store.Options{})
	if e != nil {
		t.Fatal(e)
	}
	part := make([]int16, 317)
	n, _, e := d.ReadPCM(ctx, part)
	if e != nil || n != len(part) {
		t.Fatal(n, e)
	}
	if e = s.Append(ctx, part); e != nil {
		t.Fatal(e)
	}
	d.Close()
	s.Close()
	s, e = store.Open(ctx, dir, key)
	if e != nil {
		t.Fatal(e)
	}
	d = open()
	defer d.Close()
	if e = d.Seek(ctx, s.Frames()); e != nil {
		t.Fatal(e)
	}
	buf := make([]int16, 257)
	for {
		n, _, e := d.ReadPCM(ctx, buf)
		if e != nil && e != io.EOF {
			t.Fatal(e)
		}
		if n > 0 {
			if e = s.Append(ctx, buf[:n]); e != nil {
				t.Fatal(e)
			}
		}
		if n < len(buf) {
			break
		}
	}
	got := make([]int16, s.Frames())
	if n, e = s.ReadAt(ctx, got, 0); e != nil || n != len(got) {
		t.Fatal(n, e)
	}
	fresh := open()
	defer fresh.Close()
	want := make([]int16, meta.Output.Frames)
	n, _, e = fresh.ReadPCM(ctx, want)
	if e != nil || n != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatal("resume parity", n, e)
	}
}
