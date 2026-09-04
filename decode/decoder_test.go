package decode

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func appendTestNAL(dst []byte, unit nal.Unit) []byte {
	dst = append(dst, 0, 0, 0, 1, unit.RefIDC<<5|unit.Type)
	return append(dst, unit.Payload...)
}

// SPS, PPS and one 32x16 IDR picture, encoded from synthetic testsrc2 at QP 25.
// These small wire vectors exercise error paths; no pixel oracle or external
// conformance corpus is required by the decoder unit tests.
var decoderSyntaxVectors = []struct{ name, hex string }{
	{"cavlc", "000000016742c00adcbb0110000003001000000300a0f122780000000168ce1cb2000000016588843fc4222a00c0843f0045361908000200" +
		"75a8b2c8b280009417a29286114de17d73ffc1c10605320e0830732f352191944b5c2770ae227867420949c2d267ff8efe76098b9182d62d" +
		"847ffbd60c9d2d15134768afff76a1ec70653d63f87abd6e26830e852446cf57a4000798c66c70055bfbff68078a42a7280c8dfef8100008" +
		"0700020050a8410071c38001bb0ca344a945f0c716ec50f27e802928306247fe56058947fe38017430d5348d2002346b9a136672d220c54f" +
		"a30b812ce083e9995c0222a42bfbd8c0739ab859376bffbb1301100daed4e5420002056c805d320660e556ae0c898294a691a4ecb67fff82" +
		"10381c1060e65002617a71794ca5f1ab3bffd64834c28cf27d2f23d5e0531a73388f512a3d5ec00ca8d022a286ddf8fdf3811484a9af620e" +
		"a007f298e998d8e5c5e702cb340ec7413066701149e6348620eab036f85e9a2bc543fbe0820002010e000101608803c2ec4620db1097383a" +
		"2db5bc11b5fd008e6d022288b6462822929fdffec06d8e53a900f93657efb8e66116dce109eb6f1e37c1b5d65d0753673035c547b5c0b315" +
		"ffae04573312e38c9898e8"},
	{"cabac", "00000001674d400adcbb0110000003001000000300a0f122780000000168ee1cb2000000016588843fb54f28fac06a72a15a1b8a7085b888" +
		"c29ebc147379d6386e38b5cfde6030b5bb0cd24c2dd6f8d4ae75cca840a3122681007977070fca86aa1d5f3be9db2c44a27bdf7933adfd00" +
		"c22782c5aaad2703fcccf4432652d8ebb26a54779eb686593ad4a12577e08e36c928774befbb633f08af5ecec69f62e59637c61cd49a0282" +
		"b1ba6ac20c75240ac47c3065800d3104809fc81db8afa6026f39cccb8075fa3692a4eef871e7d1fcaddfed428e5a07359e561194489a0518" +
		"0d89aadc9a227f84b335dd29b62ae5995970fa6d85eafd9305da60d9df7e304061f9ba263a120b064deb3cab966e21e9de3dcc6c83878b03" +
		"3924842d0d02524fe7a103507d1b61e6c978fb7d0bc5255e767d55539e5c308d29078e8fea293b3f084ba6b29c1e8a6e602e998720345524" +
		"44916545932f6231401b5bc91386bb8fc991d81b8c43efd0e8ffbd5a66e41bd484d75a6d411655cd426903407d80b4a4db041d3902ca838d" +
		"0aa473fe1aa766bdbd14480209d3b0409b8d0805c60b195e9d46c8fd140eeac8f729774869ef18b9a7476e59b3dfc51d69900ee7488c0c56" +
		"581015ef"},
	{"high", "000000016764100aacb976022000000300200000030140800000000168ee1cb22c000000016588843f5de7947d60353950ad0dc53842dc44" +
		"614f5e0a39bceb1c371c5ae7ef30185add86692616eb7c6a573ae6542051891340803cbb8387e543550eaf9df4ed9622513defbc99d6fe80" +
		"6113c162d5569381fe667a2193296c75d9352a3bcf5b432c9d6a5092bbf0471b64943ba5f7ddb19f8457af67634fb172cb1be30e6a4d0141" +
		"58dd3561063a9205623e1832c0069882404fe40edc57d301379ce665c03afd1b4952777c38f3e8fe56eff6a1472d039acf2b08ca244d028c" +
		"06c4d56e4d113fc2599aee94db1572ccacb87d36c2f57ec97c56983677df8c10187e6e898e8482c1937acf2ae59b887a778f731b20e1e2c0" +
		"ce49210b43409493f9e840d41f46d879b25e3edf42f149579d9f5554e7970c234a41e3a3fa8a4ecfc212e9aca707a29b980ba661c80d1549" +
		"1124595164cbd88c5006d6f244e1aee3f2647606e310fbf43a3fef5699b906f52135d69b50459573509a40d01f602d2936c1074e40b2a0e3" +
		"42a91cff86a9d9af6f4512008274ec1026e342017182c657a751b23f4503bab23dca5dd21a7bc62e69d1db966cf7f1475a6403b9d2230315" +
		"9604057bc1"},
}

func syntaxTestInput(t testing.TB, name string) []byte {
	t.Helper()
	for _, vector := range decoderSyntaxVectors {
		if vector.name == name {
			data, err := hex.DecodeString(vector.hex)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("unknown syntax vector %q", name)
	return nil
}

func firstSyntaxTestSlice(t testing.TB, name string) ([]byte, nal.Unit) {
	t.Helper()
	units, err := nal.SplitNALUnitsChecked(syntaxTestInput(t, name))
	if err != nil {
		t.Fatal(err)
	}
	var prefix []byte
	for _, unit := range units {
		if unit.Type == nal.TypeSliceIDR {
			return prefix, unit
		}
		prefix = appendTestNAL(prefix, unit)
	}
	t.Fatal("missing IDR in syntax vector")
	return nil, nal.Unit{}
}

func TestDecoderRejectsTruncatedSlices(t *testing.T) {
	for _, name := range []string{"cavlc", "cabac", "high"} {
		prefix, unit := firstSyntaxTestSlice(t, name)
		// SplitNALUnitsChecked has already removed optional trailing zero bytes.
		// These cuts remove actual non-padding slice bytes, including CABAC tails.
		for cut := 1; cut <= 16; cut++ {
			t.Run(fmt.Sprintf("%s/tail-%d", name, cut), func(t *testing.T) {
				d := NewDecoder()
				if _, err := d.Decode(prefix); err != nil {
					t.Fatal(err)
				}
				short := unit
				short.Payload = unit.Payload[:len(unit.Payload)-cut]
				frames, err := d.Decode(appendTestNAL(nil, short))
				if err == nil || len(frames) != 0 || len(d.Frames) != 0 || len(d.DPB.Frames) != 0 {
					t.Fatalf("partial picture escaped: frames=%d history=%d refs=%d err=%v", len(frames), len(d.Frames), len(d.DPB.Frames), err)
				}
			})
		}
	}
}

func TestDecoderRejectsSIAsUnsupported(t *testing.T) {
	d := pcmTestDecoder(16)
	d.SPS[0].ProfileIDC = 88 // Extended profile permits SI slices.
	d.SPS[0].ConstraintFlags = 0
	d.PPS[0].DeblockingFilterControl = false
	d.PPS[0].PicInitQS = 26
	// A complete SI IDR header: first_mb=0, slice_type=4, PPS=0, frame_num=0,
	// idr_pic_id=0, marking flags=00, QP delta=0 and QS delta=0. The body is
	// deliberately unexamined: SI reconstruction is unsupported, not bad syntax.
	unit := nal.Unit{Type: nal.TypeSliceIDR, RefIDC: 3, Payload: []byte{0x96, 0x13, 0x80}}
	frames, err := d.Decode(appendTestNAL(nil, unit))
	if err == nil || !strings.Contains(err.Error(), "unsupported SP/SI slice") || len(frames) != 0 {
		t.Fatalf("SI picture: frames=%d err=%v", len(frames), err)
	}
}

func TestDecoderCodedAllocationBudget(t *testing.T) {
	prefix, unit := firstSyntaxTestSlice(t, "cavlc")
	d := NewDecoder()
	if _, err := d.Decode(prefix); err != nil {
		t.Fatal(err)
	}
	d.MaxFrameMacroblocks = 1
	if _, err := d.Decode(appendTestNAL(nil, unit)); err == nil || !strings.Contains(err.Error(), "allocation budget") {
		t.Fatalf("picture over budget: %v", err)
	}
	if d.intraModes != nil || d.mbW != 0 || len(d.DPB.Frames) != 0 {
		t.Fatal("budget checked after picture state allocation")
	}
	// The same complete coded picture is accepted at its exact 2x1-MB budget.
	d.MaxFrameMacroblocks = 2
	if frames, err := d.Decode(appendTestNAL(nil, unit)); err != nil || len(frames) != 1 {
		t.Fatalf("picture at budget: %v", err)
	}
	t.Run("oversized_axis", func(t *testing.T) {
		// A 1025x1-MB SPS is below the default area budget but exceeds the
		// frame allocator's 16384-pixel axis limit. It must fail before allocation.
		largeSPS := nal.Unit{Type: nal.TypeSPS, RefIDC: 3, Payload: []byte{0x42, 0xc0, 0x0a, 0xda, 0x00, 0x10, 0x07, 0x90}}
		input := appendTestNAL(append([]byte(nil), prefix...), largeSPS)
		d := NewDecoder()
		if _, err := d.Decode(appendTestNAL(input, unit)); err == nil || !strings.Contains(err.Error(), "allocation budget") {
			t.Fatalf("oversized coded axis: %v", err)
		}
		if d.intraModes != nil || d.mbW != 0 {
			t.Fatal("axis limit checked after picture state allocation")
		}
	})
}

func TestDecoderRejectsMalformedIDRWithoutDroppingReferences(t *testing.T) {
	prefix, unit := firstSyntaxTestSlice(t, "cavlc")
	d := NewDecoder()
	if _, err := d.Decode(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Decode(appendTestNAL(nil, unit)); err != nil {
		t.Fatal(err)
	}
	refs := len(d.DPB.Frames)
	bad := unit
	bad.Payload = bad.Payload[:len(bad.Payload)-1]
	if _, err := d.Decode(appendTestNAL(nil, bad)); err == nil {
		t.Fatal("truncated IDR accepted")
	}
	if len(d.DPB.Frames) != refs {
		t.Fatal("malformed IDR flushed references")
	}
}

func TestDecoderIgnoresNonVCLContents(t *testing.T) {
	input := syntaxTestInput(t, "cavlc")
	want, err := NewDecoder().Decode(input)
	if err != nil || len(want) != 1 {
		t.Fatalf("base picture: frames=%d err=%v", len(want), err)
	}
	// These non-VCL types may follow the primary picture. Their contents
	// are intentionally opaque: this decoder reconstructs Annex A pictures,
	// not auxiliary pictures, extension layers or application data.
	for _, typ := range []uint8{0, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31} {
		t.Run(fmt.Sprint(typ), func(t *testing.T) {
			data := appendTestNAL(append([]byte(nil), input...), nal.Unit{Type: typ, Payload: []byte{0x80}})
			got, err := NewDecoder().Decode(data)
			if err != nil || len(got) != 1 {
				t.Fatalf("ignored NAL lost picture: frames=%d err=%v", len(got), err)
			}
			if !bytes.Equal(got[0].Y, want[0].Y) || !bytes.Equal(got[0].U, want[0].U) || !bytes.Equal(got[0].V, want[0].V) {
				t.Fatal("ignored NAL changed reconstructed samples")
			}
		})
	}
}

func TestMultiSliceIgnoresNonDelimitingNALs(t *testing.T) {
	// 7.4.1.2.5 permits these NALs after the first VCL NAL; they do not
	// separate two slices of the same primary picture.
	for _, typ := range []uint8{0, 12, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31} {
		d := assemblyDecoder(2, 1)
		data := assemblyInput(pcmAssemblySlice(0, 81), nal.Unit{Type: typ, Payload: []byte{0x80}}, pcmAssemblySlice(1, 149))
		frames, err := d.Decode(data)
		if err != nil || len(frames) != 1 {
			t.Fatalf("type %d split a primary picture: frames=%d err=%v", typ, len(frames), err)
		}
		if frames[0].PixelY(0, 0) != 81 || frames[0].PixelY(16, 0) != 149 {
			t.Fatalf("type %d lost slice samples", typ)
		}
	}
}

func TestMultiSliceExtensionNALStartsAccessUnit(t *testing.T) {
	// These ignored NALs delimit access units under 7.4.1.2.3. Type 14 is
	// tested separately: SVC prefixes may occur between base-picture slices.
	for _, typ := range []uint8{15, 16, 17, 18} {
		d := assemblyDecoder(2, 1)
		data := assemblyInput(pcmAssemblySlice(0, 81), nal.Unit{Type: typ, RefIDC: 1, Payload: []byte{0x80}}, pcmAssemblySlice(1, 149))
		_, err := d.Decode(data)
		if err == nil || !strings.Contains(err.Error(), "incomplete picture") || len(d.DPB.Frames) != 0 {
			t.Fatalf("type %d stitched incomplete pictures: %v", typ, err)
		}
	}
}

func TestDecodeIDR(t *testing.T) {
	data, err := os.ReadFile("/tmp/test.h264")
	if err != nil {
		t.Skipf("no test bitstream: %v", err)
	}

	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	t.Logf("Decoded %d frames", len(frames))
	if len(frames) == 0 {
		t.Fatal("expected at least 1 frame")
	}

	f := frames[0]
	t.Logf("Frame: %dx%d IDR=%v ref=%v poc=%d",
		f.Width, f.Height, f.IsIDR, f.IsRef, f.POC)

	if f.Width != 320 || f.Height != 240 {
		t.Errorf("frame size %dx%d want 320x240", f.Width, f.Height)
	}

	// Check that frame isn't all zeros (prediction produced something)
	nonZero := 0
	for _, v := range f.Y[:f.Width*f.Height] {
		if v != 0 {
			nonZero++
		}
	}
	t.Logf("Non-zero luma pixels: %d/%d (%.1f%%)",
		nonZero, f.Width*f.Height, float64(nonZero)*100/float64(f.Width*f.Height))
}

func TestDecoderSPSPPS(t *testing.T) {
	data, err := os.ReadFile("/tmp/test.h264")
	if err != nil {
		t.Skipf("no test bitstream: %v", err)
	}

	dec := NewDecoder()
	dec.Decode(data)

	if len(dec.SPS) == 0 {
		t.Fatal("no SPS parsed")
	}
	if len(dec.PPS) == 0 {
		t.Fatal("no PPS parsed")
	}

	for id, sps := range dec.SPS {
		t.Logf("SPS[%d]: profile=%d level=%d %dx%d",
			id, sps.ProfileIDC, sps.LevelIDC, sps.Width, sps.Height)
	}
	for id, pps := range dec.PPS {
		t.Logf("PPS[%d]: sps=%d entropy=%d qp=%d",
			id, pps.SPSID, pps.EntropyCodingMode, pps.PicInitQP)
	}
}

func TestDecodePFrame(t *testing.T) {
	data, err := os.ReadFile("/tmp/test.h264")
	if err != nil {
		t.Skipf("no test bitstream: %v", err)
	}

	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	t.Logf("Decoded %d frames total", len(frames))
	for i, f := range frames {
		t.Logf("  Frame %d: %dx%d IDR=%v ref=%v poc=%d",
			i, f.Width, f.Height, f.IsIDR, f.IsRef, f.POC)
	}

	// Should have at least the IDR frame + P-frames
	if len(frames) < 1 {
		t.Fatal("expected at least 1 frame")
	}
}

func TestDecoderTraceMBReceivesCABACEvents(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/testsrc_cabac_p.h264")
	if err != nil {
		t.Skipf("no CABAC fixture: %v", err)
	}
	dec := NewDecoder()
	seenCABAC := false
	dec.TraceMB = func(ev MBTraceEvent) {
		if ev.EntropyCABAC {
			seenCABAC = true
		}
	}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !seenCABAC {
		t.Fatal("TraceMB did not receive CABAC macroblock events")
	}
}
