package slice

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

// --- nC context helpers ---

func TestCombineNC(t *testing.T) {
	cases := []struct{ nA, nB, want int }{
		{-1, -1, 0},  // both unavailable → 0
		{-1, 4, 4},   // only nB available → nB
		{6, -1, 6},   // only nA available → nA
		{4, 6, 5},    // both available → (4+6+1)/2 = 5
		{0, 0, 0},    // both zero → 0
		{3, 3, 3},    // equal → 3
		{4, 5, 4},    // (4+5+1)/2 = 5 → 5? no: (9+1)>>1=5. Wait (4+5+1)=10>>1=5
		{1, 2, 1},    // (1+2+1)/2=2 → 2? (3+1)>>1=2
	}
	// Rebuild expected correctly
	correct := []int{0, 4, 6, 5, 0, 3, 5, 2}
	for i, c := range cases {
		got := combineNC(c.nA, c.nB)
		if got != correct[i] {
			t.Errorf("combineNC(%d,%d)=%d want %d", c.nA, c.nB, got, correct[i])
		}
	}
}

func TestNeighbourNC(t *testing.T) {
	// nil → -1
	if got := neighbourNC(nil, 0); got != -1 {
		t.Errorf("nil: got %d want -1", got)
	}
	nz := &[16]int{}
	nz[5] = 7
	if got := neighbourNC(nz, 5); got != 7 {
		t.Errorf("nz[5]: got %d want 7", got)
	}
	if got := neighbourNC(nz, 0); got != 0 {
		t.Errorf("nz[0]: got %d want 0", got)
	}
}

func TestComputeNC4x4(t *testing.T) {
	nz := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	// blkIdx=0: col=0, row=0 → nA=-1 (no left), nB=-1 (no top) → combineNC(-1,-1)=0
	got := computeNC4x4(0, nz)
	if got != 0 {
		t.Errorf("blk0 with no context: got %d want 0", got)
	}
	// blkIdx=1: col=1, row=0 → nA=nz[BlkXYToIdx[0][0]]=nz[0]=1, nB=-1 → combineNC(1,-1)=1
	got = computeNC4x4(1, nz)
	if got != 1 {
		t.Errorf("blk1: got %d want 1", got)
	}
}

func TestComputeNC4x4Ctx_CrossMB(t *testing.T) {
	nz := make([]int, 16)
	leftNZ := &[16]int{}
	topNZ := &[16]int{}
	leftNZ[BlkXYToIdx[0][3]] = 8  // right-edge of left MB, row 0
	topNZ[BlkXYToIdx[3][0]] = 4   // bottom-edge of top MB, col 0

	// blkIdx=0: col=0, row=0 → crosses left MB (leftNZ) and top MB (topNZ)
	got := computeNC4x4Ctx(0, nz, leftNZ, topNZ)
	// nA = leftNZ[BlkXYToIdx[0][3]] = 8, nB = topNZ[BlkXYToIdx[3][0]] = 4
	// combineNC(8,4) = (8+4+1)>>1 = 6
	if got != 6 {
		t.Errorf("blk0 cross-MB: got %d want 6", got)
	}
}

func TestComputeNCLumaDC(t *testing.T) {
	// Both nil → combineNC(-1,-1) = 0
	got := computeNCLumaDC(nil, nil)
	if got != 0 {
		t.Errorf("nil/nil: got %d want 0", got)
	}

	leftNZ := &[16]int{}
	leftNZ[BlkXYToIdx[0][3]] = 6
	got = computeNCLumaDC(leftNZ, nil)
	if got != 6 {
		t.Errorf("leftNZ only: got %d want 6", got)
	}
}

func TestComputeNCChroma4x4Ctx(t *testing.T) {
	nz := []int{2, 3, 4, 5}
	// blkIdx=0: x=0, y=0 → nA from left MB (nil → -1), nB from top MB (nil → -1) → 0
	got := computeNCChroma4x4Ctx(0, nz, nil, nil, 0)
	if got != 0 {
		t.Errorf("blk0 no ctx: got %d want 0", got)
	}
	// blkIdx=1: x=1, y=0 → nA=nz[0]=2, nB=nil → combineNC(2,-1)=2
	got = computeNCChroma4x4Ctx(1, nz, nil, nil, 0)
	if got != 2 {
		t.Errorf("blk1: got %d want 2", got)
	}
}

// --- CBP tables ---

func TestDecodeCBPIntra_Table(t *testing.T) {
	// Table 9-4: known codeNum → CBP mappings for intra
	cases := []struct {
		codeNum uint // encoded as UE(codeNum)
		wantCBP uint32
	}{
		{0, 47},
		{1, 31},
		{2, 15},
		{3, 0},
		{4, 23},
		{47, 41},
	}
	for _, tc := range cases {
		// Encode UE(codeNum) into a byte buffer
		buf := encodeUE(tc.codeNum)
		r := nal.NewReader(buf)
		got := decodeCBPIntra(r)
		if got != tc.wantCBP {
			t.Errorf("decodeCBPIntra(codeNum=%d)=%d want %d", tc.codeNum, got, tc.wantCBP)
		}
	}
	// Out of range codeNum (>= 48) → returns 0
	buf := encodeUE(48)
	r := nal.NewReader(buf)
	got := decodeCBPIntra(r)
	if got != 0 {
		t.Errorf("decodeCBPIntra(codeNum=48)=%d want 0", got)
	}
}

// --- DecodeMBIntra bitstream tests ---

// TestDecodeMBIntra_I16x16_Zero tests mb_type=1 (I_16x16_0: pred=0, cbpLuma=0, cbpChroma=0).
// Bitstream: UE(1)="010" + chromaMode UE(0)="1" + padding
// CBP=0 → no QPDelta, no residuals.
func TestDecodeMBIntra_I16x16_Zero(t *testing.T) {
	// UE(1) = "010", UE(0) = "1" → bits "010 1 0000" = 0x50
	buf := []byte{0x50, 0x00}
	r := nal.NewReader(buf)
	mb := DecodeMBIntra(r, IntraDecodeOpts{SliceQP: 26})
	if mb.MBType != 1 {
		t.Fatalf("MBType=%d want 1", mb.MBType)
	}
	if mb.Intra16x16PredMode != 0 {
		t.Errorf("PredMode=%d want 0", mb.Intra16x16PredMode)
	}
	if mb.CodedBlockPattern != 0 {
		t.Errorf("CBP=%d want 0", mb.CodedBlockPattern)
	}
	if mb.ChromaPredMode != 0 {
		t.Errorf("ChromaPredMode=%d want 0", mb.ChromaPredMode)
	}
	if mb.QPDelta != 0 {
		t.Errorf("QPDelta=%d want 0", mb.QPDelta)
	}
}

// TestDecodeMBIntra_INxN_AllPredicted tests mb_type=0 (I_NxN) with all
// prev_intra4x4_pred_mode_flag=1 and CBP=0 (no residuals).
// Bitstream: UE(0)="1" + 16×"1" + UE(0)="1" + UE(3)="00100" = 23 bits
// = 0xFF 0xFF 0x90
func TestDecodeMBIntra_INxN_AllPredicted(t *testing.T) {
	buf := []byte{0xFF, 0xFF, 0xC8}
	r := nal.NewReader(buf)
	mb := DecodeMBIntra(r, IntraDecodeOpts{SliceQP: 26})
	if mb.MBType != 0 {
		t.Fatalf("MBType=%d want 0", mb.MBType)
	}
	for i, m := range mb.IntraPredMode {
		if m != -1 {
			t.Errorf("IntraPredMode[%d]=%d want -1", i, m)
		}
	}
	if mb.ChromaPredMode != 0 {
		t.Errorf("ChromaPredMode=%d want 0", mb.ChromaPredMode)
	}
	if mb.CodedBlockPattern != 0 {
		t.Errorf("CBP=%d want 0", mb.CodedBlockPattern)
	}
}

// TestDecodeMBIntra_I16x16_WithCBP tests I_16x16 with non-zero CBP.
// mb_type=13 → pred=0, cbpChroma=1, cbpLuma=1 (cbpLuma flag: (13-1)/12=1>0 → cbpLuma=15)
// (13-1)=12 → pred=12%4=0, cbpChroma=12/4%3=0, (12)/12=1→cbpLuma=15
// CBP = 15 | (0<<4) = 15
// Bitstream: UE(13)="0000110110" + chromaMode + QPDelta + DC block (all zeros)...
// This is complex so just verify mb_type parsing.
func TestDecodeMBIntraWithType_I16x16_Variants(t *testing.T) {
	cases := []struct {
		mbType          uint32
		wantPredMode    int8
		wantCBPLumaFlag bool // whether cbpLuma=15
	}{
		{1, 0, false},   // pred=0, cbp=0
		{2, 1, false},   // pred=1, cbp=0
		{5, 0, false},   // pred=0, cbpChroma=1, cbpLuma=0
		{13, 0, true},   // pred=0, cbpChroma=0, cbpLuma=15
		{17, 0, true},   // pred=0, cbpChroma=1, cbpLuma=15
		{24, 3, true},   // pred=3, cbpChroma=2, cbpLuma=15
	}
	for _, tc := range cases {
		// Build minimal bitstream: just chromaMode=UE(0)="1"
		// (mb_type payload already consumed: predMode/CBP in mb_type)
		buf := []byte{0x80} // UE(0)="1" as first bit
		r := nal.NewReader(buf)
		mb := DecodeMBIntraWithType(r, tc.mbType, IntraDecodeOpts{SliceQP: 0})
		if mb.MBType != tc.mbType {
			t.Errorf("mbType=%d: got MBType=%d", tc.mbType, mb.MBType)
		}
		if mb.Intra16x16PredMode != tc.wantPredMode {
			t.Errorf("mbType=%d: PredMode=%d want %d", tc.mbType, mb.Intra16x16PredMode, tc.wantPredMode)
		}
		gotLuma := (mb.CodedBlockPattern & 0xF) == 15
		if gotLuma != tc.wantCBPLumaFlag {
			t.Errorf("mbType=%d: cbpLuma flag=%v want %v (CBP=%d)", tc.mbType, gotLuma, tc.wantCBPLumaFlag, mb.CodedBlockPattern)
		}
	}
}

// TestBlkXYToIdx verifies the exported table values.
func TestBlkXYToIdx(t *testing.T) {
	want := [4][4]int{
		{0, 1, 4, 5},
		{2, 3, 6, 7},
		{8, 9, 12, 13},
		{10, 11, 14, 15},
	}
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if BlkXYToIdx[r][c] != want[r][c] {
				t.Errorf("BlkXYToIdx[%d][%d]=%d want %d", r, c, BlkXYToIdx[r][c], want[r][c])
			}
		}
	}
}

func TestBlk4x4ColRow(t *testing.T) {
	// col and row for each blkIdx must be consistent with BlkXYToIdx
	for idx := 0; idx < 16; idx++ {
		col := Blk4x4Col[idx]
		row := Blk4x4Row[idx]
		if BlkXYToIdx[row][col] != idx {
			t.Errorf("blkIdx=%d: Blk4x4Col=%d Row=%d → BlkXYToIdx[%d][%d]=%d want %d",
				idx, col, row, row, col, BlkXYToIdx[row][col], idx)
		}
	}
}

// TestDecodeMBInter_P16x16 decodes a minimal P16x16 inter MB.
// Bitstream: mb_type=UE(0)="1" (P16x16) + ref (skip, numRef=1) + mvdX=SE(0)="1" + mvdY=SE(0)="1"
// + CBP = inter UE(0)="1"→cbpInterTable[0]=0
// = "1 1 1 00 1" padded → bits: 1,1,1,0,0,1,... = 0b1110_0100 = 0xE4, 0x00...
func TestDecodeMBInter_P16x16(t *testing.T) {
	// mb_type=P16x16: UE(0)="1"
	// no ref_idx (numRef=1 → only 1 ref, skip)
	// mvdX=SE(0)="1", mvdY=SE(0)="1"
	// CBP=UE(0)="1" → cbpInterTable[0]=0 (no residuals)
	// QPDelta: only read if CBP>0
	// Total: 1+1+1+1 = 4 bits = 0x80 → 1000 0000
	// Wait: UE(0)="1", SE(0)="1", SE(0)="1", CBP_inter UE(0)="1" → cbpInterTable[0]=0
	// bits: 1,1,1,1 = 0xF0
	buf := []byte{0xF0, 0x00}
	r := nal.NewReader(buf)
	mb := DecodeMBInter(r, InterDecodeOpts{SliceQP: 26, NumRefFrames: 1})
	if mb.MBType != PMBTypeP16x16 {
		t.Fatalf("MBType=%d want %d (P16x16)", mb.MBType, PMBTypeP16x16)
	}
	if mb.MV[0].X != 0 || mb.MV[0].Y != 0 {
		t.Errorf("MV[0]=(%d,%d) want (0,0)", mb.MV[0].X, mb.MV[0].Y)
	}
	if mb.CBP != 0 {
		t.Errorf("CBP=%d want 0", mb.CBP)
	}
}

// TestDecodeMBInter_Intra flags intra-in-P.
// mb_type >= PMBTypeIntra (5) → should return immediately with MBType set.
func TestDecodeMBInter_IntraFlag(t *testing.T) {
	// mb_type=PMBTypeIntra=5: UE(5)="00011 0"
	// UE(5): leadingZeros=2, suffix=2bits, value=4-1+2=5
	// encoding: 00 1 10 → "001 10" = 00110... = 0x30
	buf := encodeUE(5)
	r := nal.NewReader(buf)
	mb := DecodeMBInter(r, InterDecodeOpts{NumRefFrames: 1})
	if mb.MBType != PMBTypeIntra {
		t.Errorf("MBType=%d want %d (Intra sentinel)", mb.MBType, PMBTypeIntra)
	}
}

// encodeUE encodes a value as Exp-Golomb UE into a byte slice (MSB-first).
func encodeUE(v uint) []byte {
	// UE(v): write v+1 in binary with a leading-zero prefix of len(bits)-1
	n := v + 1
	// count bits needed
	bits := 0
	tmp := n
	for tmp > 0 {
		bits++
		tmp >>= 1
	}
	// total bits = 2*bits - 1
	total := 2*bits - 1
	out := make([]byte, (total+7)/8+1)
	// write leading zeros then the n bits
	// bit position from MSB
	pos := 0
	for i := 0; i < bits-1; i++ { // leading zeros
		pos++
	}
	// write n MSB-first at pos
	for i := bits - 1; i >= 0; i-- {
		bit := (n >> uint(i)) & 1
		byteIdx := pos / 8
		bitIdx := 7 - (pos % 8)
		out[byteIdx] |= byte(bit) << uint(bitIdx)
		pos++
	}
	return out
}
