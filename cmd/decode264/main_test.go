package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/decode"
)

func TestOrderFramesForOutputUsesFullPOCAcrossWrap(t *testing.T) {
	// Decode order around a max_pic_order_cnt_lsb=64 wrap. Compact POC returns
	// to zero, while FullPOC remains monotonic in presentation order.
	fullPOCs := []int{0, 60, 56, 54, 58, 68, 64, 62, 66, 76, 72, 70, 74}
	frames := make([]*decode.DecodedFrame, len(fullPOCs))
	for i, fullPOC := range fullPOCs {
		frames[i] = &decode.DecodedFrame{POC: fullPOC & 63, FullPOC: fullPOC}
	}
	frames[0].IsIDR = true

	gotFrames := orderFramesForOutput(frames)
	got := make([]int, len(gotFrames))
	for i, frame := range gotFrames {
		got[i] = frame.FullPOC
	}
	want := []int{0, 54, 56, 58, 60, 62, 64, 66, 68, 70, 72, 74, 76}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FullPOC order=%v want %v", got, want)
	}
}

func TestOrderFramesForOutputKeepsIDRGOPsSeparate(t *testing.T) {
	frames := []*decode.DecodedFrame{
		{FullPOC: 0, IsIDR: true}, {FullPOC: 6}, {FullPOC: 2}, {FullPOC: 4},
		{FullPOC: 0, IsIDR: true}, {FullPOC: 6}, {FullPOC: 2}, {FullPOC: 4},
	}

	gotFrames := orderFramesForOutput(frames)
	got := make([]int, len(gotFrames))
	for i, frame := range gotFrames {
		got[i] = frame.FullPOC
	}
	want := []int{0, 2, 4, 6, 0, 2, 4, 6}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FullPOC order=%v want %v", got, want)
	}
}

// TestFFmpegReferenceParityBBB is the durable, opt-in oracle regression for
// display ordering and visible YUV420 output. It is deliberately excluded from
// ordinary unit runs because it needs the pinned external fixture and FFmpeg
// 7.1.3 and decodes 300 640x360 frames.
//
// Run with:
//
//	GO264_FFMPEG_REGRESSION=1 \
//	GO264_FFMPEG_BIN=/workspace/tmp/ffmpeg-7.1.3/ffmpeg \
//	GO264_BBB_FIXTURE=/workspace/tmp/bbb_annexb.h264 \
//	go test ./cmd/decode264 -run TestFFmpegReferenceParityBBB -count=1
func TestFFmpegReferenceParityBBB(t *testing.T) {
	if os.Getenv("GO264_FFMPEG_REGRESSION") != "1" {
		t.Skip("set GO264_FFMPEG_REGRESSION=1 to run the FFmpeg oracle regression")
	}

	const (
		frameCount = 300
		width      = 640
		height     = 360
		fixtureSHA = "1305bc99a369721c46e35e3af8cc3e5f893f653eb6f472830bc70f6fcf3841ff"
	)
	fixture := os.Getenv("GO264_BBB_FIXTURE")
	if fixture == "" {
		fixture = "/workspace/tmp/bbb_annexb.h264"
	}
	stream, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(stream)); got != fixtureSHA {
		t.Fatalf("fixture SHA-256=%s want %s", got, fixtureSHA)
	}

	ffmpeg := os.Getenv("GO264_FFMPEG_BIN")
	if ffmpeg == "" {
		ffmpeg = "/workspace/tmp/ffmpeg-7.1.3/ffmpeg"
	}
	version, err := exec.Command(ffmpeg, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("run %s -version: %v", ffmpeg, err)
	}
	if firstLine := strings.SplitN(string(version), "\n", 2)[0]; !strings.Contains(firstLine, "ffmpeg version 7.1.3") {
		t.Fatalf("oracle must be FFmpeg 7.1.3, got %q", firstLine)
	}

	referencePath := filepath.Join(t.TempDir(), "bbb-ffmpeg-7.1.3.yuv")
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-threads", "1",
		"-i", fixture, "-frames:v", fmt.Sprint(frameCount),
		"-pix_fmt", "yuv420p", "-f", "rawvideo", "-y", referencePath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate FFmpeg reference: %v\n%s", err, output)
	}
	reference, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read FFmpeg reference: %v", err)
	}
	frameSize := width * height * 3 / 2
	if got, want := len(reference), frameCount*frameSize; got != want {
		t.Fatalf("FFmpeg reference size=%d want %d", got, want)
	}

	dec := decode.NewDecoder()
	frames, err := dec.Decode(stream)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	frames = orderFramesForOutput(frames)
	if len(frames) != frameCount {
		t.Fatalf("display-order frames=%d want %d", len(frames), frameCount)
	}

	type plane struct {
		name          string
		data          func(*decode.DecodedFrame) []byte
		stride, w, h  int
		referenceBase int
	}
	planes := []plane{
		{name: "Y", data: func(f *decode.DecodedFrame) []byte { return f.Y }, stride: width, w: width, h: height},
		{name: "U", data: func(f *decode.DecodedFrame) []byte { return f.U }, stride: width / 2, w: width / 2, h: height / 2, referenceBase: width * height},
		{name: "V", data: func(f *decode.DecodedFrame) []byte { return f.V }, stride: width / 2, w: width / 2, h: height / 2, referenceBase: width * height * 5 / 4},
	}
	for frameIndex, frame := range frames {
		if frame.Width != width || frame.Height != height {
			t.Fatalf("frame %d dimensions=%dx%d want %dx%d", frameIndex, frame.Width, frame.Height, width, height)
		}
		for _, p := range planes {
			gotPlane := p.data(frame)
			gotStride := frame.StrideY
			mbSpan := 16
			if p.name != "Y" {
				gotStride = frame.StrideC
				mbSpan = 8
			}
			for y := 0; y < p.h; y++ {
				gotRow := gotPlane[y*gotStride : y*gotStride+p.w]
				refStart := frameIndex*frameSize + p.referenceBase + y*p.stride
				refRow := reference[refStart : refStart+p.w]
				for x := range gotRow {
					if gotRow[x] != refRow[x] {
						t.Fatalf("first difference frame=%d plane=%s mb=(%d,%d) pixel=(%d,%d) got=%d want=%d", frameIndex, p.name, x/mbSpan, y/mbSpan, x, y, gotRow[x], refRow[x])
					}
				}
			}
		}
	}
}
