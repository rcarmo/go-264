//go:build !amd64 || purego

package wav

func decodePCM8(dst []float64, src []byte)  { decodePCM8Scalar(dst, src) }
func decodePCM16(dst []float64, src []byte) { decodePCM16Scalar(dst, src) }
func decodePCM32(dst []float64, src []byte) { decodePCM32Scalar(dst, src) }
