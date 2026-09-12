package cabac

import (
	"github.com/rcarmo/go-264/nal"
	"testing"
)

func TestResidualTraceFlagRefreshesAtReset(t *testing.T) {
	t.Setenv("GO264_CABAC_RESIDUAL_TRACE", "1")
	dec := NewCABACDecoder(nal.NewReader(make([]byte, 4)))
	if !dec.traceResidual {
		t.Fatal("trace flag not captured")
	}
	t.Setenv("GO264_CABAC_RESIDUAL_TRACE", "")
	dec.Reset()
	if dec.traceResidual {
		t.Fatal("trace flag not refreshed")
	}
}

func TestResidualZeroValueSnapshotsTraceLazily(t *testing.T) {
	t.Setenv("GO264_CABAC_RESIDUAL_TRACE", "1")
	dec := &CABACDecoder{}
	dec.DecodeCABACResidual(make([]CABACCtx, 1024), 5, 64, make([]int16, 64), 0, 0)
	if !dec.traceResidualSet || !dec.traceResidual {
		t.Fatal("zero-value decoder did not snapshot trace")
	}
}

func TestResidualDecodeZeroAllocWithTracingOff(t *testing.T) {
	t.Setenv("GO264_CABAC_RESIDUAL_TRACE", "")
	data := make([]byte, 4096)
	r := nal.NewReader(data)
	dec := NewCABACDecoder(r)
	base := InitContextModels(26, 0, true)
	models := append([]CABACCtx(nil), base...)
	var output [64]int16
	allocs := testing.AllocsPerRun(100, func() {
		copy(models, base)
		clear(output[:])
		dec.Reset()
		dec.DecodeCABACResidual(models, 5, 64, output[:], 0, 0)
	})
	if allocs != 0 {
		t.Fatal("trace-disabled residual allocations", allocs)
	}
}
