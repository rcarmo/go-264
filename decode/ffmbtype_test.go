package decode

import (
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func TestFFInterMBTypeP8x8L0Shape(t *testing.T) {
	got := ffInterMBType(&syntax.MBInter{MBType: syntax.PMBTypeP8x8})
	want := ffMBType8x8 | ffMBTypeP0L0 | ffMBTypeP1L0
	if got != want {
		t.Fatalf("ffInterMBType P8x8 = %d, want %d", got, want)
	}
}

func TestFFBidiMBTypeB8x8L0Shape(t *testing.T) {
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8, SubMBType: [4]uint32{1, 1, 1, 1}}
	got := ffBidiMBType(mb)
	want := ffMBType8x8 | ffMBTypeP0L0 | ffMBTypeP1L0
	if got != want {
		t.Fatalf("ffBidiMBType B8x8 L0 = %d, want %d", got, want)
	}
}

func TestFFBidiMBTypeDirect16x16Shape(t *testing.T) {
	got := ffBidiMBType(&syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16})
	want := ffMBType16x16 | ffMBTypeDirect2 | ffMBTypeP0L0 | ffMBTypeP1L0 | ffMBTypeP0L1 | ffMBTypeP1L1
	if got != want {
		t.Fatalf("ffBidiMBType Direct16x16 = %d, want %d", got, want)
	}
}
