package lc

import (
	"errors"
	"testing"

	"github.com/rcarmo/go-264/audio/aac/internal/huffman"
	"github.com/rcarmo/go-264/audio/pcm"
)

type bitWriter struct {
	buf []byte
	cur byte
	n   int
}

func (w *bitWriter) bits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		w.cur = (w.cur << 1) | byte((v>>i)&1)
		w.n++
		if w.n == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur = 0
			w.n = 0
		}
	}
}

func (w *bitWriter) bit(v bool) { w.bits(b2u(v), 1) }

func (w *bitWriter) bytes(vs ...byte) {
	for _, v := range vs {
		w.bits(uint32(v), 8)
	}
}

func (w *bitWriter) finish() []byte {
	out := append([]byte(nil), w.buf...)
	if w.n != 0 {
		out = append(out, w.cur<<uint(8-w.n))
	}
	return out
}

func (w *bitWriter) bitsUsed() int { return len(w.buf)*8 + w.n }

func b2u(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

func writeICSInfoLong(w *bitWriter, shape int, maxSFB int) {
	w.bit(false)
	w.bits(0, 2)
	w.bits(uint32(shape), 1)
	w.bits(uint32(maxSFB), 6)
	w.bit(false)
}

func writeICSInfoShort(w *bitWriter, shape int, maxSFB int, groups []int) {
	w.bit(false)
	w.bits(2, 2)
	w.bits(uint32(shape), 1)
	w.bits(uint32(maxSFB), 4)
	w.bits(uint32(groupingMask(groups)), 7)
}

func groupingMask(groups []int) int {
	if len(groups) == 0 {
		return 0
	}
	mask := 0
	win := 0
	for gi, gl := range groups {
		for i := 0; i < gl; i++ {
			if gi == 0 && i == 0 {
				win++
				continue
			}
			if i > 0 {
				mask |= 1 << (7 - win)
			}
			win++
		}
	}
	return mask
}

func writeZeroSectionsLong(w *bitWriter, maxSFB int) {
	w.bits(0, 4)
	w.bits(uint32(maxSFB), 5)
}

func writeZeroSectionsShort(w *bitWriter, groups int, maxSFB int) {
	for i := 0; i < groups; i++ {
		w.bits(0, 4)
		w.bits(uint32(maxSFB), 3)
	}
}

func writeCommonBodyZero(w *bitWriter) {
	w.bits(100, 8)
	writeZeroSectionsLong(w, 1)
	w.bit(false)
	w.bit(false)
	w.bit(false)
}

func writeScalefactor(w *bitWriter, d int8) {
	cw, err := huffman.ScalefactorCodeword(d)
	if err != nil {
		panic(err)
	}
	w.bits(cw.Code, int(cw.Bits))
}

func writeSpectralTuple(w *bitWriter, book int, vals []int16) {
	idx, err := huffman.EncodeSpectralIndex(book, vals)
	if err != nil {
		panic(err)
	}
	cw, err := huffman.SpectralCodeword(book, idx)
	if err != nil {
		panic(err)
	}
	w.bits(cw.Code, int(cw.Bits))
	signs, err := huffman.DeriveSignBits(book, vals)
	if err != nil {
		panic(err)
	}
	for _, s := range signs {
		w.bit(s)
	}
}

func monoZeroLongPayload() []byte {
	var w bitWriter
	w.bits(0, 3)
	w.bits(0, 4)
	w.bits(100, 8)
	writeICSInfoLong(&w, 0, 1)
	writeZeroSectionsLong(&w, 1)
	w.bit(false)
	w.bit(false)
	w.bit(false)
	w.bits(7, 3)
	return w.finish()
}

func stereoCommonZeroLongPayload() []byte {
	var w bitWriter
	w.bits(1, 3)
	w.bits(0, 4)
	w.bit(true)
	writeICSInfoLong(&w, 0, 1)
	w.bits(0, 2)
	writeCommonBodyZero(&w)
	writeCommonBodyZero(&w)
	w.bits(7, 3)
	return w.finish()
}

func TestParseMonoZeroSpectrumLong(t *testing.T) {
	f, err := Parse(monoZeroLongPayload(), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.Count != 1 || f.CommonWindow {
		t.Fatalf("Count/CommonWindow = %d/%v", f.Count, f.CommonWindow)
	}
	ch := f.Channels[0]
	if ch.Sequence != SequenceOnlyLong || ch.Shape != ShapeSine || ch.MaxSFB != 1 || ch.NumGroups != 1 {
		t.Fatalf("bad channel header: %+v", ch)
	}
	if ch.GroupLength[0] != 1 {
		t.Fatalf("GroupLength[0]=%d", ch.GroupLength[0])
	}
	if got, want := len(ch.Offsets), 50; got != want {
		t.Fatalf("len(Offsets)=%d want %d", got, want)
	}
	if ch.Offsets[1] != 4 || ch.Codebook[0][0] != 0 {
		t.Fatalf("offset/codebook mismatch")
	}
}

func TestParseStereoSharedWindowZeroSpectrum(t *testing.T) {
	f, err := Parse(stereoCommonZeroLongPayload(), 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if f.Count != 2 || !f.CommonWindow {
		t.Fatalf("Count/CommonWindow = %d/%v", f.Count, f.CommonWindow)
	}
	if f.MS[0][0] {
		t.Fatalf("unexpected ms flag")
	}
	for i := 0; i < 2; i++ {
		if f.Channels[i].Sequence != SequenceOnlyLong || f.Channels[i].MaxSFB != 1 {
			t.Fatalf("channel %d bad header", i)
		}
	}
}

func TestParseGroupingAndMSMask(t *testing.T) {
	var w bitWriter
	groups := []int{2, 1, 2, 3}
	w.bits(1, 3)
	w.bits(0, 4)
	w.bit(true)
	writeICSInfoShort(&w, 1, 2, groups)
	w.bits(1, 2)
	ms := []bool{true, false, false, true, true, true, false, false}
	for _, b := range ms {
		w.bit(b)
	}
	for i := 0; i < 2; i++ {
		w.bits(100, 8)
		writeZeroSectionsShort(&w, len(groups), 2)
		w.bit(false)
		w.bit(false)
		w.bit(false)
	}
	w.bits(7, 3)
	f, err := Parse(w.finish(), 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	ch := f.Channels[0]
	if ch.Sequence != SequenceEightShort || ch.Shape != ShapeKBD || ch.NumGroups != len(groups) {
		t.Fatalf("short geometry mismatch")
	}
	for i, want := range groups {
		if ch.GroupLength[i] != want {
			t.Fatalf("GroupLength[%d]=%d want %d", i, ch.GroupLength[i], want)
		}
	}
	k := 0
	for g := range groups {
		for sfb := 0; sfb < 2; sfb++ {
			if f.MS[g][sfb] != ms[k] {
				t.Fatalf("MS[%d][%d]=%v want %v", g, sfb, f.MS[g][sfb], ms[k])
			}
			k++
		}
	}
}

func TestParseShortGroupedSpectralDeinterleave(t *testing.T) {
	var w bitWriter
	groups := []int{2, 6}
	w.bits(0, 3)
	w.bits(0, 4)
	w.bits(120, 8)
	writeICSInfoShort(&w, 0, 1, groups)
	for range groups {
		w.bits(1, 4)
		w.bits(1, 3)
	}
	writeScalefactor(&w, 0)
	writeScalefactor(&w, 0)
	w.bit(false)
	w.bit(false)
	w.bit(false)
	writeSpectralTuple(&w, 1, []int16{1, 0, 0, 0})
	writeSpectralTuple(&w, 1, []int16{0, 1, 0, 0})
	for i := 0; i < 6; i++ {
		writeSpectralTuple(&w, 1, []int16{0, 0, 0, 0})
	}
	w.bits(7, 3)
	f, err := Parse(w.finish(), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.Channels[0].Quant[0] != 1 || f.Channels[0].Quant[1*128+1] != 1 {
		t.Fatalf("deinterleave failed: q0=%d q129=%d", f.Channels[0].Quant[0], f.Channels[0].Quant[129])
	}
}

func TestParseMalformedSectionOverrun(t *testing.T) {
	var w bitWriter
	w.bits(0, 3)
	w.bits(0, 4)
	w.bits(100, 8)
	writeICSInfoLong(&w, 0, 1)
	w.bits(0, 4)
	w.bits(2, 5)
	w.bit(false)
	w.bit(false)
	w.bit(false)
	w.bits(7, 3)
	_, err := Parse(w.finish(), 48000, 1)
	if !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("error=%v", err)
	}
}

func TestParseMalformedPadding(t *testing.T) {
	p := monoZeroLongPayload()
	p[len(p)-1] |= 0x01
	_, err := Parse(p, 48000, 1)
	if !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("error=%v", err)
	}
}

func TestParseUnsupportedFILSBR(t *testing.T) {
	var w bitWriter
	w.bits(6, 3)
	w.bits(1, 4)
	w.bits(13, 4)
	w.bits(0, 4)
	w.bytes(monoZeroLongPayload()...)
	_, err := Parse(w.finish(), 48000, 1)
	if !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatalf("error=%v", err)
	}
}

func TestParseDSEMetadata(t *testing.T) {
	var w bitWriter
	w.bits(4, 3)
	w.bits(3, 4)
	w.bit(false)
	w.bits(2, 8)
	w.bytes(0xaa, 0xbb)
	w.bytes(monoZeroLongPayload()...)
	f, err := Parse(w.finish(), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.DataElements) != 1 || f.DataElements[0].Tag != 3 || f.DataElements[0].PayloadBytes != 2 {
		t.Fatalf("bad DSE metadata: %+v", f.DataElements)
	}
}

func TestParseLimit(t *testing.T) {
	_, err := Parse(make([]byte, maxAccessUnitBytes+1), 48000, 1)
	if !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("error=%v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(monoZeroLongPayload(), 48000, 1)
	f.Add(stereoCommonZeroLongPayload(), 48000, 2)
	var w bitWriter
	w.bits(6, 3)
	w.bits(0, 4)
	w.bytes(monoZeroLongPayload()...)
	f.Add(w.finish(), 48000, 1)
	f.Fuzz(func(t *testing.T, data []byte, rate int, ch int) {
		if len(data) > maxAccessUnitBytes+16 {
			return
		}
		_, _ = Parse(data, rate, ch)
	})
}
