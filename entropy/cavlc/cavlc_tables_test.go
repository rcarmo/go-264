package cavlc

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func bitsToReader(bits uint32, n int) *nal.Reader {
	var data [4]byte
	v := bits << uint(32-n)
	data[0] = byte(v >> 24)
	data[1] = byte(v >> 16)
	data[2] = byte(v >> 8)
	data[3] = byte(v)
	return nal.NewReader(data[:])
}

func decodeCoeffTokenFromTableScan(r *nal.Reader, nC int) (int, int) {
	var ctLen *[68]uint8
	var ctBits *[68]uint8
	if nC < 2 {
		ctLen = &ctLen0
		ctBits = &ctBits0
	} else if nC < 4 {
		ctLen = &ctLen1
		ctBits = &ctBits1
	} else if nC < 8 {
		ctLen = &ctLen2
		ctBits = &ctBits2
	} else {
		ctLen = &ctLen3
		ctBits = &ctBits3
	}

	avail := r.BitsLeft()
	peekLen := 16
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0, 0
	}
	bits := r.PeekBits(peekLen)

	bestLen := 0
	bestTC, bestTO := 0, 0
	for tc := 0; tc <= 16; tc++ {
		maxTO := 3
		if tc < maxTO {
			maxTO = tc
		}
		for to := 0; to <= maxTO; to++ {
			idx := tc*4 + to
			cLen := int(ctLen[idx])
			cBits := uint32(ctBits[idx])
			if cLen == 0 || cLen > peekLen {
				continue
			}
			shift := uint(peekLen - cLen)
			if (bits >> shift) == cBits {
				if bestLen == 0 || cLen < bestLen {
					bestLen = cLen
					bestTC = tc
					bestTO = to
				}
			}
		}
	}
	if bestLen > 0 {
		r.ReadBits(bestLen)
		return bestTC, bestTO
	}
	r.ReadBit()
	return 0, 0
}

func TestDecodeLevelPrefixBitsFastPath(t *testing.T) {
	for prefix := 0; prefix < 16; prefix++ {
		codeLen := prefix + 1 + 16
		bits := uint32(1) << uint(16) // prefix zeros, delimiter one, padding suffix bits
		r := bitsToReader(bits, codeLen)
		got := decodeLevelPrefixBits(r)
		if got != prefix {
			t.Fatalf("prefix=%d got=%d", prefix, got)
		}
		if r.Position() != prefix+1 {
			t.Fatalf("prefix=%d position=%d want %d", prefix, r.Position(), prefix+1)
		}
	}
}

func TestDecodeLevelPrefixBitsFallbackLongPrefix(t *testing.T) {
	// 20 zeros without a delimiter must saturate at the defensive prefix cap and
	// consume exactly 20 bits, matching the original bit-at-a-time loop.
	r := bitsToReader(0, 24)
	got := decodeLevelPrefixBits(r)
	if got != 20 {
		t.Fatalf("got prefix=%d want 20", got)
	}
	if r.Position() != 20 {
		t.Fatalf("position=%d want 20", r.Position())
	}
}

func TestCoeffTokenTablesRoundtrip(t *testing.T) {
	for _, tbl := range []struct {
		name string
		lens *[68]uint8
		bits *[68]uint8
		nC   int
	}{
		{"nC0", &ctLen0, &ctBits0, 0},
		{"nC2", &ctLen1, &ctBits1, 2},
		{"nC4", &ctLen2, &ctBits2, 4},
		{"nC8", &ctLen3, &ctBits3, 8},
	} {
		for tc := 0; tc <= 16; tc++ {
			maxTO := 3
			if tc < maxTO {
				maxTO = tc
			}
			for to := 0; to <= maxTO; to++ {
				idx := tc*4 + to
				l := int(tbl.lens[idx])
				if l == 0 {
					continue
				}
				r := bitsToReader(uint32(tbl.bits[idx]), l)
				gotTC, gotTO := decodeCoeffTokenFromTable(r, tbl.nC)
				if gotTC != tc || gotTO != to || r.Position() != l {
					t.Fatalf("%s tc=%d to=%d code=%0*b: got tc=%d to=%d pos=%d want pos=%d",
						tbl.name, tc, to, l, tbl.bits[idx], gotTC, gotTO, r.Position(), l)
				}
			}
		}
	}
}

func TestCoeffTokenLookupMatchesScan(t *testing.T) {
	for _, tbl := range []struct {
		name string
		lens *[68]uint8
		bits *[68]uint8
		nC   int
	}{
		{"nC0", &ctLen0, &ctBits0, 0},
		{"nC2", &ctLen1, &ctBits1, 2},
		{"nC4", &ctLen2, &ctBits2, 4},
		{"nC8", &ctLen3, &ctBits3, 8},
	} {
		for tc := 0; tc <= 16; tc++ {
			maxTO := 3
			if tc < maxTO {
				maxTO = tc
			}
			for to := 0; to <= maxTO; to++ {
				idx := tc*4 + to
				l := int(tbl.lens[idx])
				if l == 0 {
					continue
				}
				for suffix := uint32(0); suffix < 8; suffix++ {
					bits := (uint32(tbl.bits[idx]) << 3) | suffix
					r := bitsToReader(bits, l+3)
					gotTC, gotTO, ok := decodeCoeffTokenLookup(r, tbl.nC)
					if !ok || gotTC != tc || gotTO != to || r.Position() != l {
						t.Fatalf("%s tc=%d to=%d suffix=%03b: got (%d,%d) ok=%v pos=%d want pos=%d",
							tbl.name, tc, to, suffix, gotTC, gotTO, ok, r.Position(), l)
					}
				}
			}
		}
	}
}

func TestCoeffTokenLookupAllPrefixesMatchScan(t *testing.T) {
	for _, nC := range []int{0, 2, 4, 8} {
		for prefix := 0; prefix < 1<<16; prefix++ {
			data := []byte{byte(prefix >> 8), byte(prefix), 0xff}
			fast := nal.NewReader(data)
			scan := nal.NewReader(data)
			gotTC, gotTO, ok := decodeCoeffTokenLookup(fast, nC)
			wantTC, wantTO := decodeCoeffTokenFromTableScan(scan, nC)
			if !ok {
				// Invalid prefixes fall back to scan. Both paths should leave the
				// reader unadvanced for lookup failure; the scanner consumes one
				// fallback bit only after lookup misses.
				if fast.Position() != 0 {
					t.Fatalf("nC=%d prefix=0x%04x lookup miss advanced to %d", nC, prefix, fast.Position())
				}
				continue
			}
			if gotTC != wantTC || gotTO != wantTO || fast.Position() != scan.Position() {
				t.Fatalf("nC=%d prefix=0x%04x: lookup=(%d,%d) pos=%d scan=(%d,%d) pos=%d",
					nC, prefix, gotTC, gotTO, fast.Position(), wantTC, wantTO, scan.Position())
			}
		}
	}
}

func BenchmarkDecodeCoeffTokenFromTable(b *testing.B) {
	data := []byte{0x80, 0x40, 0x20, 0x18, 0xff, 0x55, 0x33, 0x77}
	for i := 0; i < b.N; i++ {
		r := nal.NewReader(data)
		for j := 0; j < 16; j++ {
			_, _ = decodeCoeffTokenFromTable(r, j&7)
		}
	}
}

func TestTotalZerosTablesRoundtrip(t *testing.T) {
	for totalCoeff := 1; totalCoeff < 16; totalCoeff++ {
		tableIdx := totalCoeff - 1
		for totalZeros := 0; totalZeros <= totalZerosMaxVal[tableIdx]; totalZeros++ {
			l := int(totalZerosLen[tableIdx][totalZeros])
			if l == 0 {
				continue
			}
			r := bitsToReader(uint32(totalZerosBits[tableIdx][totalZeros]), l)
			got := DecodeTotalZeros(r, totalCoeff)
			if got != totalZeros || r.Position() != l {
				t.Fatalf("totalCoeff=%d totalZeros=%d code=%0*b: got %d pos=%d want pos=%d",
					totalCoeff, totalZeros, l, totalZerosBits[tableIdx][totalZeros], got, r.Position(), l)
			}
		}
	}
}

func TestChromaDCTablesRoundtrip(t *testing.T) {
	for tc := 0; tc <= 4; tc++ {
		maxTO := 3
		if tc < maxTO {
			maxTO = tc
		}
		for to := 0; to <= maxTO; to++ {
			idx := tc*4 + to
			l := int(chromaDCCoeffTokenLen[idx])
			if l == 0 {
				continue
			}
			r := bitsToReader(uint32(chromaDCCoeffTokenBits[idx]), l)
			gotTC, gotTO := decodeCoeffTokenChromaDCTable(r)
			if gotTC != tc || gotTO != to || r.Position() != l {
				t.Fatalf("chromaDC coeff tc=%d to=%d code=%0*b: got tc=%d to=%d pos=%d want pos=%d",
					tc, to, l, chromaDCCoeffTokenBits[idx], gotTC, gotTO, r.Position(), l)
			}
		}
	}
	for tc := 1; tc < 4; tc++ {
		idx := tc - 1
		for totalZeros := 0; totalZeros < 4; totalZeros++ {
			l := int(chromaDCTotalZerosLen[idx][totalZeros])
			if l == 0 {
				continue
			}
			r := bitsToReader(uint32(chromaDCTotalZerosBits[idx][totalZeros]), l)
			got := decodeChromaDCTotalZerosTable(r, tc)
			if got != totalZeros || r.Position() != l {
				t.Fatalf("chromaDC totalZeros tc=%d z=%d code=%0*b: got %d pos=%d want pos=%d",
					tc, totalZeros, l, chromaDCTotalZerosBits[idx][totalZeros], got, r.Position(), l)
			}
		}
	}
}

func decodeRunBeforeScan(r *nal.Reader, zerosLeft int) int {
	if zerosLeft <= 0 {
		return 0
	}
	tableIdx := zerosLeft - 1
	if tableIdx > 6 {
		tableIdx = 6
	}
	maxRun := zerosLeft
	if maxRun > 15 {
		maxRun = 15
	}
	avail := r.BitsLeft()
	peekLen := 11
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0
	}
	bits := r.PeekBits(peekLen)
	for run := 0; run <= maxRun; run++ {
		cLen := int(runBeforeLen[tableIdx][run])
		cBits := uint32(runBeforeBits[tableIdx][run])
		if cLen == 0 || cLen > peekLen {
			continue
		}
		shift := uint(peekLen - cLen)
		if (bits >> shift) == cBits {
			r.ReadBits(cLen)
			return run
		}
	}
	r.ReadBit()
	return 0
}

func TestRunBeforeLookupAllPrefixesMatchScan(t *testing.T) {
	for _, zerosLeft := range []int{1, 2, 3, 4, 5, 6, 7, 15} {
		for prefix := 0; prefix < 1<<11; prefix++ {
			data := []byte{byte(prefix >> 3), byte(prefix << 5), 0xff}
			fast := nal.NewReader(data)
			scan := nal.NewReader(data)
			got, ok := decodeRunBeforeLookup(fast, zerosLeft)
			want := decodeRunBeforeScan(scan, zerosLeft)
			if !ok {
				if fast.Position() != 0 {
					t.Fatalf("zerosLeft=%d prefix=0x%03x lookup miss advanced to %d", zerosLeft, prefix, fast.Position())
				}
				continue
			}
			if got != want || fast.Position() != scan.Position() {
				t.Fatalf("zerosLeft=%d prefix=0x%03x: lookup=%d pos=%d scan=%d pos=%d",
					zerosLeft, prefix, got, fast.Position(), want, scan.Position())
			}
		}
	}
}

func BenchmarkDecodeRunBefore(b *testing.B) {
	data := []byte{0xff, 0x7f, 0x3f, 0x1f, 0x0f, 0x55, 0xaa, 0x33}
	for i := 0; i < b.N; i++ {
		r := nal.NewReader(data)
		for j := 1; j <= 15; j++ {
			_ = DecodeRunBefore(r, j)
		}
	}
}

func TestRunBeforeTablesRoundtrip(t *testing.T) {
	for zerosLeft := 1; zerosLeft <= 15; zerosLeft++ {
		tableIdx := zerosLeft - 1
		if tableIdx > 6 {
			tableIdx = 6
		}
		for run := 0; run <= zerosLeft && run < 16; run++ {
			l := int(runBeforeLen[tableIdx][run])
			if l == 0 {
				continue
			}
			r := bitsToReader(uint32(runBeforeBits[tableIdx][run]), l)
			got := DecodeRunBefore(r, zerosLeft)
			if got != run || r.Position() != l {
				t.Fatalf("zerosLeft=%d run=%d code=%0*b: got %d pos=%d want pos=%d",
					zerosLeft, run, l, runBeforeBits[tableIdx][run], got, r.Position(), l)
			}
		}
	}
}

func TestVLCTableAdvanceSkipsEmulationPrevention(t *testing.T) {
	// Start three bits before an emulation-prevention byte. The RBSP bits seen by
	// DecodeRunBefore are 0001 (zerosLeft=7, run_before=7): three zero bits at
	// the end of byte 1, then byte 2 (0x03) must be skipped and the one bit is
	// read from byte 3. Table decoders must advance by reading bits, not by raw
	// Seek(pos+n), otherwise they can land inside the 0x03 EBSP byte.
	r := nal.NewReader([]byte{0x00, 0x00, 0x03, 0x80})
	r.Seek(13)
	got := DecodeRunBefore(r, 7)
	if got != 7 {
		t.Fatalf("DecodeRunBefore across emulation-prevention byte = %d, want 7", got)
	}
	if pos := r.Position(); pos != 25 {
		t.Fatalf("reader position after VLC across emulation-prevention byte = %d, want 25", pos)
	}
}
