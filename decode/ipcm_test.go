package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func TestReconstructIPCMWritesRawSamples(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBIntra{MBType: syntax.MBTypeIPCM}
	for i := range mb.PCMY {
		mb.PCMY[i] = uint8(i)
	}
	for i := range mb.PCMCb {
		mb.PCMCb[i] = uint8(10 + i)
		mb.PCMCr[i] = uint8(100 + i)
	}
	d.reconstructMB(f, mb, 0, 0, 26, nil)
	if f.PixelY(0, 0) != 0 || f.PixelY(15, 15) != 255 || f.PixelU(0, 0) != 10 || f.PixelU(7, 7) != 73 || f.PixelV(0, 0) != 100 || f.PixelV(7, 7) != 163 {
		t.Fatalf("PCM reconstruction mismatch")
	}
}

func pcmTestDecoder(width int) *Decoder {
	d := NewDecoder()
	d.SPS[0] = &nal.SPS{
		ProfileIDC: 66, ConstraintFlags: 0xc0, LevelIDC: 10,
		ChromaFormatIDC: 1, BitDepthLuma: 8, BitDepthChroma: 8,
		FrameMbsOnlyFlag: true, Log2MaxFrameNum: 4, PicOrderCntType: 2,
		MaxNumRefFrames: 1, PicWidthInMbs: uint32(width / 16), PicHeightInMapUnits: 1,
		Width: width, Height: 16,
	}
	d.PPS[0] = &nal.PPS{PicInitQP: 26, NumSliceGroups: 1,
		NumRefIdxL0Active: 1, NumRefIdxL1Active: 1, DeblockingFilterControl: true}
	return d
}

func TestCAVLCIPCMUsesZeroDeblockingQP(t *testing.T) {
	t.Setenv("GO264_DISABLE_DEBLOCK", "")
	for _, tc := range []struct {
		name   string
		typ    uint8
		prefix []byte
	}{
		// Slice QP 26, filtering enabled with zero offsets, I_PCM followed
		// by pcm_alignment_zero_bits. P slices also carry mb_skip_run=0.
		{"I", nal.TypeSliceIDR, []byte{0xb8, 0x4f, 0x0d, 0x00}},
		{"P", nal.TypeSliceNonIDR, []byte{0xe2, 0x3e, 0x1f}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pcmTestDecoder(16)
			if tc.typ == nal.TypeSliceNonIDR {
				ref := frame.NewFrame(16, 16)
				ref.IsRef = true
				d.DPB.Add(ref)
			}
			var samples []byte
			for _, size := range []int{16, 8, 8} {
				for y := 0; y < size; y++ {
					for x := 0; x < size; x++ {
						samples = append(samples, byte(100+4*((x/4+y/4)%2)))
					}
				}
			}
			payload := append(append(tc.prefix, samples...), 0x80) // rbsp_trailing_bits
			f, err := d.decodeSlice(nal.Unit{Type: tc.typ, RefIDC: 3, Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			// With PCM's filter QP 0, all three raw planes stay unchanged.
			// Slice QP 26 would blur the deliberately small block-edge steps.
			i := 0
			for _, plane := range [][]byte{f.Y, f.U, f.V} {
				for _, sample := range plane {
					if sample != samples[i] {
						t.Fatalf("sample %d=%d, want raw PCM %d", i, sample, samples[i])
					}
					i++
				}
			}
		})
	}
}

func TestCAVLCIPCMNeighborAndRunningQP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		suffix []byte
		wantY  byte
	}{
		// I16x16 DC prediction, QP delta 0, nC=16 luma coeff_token,
		// then rbsp_trailing_bits. The +1 DC level reconstructs to +1 at
		// QP 26, but to zero if the preceding PCM wrongly resets slice QP.
		{"zero residual", []byte{0x26, 0x1c}, 100},
		{"preserve QP", []byte{0x26, 0x0b}, 101},
		// I16x16 with CBP chroma=2: zero chroma DC and AC blocks. Each
		// component's left-edge AC blocks use nC=16 and nC=8, not nC=0.
		{"chroma contexts", []byte{0x19, 0x86, 0xa1, 0xc3, 0x87, 0x0f}, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pcmTestDecoder(32)
			// IDR I slice, filtering disabled, followed by the first I_PCM MB.
			payload := []byte{0xb8, 0x4a, 0x0d, 0x00}
			for i := 0; i < 384; i++ {
				v := byte(100)
				if i >= 256 {
					v = 128
				}
				payload = append(payload, v)
			}
			payload = append(payload, tc.suffix...)
			f, err := d.decodeSlice(nal.Unit{Type: nal.TypeSliceIDR, RefIDC: 3, Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < 16; y++ {
				for x := 0; x < 32; x++ {
					want := byte(100)
					if x >= 16 {
						want = tc.wantY
					}
					if got := f.PixelY(x, y); got != want {
						t.Fatalf("luma (%d,%d)=%d, want %d", x, y, got, want)
					}
				}
			}
			for _, plane := range [][]byte{f.U, f.V} {
				for i, v := range plane {
					if v != 128 {
						t.Fatalf("chroma sample %d=%d, want 128", i, v)
					}
				}
			}
		})
	}
}
