package decode

import (
	"strings"
	"testing"

	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func referenceSkipSlice(number uint32, mods []syntax.RefPicListModification, mmco []syntax.MemoryManagementControl) nal.Unit {
	w := &assemblyBits{}
	w.ue(0)
	w.ue(syntax.SliceTypeP)
	w.ue(0)
	w.uint(number, 4)
	w.bit(0) // default one active reference
	if len(mods) > 0 {
		w.bit(1)
		for _, m := range mods {
			w.ue(m.Op)
			w.ue(m.Val)
		}
		w.ue(3)
	} else {
		w.bit(0)
	}
	if len(mmco) > 0 {
		w.bit(1)
		for _, m := range mmco {
			w.ue(m.Op)
			w.ue(m.DifferenceOfPicNumsMinus1)
		}
		w.ue(0)
	} else {
		w.bit(0)
	}
	w.ue(0)
	w.ue(1)
	w.ue(1) // QP delta0, filter off, one skipped MB
	w.bit(1)
	w.align()
	return nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1, Payload: w.bytes()}
}

func primedReferenceDecoder(t *testing.T) *Decoder {
	t.Helper()
	d := assemblyDecoder(1, 1)
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInvalidReferencesDoNotChangeCommittedState(t *testing.T) {
	for _, tt := range []struct {
		name string
		unit nal.Unit
		want string
	}{
		{"missing modification", referenceSkipSlice(1, []syntax.RefPicListModification{{Op: 0, Val: 2}}, nil), "missing frame_num"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := primedReferenceDecoder(t)
			good := d.DPB.Frames[0]
			before := d.pocState()
			frames, err := d.Decode(assemblyInput(tt.unit))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if len(frames) != 0 || len(d.Frames) != 1 || len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != good || d.pocState() != before {
				t.Fatal("failed picture mutated committed reference/output/POC state")
			}
		})
	}
}
