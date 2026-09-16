package ac3

import "math"

type complexSample struct{ real, imaginary float64 }

func (d *Decoder) initWindow() {
	var values [257]float64
	var sum float64
	for n := range values {
		x := 2*float64(n)/256 - 1
		values[n] = besselI0(math.Pi * 5 * math.Sqrt(math.Max(0, 1-x*x)))
		sum += values[n]
	}
	var cumulative float64
	for n := range d.window {
		cumulative += values[n]
		d.window[n] = math.Sqrt(cumulative / sum)
	}
}

func besselI0(x float64) float64 {
	absolute := math.Abs(x)
	if absolute < 3.75 {
		y := x / 3.75
		y *= y
		return 1 + y*(3.5156229+y*(3.0899424+y*(1.2067492+y*(0.2659732+y*(0.0360768+y*0.0045813)))))
	}
	y := 3.75 / absolute
	return math.Exp(absolute) / math.Sqrt(absolute) * (0.39894228 + y*(0.01328592+y*(0.00225319+y*(-0.00157565+y*(0.00916281+y*(-0.02057706+y*(0.02635537+y*(-0.01647633+y*0.00392377))))))))
}

func inverseFFT(values []complexSample) {
	for i, j := 1, 0; i < len(values); i++ {
		bit := len(values) >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			values[i], values[j] = values[j], values[i]
		}
	}
	for length := 2; length <= len(values); length <<= 1 {
		angle := 2 * math.Pi / float64(length)
		rootReal, rootImaginary := math.Cos(angle), math.Sin(angle)
		for i := 0; i < len(values); i += length {
			wReal, wImaginary := 1.0, 0.0
			for k := 0; k < length/2; k++ {
				a := values[i+k]
				b := values[i+k+length/2]
				tReal := b.real*wReal - b.imaginary*wImaginary
				tImaginary := b.real*wImaginary + b.imaginary*wReal
				values[i+k] = complexSample{a.real + tReal, a.imaginary + tImaginary}
				values[i+k+length/2] = complexSample{a.real - tReal, a.imaginary - tImaginary}
				wReal, wImaginary = wReal*rootReal-wImaginary*rootImaginary, wReal*rootImaginary+wImaginary*rootReal
			}
		}
	}
}

func (d *Decoder) imdctBlock(channel int, coefficients [256]float64, short bool, output []float64) {
	var transformed [512]float64
	if short {
		d.imdctShort(coefficients, &transformed)
	} else {
		d.imdctLong(coefficients, &transformed)
	}
	overlapAdd(output, transformed[:256], d.overlap[channel][:])
	copy(d.overlap[channel][:], transformed[256:])
}

func overlapAddScalar(output, transformed, previous []float64) {
	for i := range output {
		output[i] = (transformed[i] + previous[i]) * 2
	}
}

func (d *Decoder) imdctLong(input [256]float64, output *[512]float64) {
	var z, y [128]complexSample
	for k := range z {
		angle := 2 * math.Pi * float64(8*k+1) / (8 * 512)
		cosine, sine := -math.Cos(angle), -math.Sin(angle)
		a, b := input[255-2*k], input[2*k]
		z[k] = complexSample{a*cosine - b*sine, b*cosine + a*sine}
	}
	inverseFFT(z[:])
	for n := range y {
		angle := 2 * math.Pi * float64(8*n+1) / (8 * 512)
		cosine, sine := -math.Cos(angle), -math.Sin(angle)
		y[n] = complexSample{z[n].real*cosine - z[n].imaginary*sine, z[n].imaginary*cosine + z[n].real*sine}
	}
	for n := 0; n < 64; n++ {
		output[2*n] = -y[64+n].imaginary * d.window[2*n]
		output[2*n+1] = y[63-n].real * d.window[2*n+1]
		output[128+2*n] = -y[n].real * d.window[128+2*n]
		output[128+2*n+1] = y[127-n].imaginary * d.window[128+2*n+1]
		output[256+2*n] = -y[64+n].real * d.window[255-2*n]
		output[256+2*n+1] = y[63-n].imaginary * d.window[254-2*n]
		output[384+2*n] = y[n].imaginary * d.window[127-2*n]
		output[384+2*n+1] = -y[127-n].real * d.window[126-2*n]
	}
}

func (d *Decoder) imdctShort(input [256]float64, output *[512]float64) {
	var z1, z2, y1, y2 [64]complexSample
	var a, b [128]float64
	for k := range a {
		a[k], b[k] = input[2*k], input[2*k+1]
	}
	for k := range z1 {
		angle := 2 * math.Pi * float64(8*k+1) / (4 * 512)
		cosine, sine := -math.Cos(angle), -math.Sin(angle)
		z1[k] = complexSample{a[127-2*k]*cosine - a[2*k]*sine, a[2*k]*cosine + a[127-2*k]*sine}
		z2[k] = complexSample{b[127-2*k]*cosine - b[2*k]*sine, b[2*k]*cosine + b[127-2*k]*sine}
	}
	inverseFFT(z1[:])
	inverseFFT(z2[:])
	for n := range y1 {
		angle := 2 * math.Pi * float64(8*n+1) / (4 * 512)
		cosine, sine := -math.Cos(angle), -math.Sin(angle)
		y1[n] = complexSample{z1[n].real*cosine - z1[n].imaginary*sine, z1[n].imaginary*cosine + z1[n].real*sine}
		y2[n] = complexSample{z2[n].real*cosine - z2[n].imaginary*sine, z2[n].imaginary*cosine + z2[n].real*sine}
	}
	for n := 0; n < 64; n++ {
		output[2*n] = -y1[n].imaginary * d.window[2*n]
		output[2*n+1] = y1[63-n].real * d.window[2*n+1]
		output[128+2*n] = -y1[n].real * d.window[128+2*n]
		output[128+2*n+1] = y1[63-n].imaginary * d.window[128+2*n+1]
		output[256+2*n] = -y2[n].real * d.window[255-2*n]
		output[256+2*n+1] = y2[63-n].imaginary * d.window[254-2*n]
		output[384+2*n] = y2[n].imaginary * d.window[127-2*n]
		output[384+2*n+1] = -y2[63-n].real * d.window[126-2*n]
	}
}
