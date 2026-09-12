package aac

import (
	"context"
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"reflect"
	"testing"
)

type testBits struct {
	b []byte
	n int
}

func (w *testBits) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[w.n/8] |= byte(v>>i&1) << uint(7-w.n%8)
		w.n++
	}
}
func zeroPacket() []byte {
	var w testBits
	w.put(0, 3)
	w.put(0, 4)
	w.put(100, 8)
	w.put(0, 1)
	w.put(0, 2)
	w.put(0, 1)
	w.put(0, 6)
	w.put(0, 1)
	w.put(0, 3)
	w.put(7, 3)
	return w.b
}
func TestDecodeTransaction(t *testing.T) {
	ctx := context.Background()
	d, e := NewDecoder([]byte{0x11, 0x88})
	if e != nil {
		t.Fatal(e)
	}
	dst := make([]float64, 1027)
	dst[1024] = 7
	n, e := d.Decode(ctx, zeroPacket(), dst)
	if n != 1024 || e != nil || dst[1024] != 7 {
		t.Fatal(n, e)
	}
	before := *d
	for i := range dst {
		dst[i] = 9
	}
	for _, b := range [][]byte{nil, {0xff}, {0x40}, {0xa0}, {0x60}} {
		if n, e = d.Decode(ctx, b, dst); n != 0 || e == nil {
			t.Fatal(n, e)
		}
		if !reflect.DeepEqual(*d, before) {
			t.Fatal("state mutated")
		}
		for _, v := range dst {
			if v != 9 {
				t.Fatal("output mutated")
			}
		}
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if n, e = d.Decode(c, zeroPacket(), dst); n != 0 || !errors.Is(e, context.Canceled) {
		t.Fatal(n, e)
	}
	if n, e = d.Decode(ctx, zeroPacket(), dst[:2]); n != 0 || !errors.Is(e, pcm.ErrMalformed) {
		t.Fatal(n, e)
	}
	d.Reset()
	fresh, _ := NewDecoder([]byte{0x11, 0x88})
	if !reflect.DeepEqual(*d, *fresh) {
		t.Fatal("reset")
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(zeroPacket())
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			return
		}
		d, _ := NewDecoder([]byte{0x11, 0x88})
		dst := make([]float64, 1024)
		_, _ = d.Decode(context.Background(), b, dst)
	})
}
