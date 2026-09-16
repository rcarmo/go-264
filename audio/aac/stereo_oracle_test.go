package aac_test

import (
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

func stereoToolAU(mode, book int) []byte {
	var w wire
	w.put(1, 3)
	w.put(0, 4)
	w.put(1, 1)
	w.put(0, 1)
	w.put(0, 2)
	w.put(0, 1)
	w.put(1, 6)
	w.put(0, 1)
	w.put(uint32(mode), 2)
	if mode == 1 {
		w.put(1, 1)
	}
	for c := 0; c < 2; c++ {
		cb := 1
		if book == 13 {
			cb = 13
		} else if c == 1 {
			cb = book
		}
		w.put(100, 8)
		w.put(uint32(cb), 4)
		w.put(1, 5)
		if cb == 13 {
			w.put(300, 9)
		} else {
			delta := int8(0)
			if c == 1 {
				delta = 4
			}
			sf, _ := huffman.ScalefactorCodeword(delta)
			w.put(sf.Code, int(sf.Bits))
		}
		w.put(0, 3)
		if cb == 1 {
			idx, _ := huffman.EncodeSpectralIndex(1, []int16{1, 0, -1, 1})
			cw, _ := huffman.SpectralCodeword(1, idx)
			w.put(cw.Code, int(cw.Bits))
		}
	}
	w.put(7, 3)
	return w.b
}
func TestStereoToolsOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline intensity/PNS oracle")
	}
	for _, book := range []int{13, 14, 15} {
		for _, mode := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("book%d-mode%d", book, mode), func(t *testing.T) {
				packet := stereoToolAU(mode, book)
				d, e := aac.NewDecoder([]byte{0x11, 0x90})
				if e != nil {
					t.Fatal(e)
				}
				var stream []byte
				var got []float64
				for i := 0; i < 5; i++ {
					header := adts(packet)
					header[3] = (header[3] & 0x3f) | 0x80
					stream = append(stream, header...)
					out := make([]float64, 2048)
					if _, e = d.Decode(context.Background(), packet, out); e != nil {
						t.Fatal(e)
					}
					got = append(got, out...)
				}
				dir := t.TempDir()
				p, out := filepath.Join(dir, "stereo.aac"), filepath.Join(dir, "pcm.f32")
				if e = os.WriteFile(p, stream, 0600); e != nil {
					t.Fatal(e)
				}
				if b, e := exec.Command("ffmpeg", "-v", "error", "-i", p, "-f", "f32le", out).CombinedOutput(); e != nil {
					t.Fatal(e, string(b))
				}
				raw, e := os.ReadFile(out)
				if e != nil {
					t.Fatal(e)
				}
				if len(raw) != len(got)*4 {
					t.Fatal("count")
				}
				maxErr := 0.0
				var perFrame [5]float64
				for i, v := range got {
					want := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:])))
					maxErr = math.Max(maxErr, math.Abs(v-want))
					perFrame[i/2048] = math.Max(perFrame[i/2048], math.Abs(v-want))
				}
				var lr, glr, maxL, maxR float64
				for i := 0; i < len(got); i += 2 {
					l := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:])))
					r := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*(i+1):])))
					lr = math.Max(lr, math.Abs(l-r))
					glr = math.Max(glr, math.Abs(got[i]-got[i+1]))
					maxL = math.Max(maxL, math.Abs(l-got[i]))
					maxR = math.Max(maxR, math.Abs(r-got[i+1]))
				}
				t.Logf("maxabs%g perFrame%v Lerr%g Rerr%g refLR%g gotLR%g", maxErr, perFrame, maxL, maxR, lr, glr)
				if maxErr > 1e-6 {
					t.Fatal("stereo tool mismatch", maxErr)
				}
			})
		}
	}
}

func TestCorrelatedPNSDeterministicStateAndReset(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []int{1, 2} {
		t.Run(fmt.Sprintf("mode%d", mode), func(t *testing.T) {
			packet := stereoToolAU(mode, 13)
			first, err := aac.NewDecoder([]byte{0x11, 0x90})
			if err != nil {
				t.Fatal(err)
			}
			second, err := aac.NewDecoder([]byte{0x11, 0x90})
			if err != nil {
				t.Fatal(err)
			}
			var firstFrames [2][2048]float64
			var secondFrames [2][2048]float64
			for frame := range firstFrames {
				if n, err := first.Decode(ctx, packet, firstFrames[frame][:]); n != 1024 || err != nil {
					t.Fatalf("first frame %d: n=%d err=%v", frame, n, err)
				}
				if n, err := second.Decode(ctx, packet, secondFrames[frame][:]); n != 1024 || err != nil {
					t.Fatalf("second frame %d: n=%d err=%v", frame, n, err)
				}
				for i := range firstFrames[frame] {
					if math.Float64bits(firstFrames[frame][i]) != math.Float64bits(secondFrames[frame][i]) {
						t.Fatalf("frame %d sample %d differs", frame, i)
					}
				}
			}
			if math.Float64bits(firstFrames[0][0]) == math.Float64bits(firstFrames[1][0]) {
				t.Fatal("PRNG/filterbank state did not advance")
			}
			first.Reset()
			var reset [2048]float64
			if n, err := first.Decode(ctx, packet, reset[:]); n != 1024 || err != nil {
				t.Fatalf("reset decode: n=%d err=%v", n, err)
			}
			for i := range reset {
				if math.Float64bits(reset[i]) != math.Float64bits(firstFrames[0][i]) {
					t.Fatalf("reset sample %d differs", i)
				}
			}
		})
	}
}
