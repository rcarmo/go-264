package filter

import "testing"

// FilterEdgeV layout: each row stored as [p3,p2,p1,p0,q0,q1,q2,q3] (8 bytes).
// pq[i*stride+0]=p3, [+1]=p2, [+2]=p1, [+3]=p0, [+4]=q0, [+5]=q1, [+6]=q2, [+7]=q3
const stride = 8

func makePQ(p3, p2, p1, p0, q0, q1, q2, q3 uint8) []uint8 {
	buf := make([]uint8, 4*stride)
	for i := 0; i < 4; i++ {
		base := i * stride
		buf[base+0] = p3
		buf[base+1] = p2
		buf[base+2] = p1
		buf[base+3] = p0
		buf[base+4] = q0
		buf[base+5] = q1
		buf[base+6] = q2
		buf[base+7] = q3
	}
	return buf
}

func TestLumaEdgesMatchSampleFormulas(t *testing.T) {
	// Mix enabled/disabled columns within each four-sample group, including values
	// on both sides of alpha/beta and every independent threshold-index pair.
	// The scalar single-pair formulas are also the oracle for strong edges.
	const pitch, columns, first = 20, 16, 2
	for indexA := 0; indexA < 52; indexA++ {
		for indexB := 0; indexB < 52; indexB++ {
			alpha, beta := alphaTable[indexA], betaTable[indexB]
			for _, base := range []int{0, 100, 250} {
				var input [8 * pitch]byte
				for i := range input {
					input[i] = 0xa5
				}
				for col := 0; col < columns; col++ {
					var samples [8]int
					switch col % 4 {
					case 0:
						samples = [8]int{0, beta - 1, beta - 1, 0, alpha - 1, alpha, alpha + beta - 1, 0}
					case 1:
						samples = [8]int{0, beta, beta - 1, 0, 1, beta, beta + 1, 0}
					case 2:
						samples = [8]int{0, beta - 1, beta, 0, 1, 0, 0, 0}
					case 3:
						for i := range samples {
							samples[i] = (i*7 + col*11 + indexA + indexB*3) % 23
						}
					}
					for row, sample := range samples {
						input[row*pitch+first+col] = Clip1(base + sample)
					}
				}
				for _, strengths := range [][4]int{{0, 1, 2, 3}, {3, 2, 1, 4}} {
					got, want := input, input
					for col := 0; col < columns; col++ {
						bs := strengths[col/4]
						if bs == 0 {
							continue
						}
						x := first + col
						var s [8]int
						for row := range s {
							s[row] = int(input[row*pitch+x])
						}
						want[3*pitch+x], want[2*pitch+x], want[pitch+x], want[4*pitch+x], want[5*pitch+x], want[6*pitch+x] = filterLumaSample(s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7], bs, alpha, beta, indexA)
					}
					// The final readable byte is the last column's q3. The SIMD
					// path must not need an extra vector or row of padding.
					FilterLumaEdgeH(got[:7*pitch+first+columns], pitch, 4, first, columns, strengths, indexA, indexB)
					if got != want {
						t.Fatalf("indexA=%d indexB=%d base=%d strengths=%v", indexA, indexB, base, strengths)
					}
					// Transpose the same sample pairs to cover the vertical
					// gather/scatter without a second copy of the filter oracle.
					var gotV, wantV [pitch * 8]byte
					for row := 0; row < 8; row++ {
						for col := 0; col < pitch; col++ {
							gotV[col*8+row] = input[row*pitch+col]
							wantV[col*8+row] = want[row*pitch+col]
						}
					}
					FilterLumaEdgeV(gotV[:], 8, 4, first, columns, strengths, indexA, indexB)
					if gotV != wantV {
						t.Fatalf("vertical indexA=%d indexB=%d base=%d strengths=%v", indexA, indexB, base, strengths)
					}
				}
			}
		}
	}
}

func TestVerticalLumaPartialGroupsKeepValidRows(t *testing.T) {
	const pitch, x = 12, 5
	for _, rowStart := range []int{-2, -1, 0, 1} {
		var got [6 * pitch]byte
		for row := 0; row < 6; row++ {
			copy(got[row*pitch+x-4:], []byte{100, 112, 114, 115, 118, 120, 121, 122})
		}
		want := got
		for i := 0; i < 8; i++ {
			row := rowStart + i
			if row < 0 || row >= 6 {
				continue
			}
			p := want[row*pitch+x-4 : row*pitch+x+4]
			p[3], p[2], p[1], p[4], p[5], p[6] = filterLumaSample(int(p[0]), int(p[1]), int(p[2]), int(p[3]), int(p[4]), int(p[5]), int(p[6]), int(p[7]), 1+i/4, alphaTable[27], betaTable[27], 27)
		}
		FilterLumaEdgeV(got[:5*pitch+x+4], pitch, x, rowStart, 8, [4]int{1, 2}, 27, 27)
		if got != want {
			t.Fatalf("rowStart=%d: partial group lost valid rows or changed other pixels", rowStart)
		}
	}
}

// TestFilterEdgeV_Skip: bS=0 → no changes.
func TestFilterEdgeV_Skip(t *testing.T) {
	pq := makePQ(100, 110, 120, 130, 140, 150, 160, 170)
	orig := append([]uint8(nil), pq...)
	FilterEdgeV(pq, stride, 0, 31)
	for i, v := range pq {
		if v != orig[i] {
			t.Errorf("bS=0: pq[%d]=%d changed from %d", i, v, orig[i])
		}
	}
}

// TestFilterEdgeV_OutOfRange: invalid indexA → no changes.
func TestFilterEdgeV_OutOfRange(t *testing.T) {
	pq := makePQ(100, 110, 120, 130, 140, 150, 160, 170)
	orig := append([]uint8(nil), pq...)
	FilterEdgeV(pq, stride, 2, -1)
	FilterEdgeV(pq, stride, 2, 52)
	for i, v := range pq {
		if v != orig[i] {
			t.Errorf("out-of-range: pq[%d]=%d changed from %d", i, v, orig[i])
		}
	}
}

// TestFilterEdgeV_ConditionNotMet: abs(p0-q0) >= alpha → filter skipped.
// alpha[31]=28; p0=100, q0=200 → abs=100 >= 28.
func TestFilterEdgeV_ConditionNotMet(t *testing.T) {
	pq := makePQ(95, 97, 99, 100, 200, 201, 202, 203)
	orig := append([]uint8(nil), pq...)
	FilterEdgeV(pq, stride, 2, 31)
	for i, v := range pq {
		if v != orig[i] {
			t.Errorf("condition not met: pq[%d]=%d changed from %d", i, v, orig[i])
		}
	}
}

// TestFilterEdgeV_Strong: bS=4, indexA=27 (alpha=17, beta=6).
// Values: p3=100,p2=112,p1=114,p0=115 | q0=118,q1=120,q2=121,q3=122
// abs(p0-q0)=3 < 17 ✓, abs(p1-p0)=1 < 6 ✓, abs(q1-q0)=2 < 6 ✓ → filter applies
// abs(p0-q0)=3 < alpha/4+2=6 ✓, abs(p2-p0)=3 < 6 ✓ → strong p side
// abs(q2-q0)=3 < 6 ✓ → strong q side
// new_p0 = (112+2*114+2*115+2*118+120+4)>>3 = 930>>3 = 116
// new_p1 = (112+114+115+118+2)>>2           = 461>>2 = 115
// new_p2 = (2*100+3*112+114+115+118+4)>>3   = 887>>3 = 110
// new_q0 = (114+2*115+2*118+2*120+121+4)>>3 = 945>>3 = 118
// new_q1 = (115+118+120+121+2)>>2           = 476>>2 = 119
// new_q2 = (2*122+3*121+120+118+115+4)>>3   = 964>>3 = 120
func TestFilterEdgeV_Strong(t *testing.T) {
	pq := makePQ(100, 112, 114, 115, 118, 120, 121, 122)
	FilterEdgeV(pq, stride, 4, 27)

	type chk struct {
		name      string
		idx, want int
	}
	checks := []chk{
		{"new_p0", 3, 116},
		{"new_p1", 2, 115},
		{"new_p2", 1, 110},
		{"new_q0", 4, 118},
		{"new_q1", 5, 119},
		{"new_q2", 6, 120},
		{"p3_unchanged", 0, 100},
		{"q3_unchanged", 7, 122},
	}
	for _, c := range checks {
		got := int(pq[c.idx])
		if got != c.want {
			t.Errorf("strong filter row 0 %s: got %d want %d", c.name, got, c.want)
		}
	}
	// All 4 rows should be identical
	for row := 1; row < 4; row++ {
		if pq[row*stride+3] != 116 {
			t.Errorf("strong filter row %d new_p0=%d want 116", row, pq[row*stride+3])
		}
	}
}

// TestFilterEdgeV_Normal: bS=2, indexA=31 (alpha=28, beta=8, tc0[1]=2).
// p3=105,p2=115,p1=118,p0=122 | q0=126,q1=130,q2=140,q3=150
// abs(p0-q0)=4<28, abs(p1-p0)=4<8, abs(q1-q0)=4<8 → applies
// tc=2, abs(p2-p0)=7<8 → tc++=3; abs(q2-q0)=14 NOT <8
// delta = Clip3(-3,3, ((4)*4+(-12)+4)>>3) = Clip3(-3,3,1) = 1
// new_p0=123, new_q0=125
// new_p1 = Clip1(118 + Clip3(-2,2, (115+124-236)>>1)) = Clip1(118+1) = 119
func TestFilterEdgeV_Normal(t *testing.T) {
	pq := makePQ(105, 115, 118, 122, 126, 130, 140, 150)
	FilterEdgeV(pq, stride, 2, 31)

	type chk struct {
		name      string
		idx, want int
	}
	checks := []chk{
		{"new_p0", 3, 123},
		{"new_q0", 4, 125},
		{"new_p1", 2, 119},
		{"q1_unchanged", 5, 130}, // abs(q2-q0)=14 >= beta → q1 not updated
		{"p3_unchanged", 0, 105},
		{"p2_unchanged", 1, 115},
	}
	for _, c := range checks {
		got := int(pq[c.idx])
		if got != c.want {
			t.Errorf("normal filter row 0 %s: got %d want %d", c.name, got, c.want)
		}
	}
}

// TestFilterEdgeV_StrongFallback: bS=4, abs(p0-q0) >= alpha/4+2 → weak formula.
// alpha[27]=17, alpha/4+2=6; p0=110,q0=120 → abs=10 >= 6 → fallback
// abs(p1-p0)=5<6 ✓, abs(q1-q0)=5<6 ✓ → outer condition ok
// fallback p[0] = (2*p1+p0+q1+2)>>2 = (230+110+125+2)>>2 = 467>>2 = 116
// fallback q[0] = (2*q1+q0+p1+2)>>2 = (250+120+115+2)>>2 = 487>>2 = 121
func TestFilterEdgeV_StrongFallback(t *testing.T) {
	pq := makePQ(95, 107, 115, 110, 120, 125, 131, 140)
	FilterEdgeV(pq, stride, 4, 27)
	if pq[3] != 116 {
		t.Errorf("strong fallback new_p0=%d want 116", pq[3])
	}
	if pq[4] != 121 {
		t.Errorf("strong fallback new_q0=%d want 121", pq[4])
	}
}

func TestFilterLumaEdgeHAllowsQ3OnFinalRow(t *testing.T) {
	const planeStride = 4
	plane := make([]uint8, 8*planeStride)
	rows := []uint8{100, 112, 114, 115, 118, 120, 121, 122}
	for y, value := range rows {
		for x := 0; x < planeStride; x++ {
			plane[y*planeStride+x] = value
		}
	}

	FilterLumaEdgeH(plane, planeStride, 4, 0, 4, [4]int{4}, 27, 27)

	for x := 0; x < planeStride; x++ {
		if got := plane[3*planeStride+x]; got != 116 {
			t.Errorf("p0 at x=%d: got %d want 116", x, got)
		}
		if got := plane[7*planeStride+x]; got != 122 {
			t.Errorf("q3 at x=%d changed to %d", x, got)
		}
	}
}

func TestFilterChromaEdgeHAllowsQ1OnFinalRow(t *testing.T) {
	const planeStride = 4
	plane := make([]uint8, 4*planeStride)
	rows := []uint8{114, 115, 118, 120}
	for y, value := range rows {
		for x := 0; x < planeStride; x++ {
			plane[y*planeStride+x] = value
		}
	}

	FilterChromaEdgeH(plane, planeStride, 2, 0, 4, [4]int{4, 4}, 27, 27)

	for x := 0; x < planeStride; x++ {
		if got := plane[planeStride+x]; got != 116 {
			t.Errorf("p0 at x=%d: got %d want 116", x, got)
		}
		if got := plane[3*planeStride+x]; got != 120 {
			t.Errorf("q1 at x=%d changed to %d", x, got)
		}
	}
}

func TestFilterChromaEdgeHUsesTwoColumnsPerBoundaryStrength(t *testing.T) {
	const planeStride = 8
	plane := make([]uint8, 4*planeStride)
	rows := []uint8{114, 115, 118, 120}
	for y, value := range rows {
		for x := 0; x < planeStride; x++ {
			plane[y*planeStride+x] = value
		}
	}

	// The second bS value represents luma columns 4-7 and therefore chroma
	// columns 2-3. It must not be stretched across four chroma columns.
	FilterChromaEdgeH(plane, planeStride, 2, 0, 8, [4]int{0, 4, 0, 0}, 27, 27)
	for x := 0; x < planeStride; x++ {
		want := uint8(115)
		if x == 2 || x == 3 {
			want = 116
		}
		if got := plane[planeStride+x]; got != want {
			t.Errorf("p0 at x=%d: got %d want %d", x, got, want)
		}
	}
}

func TestInterMotionBoundaryPSlice(t *testing.T) {
	a := MBDeblockInfo{}
	b := MBDeblockInfo{}
	a.RefIDL0[0], b.RefIDL0[0] = 12, 12
	a.RefIDL1[0], b.RefIDL1[0] = -1, -1
	a.MVL0[0], b.MVL0[0] = [2]int16{8, -4}, [2]int16{11, -1}
	if interMotionBoundary(&a, 0, &b, 0) {
		t.Fatal("quarter-sample deltas below four should not create bS=1")
	}
	b.MVL0[0][0] = 12
	if !interMotionBoundary(&a, 0, &b, 0) {
		t.Fatal("quarter-sample delta of four should create bS=1")
	}
	b.MVL0[0], b.RefIDL0[0] = a.MVL0[0], 14
	if !interMotionBoundary(&a, 0, &b, 0) {
		t.Fatal("different reference pictures should create bS=1")
	}
}

func TestInterMotionBoundaryBSliceAcceptsSwappedLists(t *testing.T) {
	a := MBDeblockInfo{IsB: true}
	b := MBDeblockInfo{IsB: true}
	a.RefIDL0[0], a.RefIDL1[0] = 10, 20
	a.MVL0[0], a.MVL1[0] = [2]int16{4, 8}, [2]int16{-4, 12}
	b.RefIDL0[0], b.RefIDL1[0] = 20, 10
	b.MVL0[0], b.MVL1[0] = a.MVL1[0], a.MVL0[0]
	if interMotionBoundary(&a, 0, &b, 0) {
		t.Fatal("equivalent swapped B-slice lists should not create bS=1")
	}
	b.MVL1[0][1] += 4
	if !interMotionBoundary(&a, 0, &b, 0) {
		t.Fatal("swapped-list MV delta of four should create bS=1")
	}
}

// TestClip1 covers Clip1 boundary and normal cases.
func TestClip1(t *testing.T) {
	cases := []struct {
		in   int
		want uint8
	}{
		{-1, 0}, {0, 0}, {1, 1}, {128, 128}, {255, 255}, {256, 255}, {10000, 255}, {-10000, 0},
	}
	for _, c := range cases {
		if got := Clip1(c.in); got != c.want {
			t.Errorf("Clip1(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

// TestClip3 covers Clip3 boundary and normal cases.
func TestClip3(t *testing.T) {
	cases := []struct{ lo, hi, v, want int }{
		{0, 10, 5, 5}, {0, 10, -1, 0}, {0, 10, 11, 10},
		{-5, 5, 0, 0}, {-5, 5, -10, -5}, {-5, 5, 10, 5},
	}
	for _, c := range cases {
		if got := Clip3(c.lo, c.hi, c.v); got != c.want {
			t.Errorf("Clip3(%d,%d,%d)=%d want %d", c.lo, c.hi, c.v, got, c.want)
		}
	}
}

func TestChromaEdgesMatchSampleFormulas(t *testing.T) {
	const pitch, first, columns = 12, 2, 8
	for indexA := 0; indexA < 52; indexA++ {
		for indexB := 0; indexB < 52; indexB++ {
			alpha, beta := alphaTable[indexA], betaTable[indexB]
			for _, base := range []int{0, 100, 250} {
				var input [4 * pitch]byte
				for i := range input {
					input[i] = 0xa5
				}
				for col := 0; col < columns; col++ {
					var values [4]int
					switch col % 4 {
					case 0:
						values = [4]int{beta - 1, 0, alpha - 1, alpha + beta - 2}
					case 1:
						values = [4]int{beta, 0, 1, 1}
					case 2:
						values = [4]int{0, 0, alpha, alpha}
					case 3:
						for row := range values {
							values[row] = (row*7 + col*11 + indexA + indexB*3) % 23
						}
					}
					for row, value := range values {
						input[row*pitch+first+col] = Clip1(base + value)
					}
				}
				for _, strengths := range [][4]int{{0, 1, 2, 3}, {4, 3, 1, 2}, {2, 4, 0, 1}, {1, 2, 3, 4}} {
					got, want := input, input
					for col := 0; col < columns; col++ {
						bs, x := strengths[col/2], first+col
						if bs != 0 {
							want[pitch+x], want[2*pitch+x] = filterChromaSample(int(input[x]), int(input[pitch+x]), int(input[2*pitch+x]), int(input[3*pitch+x]), bs, alpha, beta, indexA)
						}
					}
					FilterChromaEdgeH(got[:3*pitch+first+columns], pitch, 2, first, columns, strengths, indexA, indexB)
					if got != want {
						t.Fatalf("horizontal indexA=%d indexB=%d base=%d strengths=%v", indexA, indexB, base, strengths)
					}
					// Reuse the same independently derived outputs after a
					// transpose, including untouched border rows and columns.
					// Padding on both sides of each vertical window catches
					// hard-coded packed strides as well as stray stores.
					const verticalStride, windowStart = 12, 3
					var gotV, wantV [pitch * verticalStride]byte
					for i := range gotV {
						gotV[i], wantV[i] = 0xa5, 0xa5
					}
					for row := 0; row < 4; row++ {
						for col := 0; col < pitch; col++ {
							gotV[col*verticalStride+windowStart+row] = input[row*pitch+col]
							wantV[col*verticalStride+windowStart+row] = want[row*pitch+col]
						}
					}
					end := (first+columns-1)*verticalStride + windowStart + 4
					FilterChromaEdgeV(gotV[:end], verticalStride, windowStart+2, first, columns, strengths, indexA, indexB)
					if gotV != wantV {
						t.Fatalf("vertical indexA=%d indexB=%d base=%d strengths=%v", indexA, indexB, base, strengths)
					}
				}
			}
		}
	}
}
