//go:build amd64 && !purego

package wav

func decodePCM8(dst []float64, src []byte) {
	bulk := len(dst) &^ 3
	if bulk != 0 {
		decodePCM8SSE2(&dst[0], &src[0], bulk)
	}
	decodePCM8Scalar(dst[bulk:], src[bulk:])
}

func decodePCM16(dst []float64, src []byte) {
	bulk := len(dst) &^ 3
	if bulk != 0 {
		decodePCM16SSE2(&dst[0], &src[0], bulk)
	}
	decodePCM16Scalar(dst[bulk:], src[bulk*2:])
}

func decodePCM32(dst []float64, src []byte) {
	bulk := len(dst) &^ 3
	if bulk != 0 {
		decodePCM32SSE2(&dst[0], &src[0], bulk)
	}
	decodePCM32Scalar(dst[bulk:], src[bulk*4:])
}

//go:noescape
func decodePCM8SSE2(dst *float64, src *byte, n int)

//go:noescape
func decodePCM16SSE2(dst *float64, src *byte, n int)

//go:noescape
func decodePCM32SSE2(dst *float64, src *byte, n int)
