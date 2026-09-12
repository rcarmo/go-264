package decode

import (
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func TestDecodeCABACIntraMBIntoResetsCallerStorage(t *testing.T) {
	storage := syntax.MBIntra{MBType: 99, QPDelta: 7, Coeffs: [16][16]int16{{1}}}
	got := decodeCABACIntraMBInto(&storage, nil, nil, 0,
		nil, nil, nil, nil, 0, 0, 0, 0, 0, 0,
		false, 0, [2]int8{}, [2]int8{})
	if got != &storage {
		t.Fatal("decoder did not return caller-owned intra storage")
	}
	if storage != (syntax.MBIntra{}) {
		t.Fatalf("caller-owned intra storage was not reset: %+v", storage)
	}
}

func TestDecodeCABACPInterMBIntoResetsCallerStorage(t *testing.T) {
	storage := syntax.MBInter{MBType: 99, QPDelta: 7, Coeffs: [16][16]int16{{1}}}
	got, intra, skipped := decodeCABACPInterMBInto(&storage, nil, nil, nil, 0, 0,
		nil, nil, nil, nil, 0, 0, false, false,
		[4]int{}, nil, nil, 0, 0, 0, 0, false, 0,
		0, 0, 0, 0, [2]int8{}, [2]int8{})
	if got != &storage || intra != nil || !skipped {
		t.Fatalf("wrong fallback result: got=%p storage=%p intra=%p skipped=%t", got, &storage, intra, skipped)
	}
	want := syntax.MBInter{MBType: syntax.PMBTypeP16x16}
	if storage != want {
		t.Fatalf("caller-owned P storage was not reset: got=%+v want=%+v", storage, want)
	}
}

func TestDecodeCABACBidiMBIntoResetsCallerStorage(t *testing.T) {
	storage := syntax.MBBidi{MBType: 99, QPDelta: 7, Coeffs: [16][16]int16{{1}}}
	got, intra, skipped := decodeCABACBidiMBInto(&storage, nil, nil, nil,
		0, 0, 0, nil, nil, nil, nil, 0, 0,
		false, false, true, true, [4]int{}, nil, nil, nil, nil, nil, nil, nil,
		0, 0, 0, 0, false, 0, syntax.MotionVector{}, 0, syntax.MotionVector{},
		nil, nil, 0, false, 0, 0, 0, 0, 0, [2]int8{}, [2]int8{})
	if got != &storage || intra != nil || !skipped {
		t.Fatalf("wrong fallback result: got=%p storage=%p intra=%p skipped=%t", got, &storage, intra, skipped)
	}
	if storage != (syntax.MBBidi{}) {
		t.Fatalf("caller-owned B storage was not reset: %+v", storage)
	}
}
