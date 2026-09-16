package convert

import "math"

func allFiniteScalar(src []float64) bool {
	for _, v := range src {
		if math.Float64bits(v)&0x7ff0000000000000 == 0x7ff0000000000000 {
			return false
		}
	}
	return true
}
