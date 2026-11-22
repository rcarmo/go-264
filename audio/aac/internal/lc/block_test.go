package lc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func writeZeroSingle(w *bitWriter, id, tag int) {
	w.bits(uint32(id), 3)
	w.bits(uint32(tag), 4)
	w.bits(100, 8)
	writeICSInfoLong(w, 0, 1)
	writeZeroSectionsLong(w, 1)
	w.bit(false)
	w.bit(false)
	w.bit(false)
}

func writeZeroPair(w *bitWriter, tag, msMode int) {
	w.bits(1, 3)
	w.bits(uint32(tag), 4)
	w.bit(true)
	writeICSInfoLong(w, 0, 1)
	w.bits(uint32(msMode), 2)
	if msMode == 1 {
		w.bit(true)
	}
	writeCommonBodyZero(w)
	writeCommonBodyZero(w)
}

func writeZeroElements(w *bitWriter, elements []ElementType) {
	tags := map[ElementType]int{}
	for _, e := range elements {
		switch e {
		case ElementCPE:
			writeZeroPair(w, tags[e], 0)
		default:
			writeZeroSingle(w, int(e), tags[e])
		}
		tags[e]++
	}
}

func zeroBlockPayload(elements ...ElementType) []byte {
	var w bitWriter
	writeZeroElements(&w, elements)
	w.bits(7, 3)
	return w.finish()
}

func parseAllElements(data []byte, config int) ([]ElementType, int, error) {
	b, err := NewBlock(data, 48000, config)
	if err != nil {
		return nil, 0, err
	}
	var got []ElementType
	channels := 0
	var f Frame
	for b.Remaining() > 0 {
		if err := b.Next(&f); err != nil {
			return got, channels, err
		}
		got = append(got, f.Element)
		channels += f.Count
	}
	return got, channels, b.Finish()
}

func TestBlockChannelConfigurations(t *testing.T) {
	wantChannels := [...]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 8}
	for config := 1; config <= 7; config++ {
		t.Run(fmt.Sprint(config), func(t *testing.T) {
			elements := Elements(config)
			got, channels, err := parseAllElements(zeroBlockPayload(elements...), config)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(elements) || channels != wantChannels[config] {
				t.Fatalf("elements=%v channels=%d want %v/%d", got, channels, elements, wantChannels[config])
			}
		})
	}
	for _, config := range []int{-1, 0, 8, 15} {
		if Elements(config) != nil {
			t.Fatalf("configuration %d has elements", config)
		}
		if _, err := NewBlock(zeroBlockPayload(ElementSCE), 48000, config); !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("configuration %d err=%v", config, err)
		}
	}
}

func TestBlockRejectsLayoutMismatch(t *testing.T) {
	sce, cpe, lfe := ElementSCE, ElementCPE, ElementLFE
	for _, tc := range []struct {
		name     string
		config   int
		elements []ElementType
	}{
		{"missing-surround", 5, []ElementType{sce, cpe}},
		{"missing-lfe", 6, []ElementType{sce, cpe, cpe}},
		{"lfe-before-surround", 6, []ElementType{sce, cpe, lfe, cpe}},
		{"pair-before-centre", 3, []ElementType{cpe, sce}},
		{"extra-sce", 3, []ElementType{sce, cpe, sce}},
		{"extra-lfe", 5, []ElementType{sce, cpe, cpe, lfe}},
		{"lfe-in-stereo", 2, []ElementType{cpe, lfe}},
		// Formerly unsupported ("AAC-LC LFE"); now a layout mismatch.
		{"lfe-for-mono", 1, []ElementType{lfe}},
		{"lfe-before-stereo", 2, []ElementType{lfe, cpe}},
		{"lfe-for-centre", 4, []ElementType{sce, cpe, lfe}},
		{"seven-one-as-five-one", 6, []ElementType{sce, cpe, cpe, cpe, lfe}},
		{"empty", 7, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseAllElements(zeroBlockPayload(tc.elements...), tc.config); !errors.Is(err, pcm.ErrMalformed) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestBlockRejectsUnsupportedElements(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   int
	}{{"cce", 2}, {"pce", 5}} {
		t.Run(tc.name, func(t *testing.T) {
			var w bitWriter
			writeZeroElements(&w, []ElementType{ElementSCE, ElementCPE})
			w.bits(uint32(tc.id), 3)
			w.bits(0, 13)
			w.bits(7, 3)
			if _, _, err := parseAllElements(w.finish(), 3); !errors.Is(err, pcm.ErrUnsupported) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	// LFE uses SCE syntax but ISO/IEC 14496-3 restricts it to long windows.
	var w bitWriter
	writeZeroElements(&w, []ElementType{ElementSCE, ElementCPE, ElementCPE})
	w.bits(3, 3)
	w.bits(0, 4)
	w.bits(100, 8)
	writeICSInfoShort(&w, 0, 1, []int{8})
	writeZeroSectionsShort(&w, 1, 1)
	w.bit(false)
	w.bit(false)
	w.bit(false)
	w.bits(7, 3)
	if _, _, err := parseAllElements(w.finish(), 6); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("short LFE err=%v", err)
	}
}

func TestBlockMetadataBetweenElements(t *testing.T) {
	var w bitWriter
	writeZeroSingle(&w, 0, 0)
	w.bits(4, 3) // DSE
	w.bits(1, 4)
	w.bit(false)
	w.bits(1, 8)
	w.bytes(0x55)
	writeZeroPair(&w, 0, 2)
	w.bits(6, 3) // empty FIL
	w.bits(0, 4)
	writeZeroPair(&w, 1, 0)
	writeZeroSingle(&w, 3, 0)
	w.bits(6, 3)
	w.bits(0, 4)
	w.bits(7, 3)
	b, err := NewBlock(w.finish(), 48000, 6)
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	for i := 0; b.Remaining() > 0; i++ {
		if err := b.Next(&f); err != nil {
			t.Fatal(i, err)
		}
		// The second CPE has ms_mask_present=0 and must not inherit the first
		// CPE's all-band mask from the reused destination.
		if i == 1 && (f.MMode != 2 || !f.MS[0][0]) || i == 2 && (f.MMode != 0 || f.MS[0][0]) {
			t.Fatalf("element %d MMode=%d MS=%v", i, f.MMode, f.MS[0][0])
		}
	}
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(b.DataElements) != 1 || b.DataElements[0].Tag != 1 || len(b.Fills) != 2 {
		t.Fatalf("metadata DSE=%+v FIL=%+v", b.DataElements, b.Fills)
	}
	if err := b.Next(&f); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("Next after final element err=%v", err)
	}
}

func TestParseRejectsMultiElementConfiguration(t *testing.T) {
	if _, err := Parse(zeroBlockPayload(ElementSCE, ElementCPE), 48000, 3); !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestBlockZeroAlloc(t *testing.T) {
	payload := zeroBlockPayload(Elements(7)...)
	var f Frame
	var err error
	allocs := testing.AllocsPerRun(100, func() {
		var b Block
		if b, err = NewBlock(payload, 48000, 7); err != nil {
			return
		}
		for b.Remaining() > 0 && err == nil {
			err = b.Next(&f)
		}
		if err == nil {
			err = b.Finish()
		}
	})
	if err != nil || allocs != 0 {
		t.Fatalf("7.1 block parse allocated: %g, err=%v", allocs, err)
	}
}

func FuzzBlock(f *testing.F) {
	for config := 1; config <= 7; config++ {
		f.Add(zeroBlockPayload(Elements(config)...), config)
	}
	f.Fuzz(func(t *testing.T, data []byte, config int) {
		if len(data) > maxAccessUnitBytes+16 {
			return
		}
		b, err := NewBlock(data, 48000, config)
		if err != nil {
			return
		}
		var frame Frame
		channels := 0
		for b.Remaining() > 0 {
			if err := b.Next(&frame); err != nil {
				return
			}
			if frame.Element != Elements(config)[b.next-1] || frame.Count < 1 || frame.Count > 2 {
				t.Fatalf("element %v count %d", frame.Element, frame.Count)
			}
			channels += frame.Count
		}
		if b.Finish() == nil && channels > MaxChannels {
			t.Fatalf("channels %d", channels)
		}
	})
}
