package me

func sadSmallScalar(a, b []byte, strideA, strideB, size int) int {
	v := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := int(a[y*strideA+x]) - int(b[y*strideB+x])
			if d < 0 {
				d = -d
			}
			v += d
		}
	}
	return v
}

// The scalar public path retains legacy behaviour for malformed geometry.
// Assembly requires positive strides and complete addressed row extents.
func smallSADSafe(a, b []byte, strideA, strideB, size int) bool {
	return strideA >= size && strideB >= size && strideA <= (len(a)-size)/(size-1) && strideB <= (len(b)-size)/(size-1) && len(a) >= size && len(b) >= size
}
