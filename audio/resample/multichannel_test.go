package resample

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// multichannelInput gives every channel its own tone and puts an impulse in
// channel 1 so a channel-stride mistake cannot cancel out.
func multichannelInput(frames, channels, rate int) []float64 {
	data := make([]float64, frames*channels)
	for c := 0; c < channels; c++ {
		hz := 300 + 700*float64(c)
		for i := 0; i < frames; i++ {
			data[i*channels+c] = 0.4*math.Sin(2*math.Pi*hz*float64(i)/float64(rate)) + float64((i+5*c)%23-11)/64
		}
	}
	data[(frames/2)*channels+1] = 1
	return data
}

func channelPlane(data []float64, channels, c int) []float64 {
	plane := make([]float64, len(data)/channels)
	for i := range plane {
		plane[i] = data[i*channels+c]
	}
	return plane
}

func TestMultichannelMatchesPerChannelMono(t *testing.T) {
	const frames = 6007
	for _, rates := range [][2]int{{44100, 48000}, {48000, 16000}, {48000, 44100}, {22050, 48000}} {
		for _, channels := range []int{3, 6, 8} {
			t.Run(fmt.Sprintf("%d-%d/ch%d", rates[0], rates[1], channels), func(t *testing.T) {
				input := multichannelInput(frames, channels, rates[0])
				mono := make([][]float64, channels)
				for c := range mono {
					r, err := New(&mem{data: channelPlane(input, channels, c), rate: rates[0]}, rates[1])
					if err != nil {
						t.Fatal(err)
					}
					mono[c] = renderChannels(t, r, 4096)
				}
				for _, chunk := range []int{1, 7, 4096} {
					r, err := New(&interleavedMem{data: input, rate: rates[0], channels: channels}, rates[1])
					if err != nil {
						t.Fatal(err)
					}
					if r.Info().Channels != channels {
						t.Fatalf("channels %d", r.Info().Channels)
					}
					got := renderChannels(t, r, chunk)
					outFrames := r.Info().Frames
					want, _ := pcm.OutputFrames(frames, rates[0], rates[1])
					if outFrames != want || int64(len(got)) != outFrames*int64(channels) {
						t.Fatalf("chunk %d: frames info=%d want=%d samples=%d", chunk, outFrames, want, len(got))
					}
					for c := 0; c < channels; c++ {
						if int64(len(mono[c])) != outFrames {
							t.Fatalf("mono frames %d want %d", len(mono[c]), outFrames)
						}
						for i := range mono[c] {
							if v := got[i*channels+c]; v != mono[c][i] {
								t.Fatalf("chunk %d channel %d frame %d: %v != mono %v", chunk, c, i, v, mono[c][i])
							}
						}
					}
				}
			})
		}
	}
}

func TestMultichannelSeekMatchesSequential(t *testing.T) {
	const channels = 6
	for _, rates := range [][2]int{{44100, 48000}, {48000, 16000}} {
		t.Run(fmt.Sprintf("%d-%d", rates[0], rates[1]), func(t *testing.T) {
			input := multichannelInput(9001, channels, rates[0])
			r, err := New(&interleavedMem{data: input, rate: rates[0], channels: channels}, rates[1])
			if err != nil {
				t.Fatal(err)
			}
			full := renderChannels(t, r, 1601)
			total := r.Info().Frames
			for _, off := range []int64{0, 1, 127, total / 3, total - 1, total} {
				for _, chunk := range []int{7, 1601} {
					if err := r.SeekFrame(context.Background(), off); err != nil {
						t.Fatal(err)
					}
					got := renderChannels(t, r, chunk)
					want := full[off*channels:]
					if len(got) != len(want) {
						t.Fatalf("offset %d chunk %d: %d samples want %d", off, chunk, len(got), len(want))
					}
					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("offset %d chunk %d sample %d: %v != %v", off, chunk, i, got[i], want[i])
						}
					}
				}
			}
		})
	}
}

func TestMultichannelChannelBounds(t *testing.T) {
	for _, channels := range []int{0, pcm.MaxChannels + 1} {
		src := &interleavedMem{data: make([]float64, 64), rate: 48000, channels: max(channels, 1)}
		_, err := New(fixedInfo{src, pcm.Info{SampleRate: 48000, Channels: channels, Frames: 8}}, 16000)
		if !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("channels %d: %v", channels, err)
		}
	}
	src := &interleavedMem{data: make([]float64, 64*pcm.MaxChannels), rate: 48000, channels: pcm.MaxChannels}
	r, err := New(src, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Info().Channels != pcm.MaxChannels {
		t.Fatal(r.Info())
	}
}

func TestMultichannelSameRatePassthrough(t *testing.T) {
	const channels = 6
	input := multichannelInput(5000, channels, 48000)
	r, err := New(&interleavedMem{data: input, rate: 48000, channels: channels}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if info := r.Info(); info.Channels != channels || info.Frames != 5000 || info.SampleRate != 48000 {
		t.Fatal(info)
	}
	got := renderChannels(t, r, 333)
	if len(got) != len(input) {
		t.Fatal(len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Fatalf("sample %d: %v != %v", i, got[i], input[i])
		}
	}
}

// fixedInfo reports an arbitrary layout so New's validation can be probed
// with channel counts no real interleaved buffer can represent.
type fixedInfo struct {
	*interleavedMem
	info pcm.Info
}

func (f fixedInfo) Info() pcm.Info { return f.info }
