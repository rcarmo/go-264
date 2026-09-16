package cavlc

import "github.com/rcarmo/go-264/nal"

// runBeforeLookup packs len,run as:
//
//	bits 15..8: code length
//	bits  7..0: run_before
//
// A zero entry means no valid run_before prefix.
var runBeforeLookup [7][1 << 11]uint16

func init() {
	for tableIdx := 0; tableIdx < 7; tableIdx++ {
		buildRunBeforeLookup(tableIdx)
	}
}

func buildRunBeforeLookup(tableIdx int) {
	maxRun := tableIdx + 1
	if tableIdx == 6 {
		maxRun = 15
	}
	for run := 0; run <= maxRun; run++ {
		l := int(runBeforeLen[tableIdx][run])
		if l == 0 || l > 11 {
			continue
		}
		prefix := int(runBeforeBits[tableIdx][run]) << uint(11-l)
		span := 1 << uint(11-l)
		packed := uint16(l<<8 | run)
		for suffix := 0; suffix < span; suffix++ {
			runBeforeLookup[tableIdx][prefix|suffix] = packed
		}
	}
}

func runBeforeTableIndex(zerosLeft int) int {
	tableIdx := zerosLeft - 1
	if tableIdx > 6 {
		return 6
	}
	return tableIdx
}

func decodeRunBeforeLookup(r *nal.Reader, zerosLeft int) (run int, ok bool) {
	if zerosLeft <= 0 || r.BitsLeft() < 11 {
		return 0, false
	}
	entry := runBeforeLookup[runBeforeTableIndex(zerosLeft)][r.PeekBits(11)]
	if entry == 0 {
		return 0, false
	}
	run = int(entry & 0xff)
	maxRun := zerosLeft
	if maxRun > 15 {
		maxRun = 15
	}
	if run > maxRun {
		return 0, false
	}
	l := int(entry >> 8)
	r.SkipBits(l)
	return run, true
}
