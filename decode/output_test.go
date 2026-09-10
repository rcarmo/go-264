package decode

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func TestPictureOutputLimits(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		change                       func(*nal.SPS)
		capacity, buffering, reorder int
		invalid                      bool
	}{
		{name: "720p level3.1", capacity: 5, buffering: 5, reorder: 5},
		{name: "1080p level4", change: func(s *nal.SPS) {
			s.LevelIDC, s.PicWidthInMbs, s.PicHeightInMapUnits, s.MaxNumRefFrames = 40, 120, 68, 4
		}, capacity: 4, buffering: 4, reorder: 4},
		{name: "1080p exceeds level3.1 frame size", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 120, 68
		}, invalid: true},
		{name: "small picture caps DPB at16", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 1, 1
		}, capacity: 16, buffering: 16, reorder: 16},
		{name: "explicit VUI does not shrink output-order DPB", change: func(s *nal.SPS) {
			s.BitstreamRestriction = true
			s.MaxNumReorderFrames, s.MaxDecFrameBuffering, s.MaxNumRefFrames = 2, 3, 3
		}, capacity: 5, buffering: 3, reorder: 2},
		{name: "explicit zero differs from absent VUI", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxNumRefFrames = true, 0
		}, capacity: 5, buffering: 0},
		{name: "High Intra absent VUI inference", change: func(s *nal.SPS) {
			s.ProfileIDC, s.ConstraintFlags, s.MaxNumRefFrames = 100, 0x10, 0
		}, capacity: 5, buffering: 0},
		{name: "CB level1b", change: func(s *nal.SPS) {
			s.LevelIDC, s.ConstraintFlags = 11, 0xd0
			s.PicWidthInMbs, s.PicHeightInMapUnits, s.MaxNumRefFrames = 9, 11, 4
		}, capacity: 4, buffering: 4, reorder: 4},
		{name: "CB level1.1", change: func(s *nal.SPS) {
			s.LevelIDC = 11
			s.PicWidthInMbs, s.PicHeightInMapUnits = 9, 11
		}, capacity: 9, buffering: 9, reorder: 9},
		{name: "references exceed level DPB", change: func(s *nal.SPS) {
			s.MaxNumRefFrames = 6
		}, invalid: true},
		{name: "references exceed VUI buffering", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering = true, 4
		}, invalid: true},
		{name: "VUI buffering exceeds level DPB", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering = true, 6
		}, invalid: true},
		{name: "reordering exceeds VUI buffering", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering, s.MaxNumReorderFrames = true, 5, 6
		}, invalid: true},
		{name: "level dimension bound", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 171, 1
		}, invalid: true},
		{name: "unknown level", change: func(s *nal.SPS) { s.LevelIDC = 0 }, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &nal.SPS{ProfileIDC: 66, ConstraintFlags: 0xc0, LevelIDC: 31,
				FrameMbsOnlyFlag: true, PicWidthInMbs: 80, PicHeightInMapUnits: 45, MaxNumRefFrames: 5}
			if tt.change != nil {
				tt.change(s)
			}
			got, err := pictureOutputLimits(s)
			if tt.invalid {
				if err == nil {
					t.Fatalf("invalid restrictions accepted: %+v", got)
				}
				return
			}
			if err != nil || got.capacity != tt.capacity || got.buffering != tt.buffering || got.reorder != tt.reorder {
				t.Fatalf("limits = %+v, %v; want capacity%d buffering%d reorder%d", got, err, tt.capacity, tt.buffering, tt.reorder)
			}
		})
	}
}
