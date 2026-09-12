package mp4

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"testing"
)

func box(kind string, payload []byte, large bool) []byte {
	head := 8
	if large {
		head = 16
	}
	b := make([]byte, head+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	copy(b[4:8], kind)
	if large {
		binary.BigEndian.PutUint32(b, 1)
		binary.BigEndian.PutUint64(b[8:], uint64(len(b)))
	}
	copy(b[head:], payload)
	return b
}
func TestWalkBounds(t *testing.T) {
	for _, large := range []bool{false, true} {
		moov := box("moov", box("trak", box("mdia", box("minf", box("stbl", box("stco", []byte{0, 0, 0, 0, 0, 0, 0, 0}, large), large), large), large), large), large)
		mdat := box("mdat", make([]byte, 23), large)
		for _, tail := range []bool{false, true} {
			data := append(append([]byte{}, moov...), mdat...)
			if tail {
				data = append(append([]byte{}, mdat...), moov...)
			}
			var got []Box
			e := Walk(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}, func(b Box) error { got = append(got, b); return nil })
			if e != nil || len(got) != 7 {
				t.Fatal(e, len(got))
			}
			for _, b := range got {
				if b.PayloadOffset()+b.PayloadSize() > int64(len(data)) {
					t.Fatal(b)
				}
			}
		}
	}
}
func TestReject(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"moof", "mvex", "traf"} {
		data := box(kind, nil, false)
		if e := Walk(ctx, bytes.NewReader(data), int64(len(data)), Limits{}, func(Box) error { return nil }); !errors.Is(e, pcm.ErrUnsupported) {
			t.Fatal(e)
		}
	}
	for _, data := range [][]byte{nil, {0, 0, 0, 4, 'f', 'r', 'e', 'e'}, box("moov", []byte{1}, false), {0, 0, 0, 1, 'm', 'd', 'a', 't', 255, 255, 255, 255, 255, 255, 255, 255}} {
		if e := Walk(ctx, bytes.NewReader(data), int64(len(data)), Limits{}, func(Box) error { return nil }); e == nil {
			t.Fatal("accepted", data)
		}
	}
	data := box("moov", box("trak", box("mdia", nil, false), false), false)
	for _, lim := range []Limits{{MaxDepth: 1}, {MaxBoxes: 1}, {MaxBytes: 8}, {MaxDepth: 65}} {
		if e := Walk(ctx, bytes.NewReader(data), int64(len(data)), lim, func(Box) error { return nil }); !errors.Is(e, pcm.ErrLimit) {
			t.Fatal(lim, e)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if e := Walk(ctx, bytes.NewReader(data), int64(len(data)), Limits{}, func(Box) error { return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestZeroSizeAndCallback(t *testing.T) {
	data := box("mdat", make([]byte, 32), false)
	binary.BigEndian.PutUint32(data, 0)
	stop := errors.New("stop")
	if e := Walk(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}, func(b Box) error {
		if b.Size != int64(len(data)) {
			t.Fatal(b)
		}
		return stop
	}); e != stop {
		t.Fatal(e)
	}
}
func FuzzBoxes(f *testing.F) {
	f.Add(box("moov", box("trak", nil, false), false))
	f.Add(box("mdat", make([]byte, 16), true))
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = Walk(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxBytes: 1 << 20, MaxBoxes: 128, MaxDepth: 8}, func(b Box) error {
			if b.Offset < 0 || b.Size < b.HeaderSize || b.Offset+b.Size > int64(len(data)) {
				t.Fatal(b)
			}
			return nil
		})
	})
}
