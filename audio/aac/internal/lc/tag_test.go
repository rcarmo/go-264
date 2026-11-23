package lc

import (
	"errors"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func TestBlockRejectsDuplicateElementTags(t *testing.T) {
	for _, tc := range []struct {
		config   int
		elements []ElementType
		tags     []int
	}{
		{4, []ElementType{ElementSCE, ElementCPE, ElementSCE}, []int{3, 3, 3}},
		{6, []ElementType{ElementSCE, ElementCPE, ElementCPE, ElementLFE}, []int{5, 7, 7, 5}},
	} {
		var w bitWriter
		for i, e := range tc.elements {
			if e == ElementCPE {
				writeZeroPair(&w, tc.tags[i], 0)
			} else {
				writeZeroSingle(&w, int(e), tc.tags[i])
			}
		}
		w.bits(7, 3)
		if _, _, err := parseAllElements(w.finish(), tc.config); !errors.Is(err, pcm.ErrMalformed) {
			t.Fatalf("config %d got %v", tc.config, err)
		}
	}
	// Tags are scoped by element type, not a mandated zero-based sequence.
	var w bitWriter
	writeZeroSingle(&w, int(ElementSCE), 9)
	writeZeroPair(&w, 9, 0)
	writeZeroPair(&w, 14, 0)
	writeZeroSingle(&w, int(ElementLFE), 9)
	w.bits(7, 3)
	if _, n, err := parseAllElements(w.finish(), 6); err != nil || n != 6 {
		t.Fatal(n, err)
	}
}
