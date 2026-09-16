package decode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
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
