package aac_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/aac/internal/huffman"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type wire struct {
	b []byte
	n int
}

func (w *wire) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[w.n/8] |= byte(v>>i&1) << uint(7-w.n%8)
		w.n++
	}
}
func tnsAU(reverse bool, coef uint32) []byte {
	var w wire
	w.put(0, 3)
	w.put(0, 4)
	w.put(150, 8)
	w.put(0, 1)
	w.put(0, 2)
	w.put(0, 1)
	w.put(1, 6)
	w.put(0, 1)
	w.put(1, 4)
	w.put(1, 5)
	sf, _ := huffman.ScalefactorCodeword(0)
	w.put(sf.Code, int(sf.Bits))
	w.put(0, 1)
	w.put(1, 1)
	w.put(1, 2)
	w.put(0, 1)
	w.put(49, 6)
	w.put(1, 5)
	if reverse {
		w.put(1, 1)
	} else {
		w.put(0, 1)
	}
	w.put(0, 1)
	w.put(coef, 3)
	w.put(0, 1)
	idx, _ := huffman.EncodeSpectralIndex(1, []int16{1, 0, -1, 1})
	cw, _ := huffman.SpectralCodeword(1, idx)
	w.put(cw.Code, int(cw.Bits))
	w.put(7, 3)
	return w.b
}
func adts(packet []byte) []byte {
	n := len(packet) + 7
	b := []byte{0xff, 0xf1, 0x4c, 0x40, 0, 0x1f, 0xfc}
	b[3] |= byte(n >> 11)
	b[4] = byte(n >> 3)
	b[5] |= byte(n&7) << 5
	return append(b, packet...)
}
func TestSyntheticTNSOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline TNS PCM oracle")
	}
	for _, reverse := range []bool{false, true} {
		for _, coef := range []uint32{1, 7} {
			t.Run(fmt.Sprintf("reverse%v-coef%d", reverse, coef), func(t *testing.T) {
				p := tnsAU(reverse, coef)
				d, e := aac.NewDecoder([]byte{0x11, 0x88})
				if e != nil {
					t.Fatal(e)
				}
				var stream bytes.Buffer
				var got []float64
				for i := 0; i < 5; i++ {
					stream.Write(adts(p))
					out := make([]float64, 1024)
					if _, e = d.Decode(context.Background(), p, out); e != nil {
						t.Fatal(e)
					}
					got = append(got, out...)
				}
				dir := t.TempDir()
				input, output := filepath.Join(dir, "synthetic.aac"), filepath.Join(dir, "pcm.f32")
				if e = os.WriteFile(input, stream.Bytes(), 0600); e != nil {
					t.Fatal(e)
				}
				if b, e := exec.Command("ffmpeg", "-v", "error", "-i", input, "-f", "f32le", output).CombinedOutput(); e != nil {
					t.Fatal(e, string(b))
				}
				raw, e := os.ReadFile(output)
				if e != nil {
					t.Fatal(e)
				}
				if len(raw)/4 != len(got) {
					t.Fatal("count")
				}
				maxErr := 0.0
				for i, v := range got {
					ref := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:])))
					maxErr = math.Max(maxErr, math.Abs(v-ref))
				}
				t.Logf("TNS PCM maxabs%g", maxErr)
				if maxErr > 1e-6 {
					t.Fatal("TNS mismatch", maxErr)
				}
			})
		}
	}
}
