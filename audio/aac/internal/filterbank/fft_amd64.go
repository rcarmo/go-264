//go:build amd64 && !purego

package filterbank

// All amd64 CPUs support SSE2. AVX2 dispatch additionally requires OSXSAVE and
// XCR0 XMM/YMM state; the assembly probe checks the complete contract before a
// YMM instruction can execute. Internal callers provide valid plan geometry.
var fftHasAVX2 = fftCPUHasAVX2()

func fftStage(x, roots []complex128, half, stride int) {
	if len(x) == 0 {
		return
	}
	if fftHasAVX2 && half >= 2 {
		fftStageAVX2(&x[0], &roots[0], len(x), half, stride)
		return
	}
	fftStageSSE2(&x[0], &roots[0], len(x), half, stride)
}

//go:noescape
func fftCPUHasAVX2() bool

//go:noescape
func fftStageSSE2(x, roots *complex128, n, half, stride int)

//go:noescape
func fftStageAVX2(x, roots *complex128, n, half, stride int)
