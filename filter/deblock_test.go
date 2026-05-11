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

	type chk struct{ name string; idx, want int }
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

	type chk struct{ name string; idx, want int }
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
