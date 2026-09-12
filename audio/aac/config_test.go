package aac

import (
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"testing"
)

func TestConfig(t *testing.T) {
	for _, c := range []struct {
		b        []byte
		rate, ch int
	}{{[]byte{0x12, 0x10}, 44100, 2}, {[]byte{0x11, 0x88}, 48000, 1}, {[]byte{0x12, 0x10, 0}, 44100, 2}} {
		v, e := ParseConfig(c.b)
		if e != nil || v.SampleRate != c.rate || v.Channels != c.ch || v.FrameSamples != 1024 {
			t.Fatal(v, e)
		}
	}
}
func TestConfigRejects(t *testing.T) {
	for _, b := range [][]byte{{}, {0x12}, {0x2b, 0x92, 0x08}, {0x12, 0x14}, {0x12, 0}, {0x12, 0x10, 0x56, 0xe5}, {0x17, 0x90}} {
		if _, e := ParseConfig(b); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
	if _, e := ParseConfig(make([]byte, 65)); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
}
func FuzzConfig(f *testing.F) {
	f.Add([]byte{0x12, 0x10})
	f.Add([]byte{0x2b, 0x92, 0x08})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, e := ParseConfig(b)
		if e == nil && (c.FrameSamples != 1024 || c.Channels < 1 || c.Channels > 2) {
			t.Fatal(c)
		}
	})
}

func TestSyncExtension(t *testing.T) {
	if c, e := ParseConfig([]byte{0x12, 0x10, 0x56, 0xe5, 0}); e != nil || c.SampleRate != 44100 {
		t.Fatal(c, e)
	}
	for _, b := range [][]byte{{0x12, 0x10, 0x56, 0xe5, 0x80}, {0x12, 0x10, 0x56, 0xe5}, {0x12, 0x10, 0x56, 0xe6, 0}, {0x12, 0x10, 0x56, 0xe5, 0, 1}} {
		if _, e := ParseConfig(b); e == nil {
			t.Fatalf("accepted%x", b)
		}
	}
}
