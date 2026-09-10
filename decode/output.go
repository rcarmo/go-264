package decode

import (
	"fmt"

	"github.com/rcarmo/go-264/nal"
)

type outputLimits struct {
	capacity, buffering, reorder int
	width, height                uint32
}

// Annex A, Table A-1 and E.2.1. These checks bound the output DPB; they do
// not validate bitrate, processing rate, or every other level constraint.
func pictureOutputLimits(s *nal.SPS) (outputLimits, error) {
	level := s.LevelIDC
	if level == 11 && s.ConstraintFlags&0x10 != 0 && (s.ProfileIDC == 66 || s.ProfileIDC == 77 || s.ProfileIDC == 88) {
		level = 9 // Level 1b uses the Level 1 picture and DPB limits.
	}
	var maxFS, maxDPB uint64
	switch level {
	case 9, 10:
		maxFS, maxDPB = 99, 396
	case 11:
		maxFS, maxDPB = 396, 900
	case 12, 13, 20:
		maxFS, maxDPB = 396, 2376
	case 21:
		maxFS, maxDPB = 792, 4752
	case 22, 30:
		maxFS, maxDPB = 1620, 8100
	case 31:
		maxFS, maxDPB = 3600, 18000
	case 32:
		maxFS, maxDPB = 5120, 20480
	case 40, 41:
		maxFS, maxDPB = 8192, 32768
	case 42:
		maxFS, maxDPB = 8704, 34816
	case 50:
		maxFS, maxDPB = 22080, 110400
	case 51, 52:
		maxFS, maxDPB = 36864, 184320
	case 60, 61, 62:
		maxFS, maxDPB = 139264, 696320
	default:
		return outputLimits{}, fmt.Errorf("unsupported output-order level_idc %d", s.LevelIDC)
	}
	w, h := uint64(s.PicWidthInMbs), uint64(s.PicHeightInMapUnits)
	if !s.FrameMbsOnlyFlag || w == 0 || h == 0 || w*h > maxFS || w*w > maxFS*8 || h*h > maxFS*8 {
		return outputLimits{}, fmt.Errorf("%w: picture exceeds progressive level size limits", nal.ErrInvalidSyntax)
	}
	limit := outputLimits{capacity: int(min(maxDPB/(w*h), 16)), width: s.PicWidthInMbs, height: s.PicHeightInMapUnits}
	reorder, buffering := uint32(limit.capacity), uint32(limit.capacity)
	if s.BitstreamRestriction {
		reorder, buffering = s.MaxNumReorderFrames, s.MaxDecFrameBuffering
	} else if s.ConstraintFlags&0x10 != 0 {
		switch s.ProfileIDC {
		case 44, 86, 100, 110, 122, 244:
			reorder, buffering = 0, 0
		}
	}
	if reorder > buffering || buffering > uint32(limit.capacity) || buffering < s.MaxNumRefFrames {
		return outputLimits{}, fmt.Errorf("%w: inconsistent output DPB restrictions", nal.ErrInvalidSyntax)
	}
	limit.buffering, limit.reorder = int(buffering), int(reorder)
	return limit, nil
}
