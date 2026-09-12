package cabac

import (
	"github.com/rcarmo/go-264/nal"
	"testing"
)

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
