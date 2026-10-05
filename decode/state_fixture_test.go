package decode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

type stateFixture struct {
	name, inputSHA, outputSHA string
	frames, width, height     int
	checkSyntax               func(*testing.T, []nal.Unit, []*nal.SPS)
}

func stateFixtureUnits(t *testing.T, data []byte) ([]nal.Unit, []*nal.SPS) {
	t.Helper()
	units, err := nal.SplitNALUnitsChecked(data)
	if err != nil {
		t.Fatal(err)
	}
	var sps []*nal.SPS
	for _, unit := range units {
		if unit.Type != nal.TypeSPS {
			continue
		}
		parsed, err := nal.ParseSPS(unit.Payload)
		if err != nil {
			t.Fatal(err)
		}
		sps = append(sps, parsed)
	}
	return units, sps
}

func hashVisibleFrames(t *testing.T, frames []*frame.Frame) string {
	t.Helper()
	h := sha256.New()
	for _, f := range frames {
		for y := 0; y < f.Height; y++ {
			h.Write(f.Y[y*f.StrideY : y*f.StrideY+f.Width])
		}
		for _, plane := range [][]byte{f.U, f.V} {
			for y := 0; y < f.Height/2; y++ {
				h.Write(plane[y*f.StrideC : y*f.StrideC+f.Width/2])
			}
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// TestOfficialReferenceSyntax pins the reference-management operations used by
// the external FFmpeg parity gate. Their media stays outside Git.
func TestOfficialReferenceSyntax(t *testing.T) {
	root := os.Getenv("GO264_CONFORMANCE_ROOT")
	if root == "" {
		root = "/workspace/reports/go-264/fixtures/h264-conformance"
	}
	for _, tc := range []struct {
		name, sha string
		log2      uint32
		ops       map[uint32]int
	}{
		{"MR1_BT_A.h264", "20dc67331c81adcf40048bb37357883a69b3ab002b0927e599f43d86be9c3d8b", 5, map[uint32]int{3: 28, 4: 2}},
		{"MR2_TANDBERG_E.264", "24d95632d3adff1808f391402d2bae4f111dd051c1187b915701c2201f995254", 8, map[uint32]int{2: 31, 6: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv("GO264_PHASE4_REGRESSION") != "1" {
				t.Skip("set GO264_PHASE4_REGRESSION=1 to run external conformance syntax checks")
			}
			data, err := os.ReadFile(root + "/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != tc.sha {
				t.Fatalf("input SHA-256=%s want %s", got, tc.sha)
			}
			units, sps := stateFixtureUnits(t, data)
			if len(sps) == 0 {
				t.Fatal("no SPS")
			}
			for _, s := range sps {
				if !s.FrameMbsOnlyFlag || s.ChromaFormatIDC != 1 || s.BitDepthLuma != 8 || s.BitDepthChroma != 8 || s.Log2MaxFrameNum != tc.log2 {
					t.Fatalf("SPS=%+v, want progressive 8-bit 4:2:0 MaxPicNum=%d", s, 1<<tc.log2)
				}
			}
			pps := make(map[uint32]*nal.PPS)
			seen := make(map[uint32]int)
			for _, unit := range units {
				switch unit.Type {
				case nal.TypePPS:
					p, err := nal.ParsePPS(unit.Payload)
					if err != nil {
						t.Fatal(err)
					}
					pps[p.PPSID] = p
				case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
					reader := nal.NewReader(unit.Payload)
					reader.ReadUE() // first_mb_in_slice
					reader.ReadUE() // slice_type
					p := pps[reader.ReadUE()]
					if p == nil {
						t.Fatal("slice PPS not found")
					}
					var s *nal.SPS
					for _, candidate := range sps {
						if candidate.SPSID == p.SPSID {
							s = candidate
						}
					}
					if s == nil {
						t.Fatal("slice SPS not found")
					}
					h, r := syntax.ParseHeaderWithRefIDC(unit.Payload, unit.Type, unit.RefIDC, s, p)
					if err := r.Err(); err != nil {
						t.Fatal(err)
					}
					for _, op := range h.MemoryManagementControls {
						seen[op.Op]++
					}
				}
			}
			for op, want := range tc.ops {
				if seen[op] != want {
					t.Fatalf("MMCO%d count=%d want %d", op, seen[op], want)
				}
			}
		})
	}
}

func TestProgressiveStateFixturesExact(t *testing.T) {
	fixtures := []stateFixture{
		{
			name: "multiple-idr", inputSHA: "10df23d24c6b6fba14eaabb9c88bbeda945a188127620ea4dd2a52279eff7b20",
			outputSHA: "b3812a3161122dbb29c09f7b3ea03a64e40dcd7f603b272aa6670258f63337f2", frames: 12, width: 32, height: 32,
			checkSyntax: func(t *testing.T, units []nal.Unit, sps []*nal.SPS) {
				idr := 0
				for _, unit := range units {
					if unit.Type == nal.TypeSliceIDR {
						idr++
					}
				}
				if idr != 3 || len(sps) != 3 {
					t.Fatalf("IDR=%d SPS=%d, want three GOP epochs", idr, len(sps))
				}
			},
		},
		{
			name: "frame-poc-wrap", inputSHA: "e65a9916f881419179b11419e0c47a76ded1a8c50aaacfcfef2fbb9253a5c3eb",
			outputSHA: "fab056f5fd7c55263edbb56e6409704720587d7900c2fb92daa56375bad019e7", frames: 40, width: 32, height: 32,
			checkSyntax: func(t *testing.T, _ []nal.Unit, sps []*nal.SPS) {
				if len(sps) != 1 || sps[0].Log2MaxFrameNum != 4 || sps[0].PicOrderCntType != 2 {
					t.Fatalf("SPS=%+v, want type-2 POC and MaxPicNum 16", sps)
				}
			},
		},
		{
			name: "coded-edge-crop", inputSHA: "0f3b2671b56b25052bde9c92542720bb99d3cdbf7398d018cc82e1c2ac418060",
			outputSHA: "567411f084131c6f5a97b24e6d0be9befc9534476b96879461b67b750c6240c7", frames: 5, width: 30, height: 22,
			checkSyntax: func(t *testing.T, _ []nal.Unit, sps []*nal.SPS) {
				if len(sps) != 1 || !sps[0].FrameCropping || sps[0].PicWidthInMbs != 2 || sps[0].PicHeightInMapUnits != 2 || sps[0].CropRight != 1 || sps[0].CropBottom != 5 {
					t.Fatalf("SPS=%+v, want coded 32x32 cropped to 30x22", sps)
				}
			},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/state/" + fixture.name + ".h264")
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != fixture.inputSHA {
				t.Fatalf("input SHA256=%s want %s", got, fixture.inputSHA)
			}
			units, sps := stateFixtureUnits(t, data)
			fixture.checkSyntax(t, units, sps)

			var frames []*frame.Frame
			stream, err := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error {
				frames = append(frames, f)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Push(data); err != nil {
				t.Fatal(err)
			}
			if err := stream.Drain(); err != nil {
				t.Fatal(err)
			}
			if len(frames) != fixture.frames {
				t.Fatalf("frames=%d want %d", len(frames), fixture.frames)
			}
			for i, f := range frames {
				if f.Width != fixture.width || f.Height != fixture.height {
					t.Fatalf("frame %d dimensions=%dx%d want %dx%d", i, f.Width, f.Height, fixture.width, fixture.height)
				}
			}
			if fixture.name == "frame-poc-wrap" && (frames[15].FrameNum != 15 || frames[16].FrameNum != 0 || frames[32].FrameNum != 0) {
				t.Fatalf("frame_num did not wrap twice: 15=%d 16=%d 32=%d", frames[15].FrameNum, frames[16].FrameNum, frames[32].FrameNum)
			}
			if got := hashVisibleFrames(t, frames); got != fixture.outputSHA {
				t.Fatalf("visible YUV SHA256=%s want %s (FFmpeg 8.1.2)", got, fixture.outputSHA)
			}
		})
	}
}
