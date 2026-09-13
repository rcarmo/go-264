//go:build amd64 && !purego

package convert

func allFinite(src []float64) bool {
	if len(src) == 0 {
		return true
	}
	return finiteSSE2(&src[0], len(src))
}

//go:noescape
func finiteSSE2(src *float64, n int) bool
