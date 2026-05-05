package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/slice"
)

func main() {
	input := flag.String("i", "", "input Annex B H.264 bitstream")
	limit := flag.Int("limit", 64, "maximum macroblocks to trace per slice")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: trace264 -i input.h264 [-limit N]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	if err := trace(data, *limit); err != nil {
		fmt.Fprintf(os.Stderr, "trace: %v\n", err)
		os.Exit(1)
	}
}

func trace(data []byte, limit int) error {
	units := nal.SplitNALUnits(data)
	spsMap := map[uint32]*nal.SPS{}
	ppsMap := map[uint32]*nal.PPS{}
	for nalIdx, unit := range units {
		switch unit.Type {
		case nal.TypeSPS:
			sps, err := nal.ParseSPS(unit.Payload)
			if err != nil {
				return fmt.Errorf("nal %d SPS: %w", nalIdx, err)
			}
			spsMap[sps.SPSID] = sps
			fmt.Printf("nal=%d type=SPS id=%d size=%dx%d mbs=%dx%d\n", nalIdx, sps.SPSID, sps.Width, sps.Height, sps.PicWidthInMbs, sps.PicHeightInMapUnits)
		case nal.TypePPS:
			pps, err := nal.ParsePPS(unit.Payload)
			if err != nil {
				return fmt.Errorf("nal %d PPS: %w", nalIdx, err)
			}
			ppsMap[pps.PPSID] = pps
			fmt.Printf("nal=%d type=PPS id=%d sps=%d entropy=%d initQP=%d refsL0=%d\n", nalIdx, pps.PPSID, pps.SPSID, pps.EntropyCodingMode, pps.PicInitQP, pps.NumRefIdxL0Active)
		case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
			if err := traceSlice(nalIdx, unit, spsMap, ppsMap, limit); err != nil {
				return err
			}
		}
	}
	return nil
}

func traceSlice(nalIdx int, unit nal.Unit, spsMap map[uint32]*nal.SPS, ppsMap map[uint32]*nal.PPS, limit int) error {
	peek := nal.NewReader(unit.Payload)
	_ = peek.ReadUE()
	_ = peek.ReadUE()
	ppsID := peek.ReadUE()
	pps := ppsMap[ppsID]
	if pps == nil {
		return fmt.Errorf("nal %d slice: PPS %d not available", nalIdx, ppsID)
	}
	sps := spsMap[pps.SPSID]
	if sps == nil {
		return fmt.Errorf("nal %d slice: SPS %d not available", nalIdx, pps.SPSID)
	}
	hdr, r := slice.ParseHeader(unit.Payload, unit.Type, sps, pps)
	mbWidth := int(sps.PicWidthInMbs)
	mbHeight := int(sps.PicHeightInMapUnits)
	maxMBs := mbWidth * mbHeight
	if limit > 0 && maxMBs > int(hdr.FirstMbInSlice)+limit {
		maxMBs = int(hdr.FirstMbInSlice) + limit
	}
	fmt.Printf("nal=%d type=%d slice=%d firstMB=%d frame=%d qp=%d payloadBits=%d\n", nalIdx, unit.Type, hdr.SliceType, hdr.FirstMbInSlice, hdr.FrameNum, hdr.QP(pps.PicInitQP), len(unit.Payload)*8)
	currentQP := int(hdr.QP(pps.PicInitQP))
	nzCtx := make([][16]int, mbWidth*mbHeight)
	chromaNZCtx := make([][2][4]int, mbWidth*mbHeight)
	skipRun := 0
	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth
		var leftNZ, topNZ *[16]int
		var leftChromaNZ, topChromaNZ *[2][4]int
		if mbX > 0 {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
		}
		if mbY > 0 {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
		}
		start := r.Position()
		if hdr.IsIntra() {
			mb := slice.DecodeMBIntraCtxFull(r, int32(currentQP), pps.EntropyCodingMode, pps.Transform8x8Mode, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
			currentQP = (currentQP + int(mb.QPDelta)%52 + 52) % 52
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=I:%d cbp=%02x chromaMode=%d qpd=%d qp=%d tc=%v\n", mbIdx, mbX, mbY, start, r.Position(), mb.MBType, mb.CodedBlockPattern, mb.ChromaPredMode, mb.QPDelta, currentQP, mb.TotalCoeff)
			if mb.MBType > slice.MBTypeIPCM || mb.ChromaPredMode > 3 {
				fmt.Printf("  !! invalid intra syntax at mb=%d: mb_type=%d chroma_mode=%d nextBit=%d\n", mbIdx, mb.MBType, mb.ChromaPredMode, r.Position())
				return nil
			}
			continue
		}
		if hdr.SliceType == slice.SliceTypeP && pps.EntropyCodingMode == 0 {
			if skipRun == 0 {
				skipRun = int(r.ReadUE())
			}
			if skipRun > 0 {
				fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=P_SKIP remainingSkip=%d qp=%d\n", mbIdx, mbX, mbY, start, r.Position(), skipRun-1, currentQP)
				skipRun--
				continue
			}
		}
		mb := slice.DecodeMBInterCtxFull(r, int32(currentQP), hdr.NumRefIdxL0Active, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
		currentQP = (currentQP + int(mb.QPDelta)%52 + 52) % 52
		nzCtx[mbIdx] = mb.TotalCoeff
		chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
		fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=P:%d cbp=%02x qpd=%d qp=%d mv0=(%d,%d) tc=%v\n", mbIdx, mbX, mbY, start, r.Position(), mb.MBType, mb.CBP, mb.QPDelta, currentQP, mb.MV[0].X, mb.MV[0].Y, mb.TotalCoeff)
	}
	return nil
}
