package filterbank

import "math"

// This FFT reduction is derived from the IMDCT cosine sum used by the scalar
// oracle in filterbank_test.go; it does not import another decoder's transform.
// Let a=(N/2+1)/2. Pre-rotate c[k] by exp(i*2*pi*a*k/N), zero-pad to N,
// compute the positive-sign DFT and rotate output n by exp(i*pi*(n+a)/N).
// Its real part times 2/N equals the direct IMDCT. No FFT normalisation is used.
type transformPlan struct{ pre, post, roots []complex128 }

func makePlan(n int) transformPlan {
	p := transformPlan{pre: make([]complex128, n/2), post: make([]complex128, n), roots: make([]complex128, n/2)}
	unit := func(a float64) complex128 { s, c := math.Sincos(a); return complex(c, s) }
	a := float64(n/2+1) / 2
	for k := range p.pre {
		p.pre[k] = unit(2 * math.Pi * a * float64(k) / float64(n))
	}
	for k := range p.post {
		p.post[k] = unit(math.Pi * (float64(k) + a) / float64(n))
	}
	for k := range p.roots {
		p.roots[k] = unit(2 * math.Pi * float64(k) / float64(n))
	}
	return p
}

var longPlan = makePlan(longTransform)
var shortPlan = makePlan(shortTransform)

func imdct(coeff []float64, n int, dst []float64) {
	p := &longPlan
	if n == shortTransform {
		p = &shortPlan
	}
	var scratch [longTransform]complex128
	x := scratch[:n]
	rotateInput(x[:len(coeff)], coeff, p.pre)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		stride := n / size
		fftStage(x, p.roots, half, stride)
	}
	scale := 2 / float64(n)
	rotateOutput(dst, x, p.post, scale)
}
