package audio_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rcarmo/go-264/audio"
)

type profileCase struct {
	name, env      string
	rate, channels int
}

var profileCases = []profileCase{
	{name: "aac-source-stereo", env: "GO264_PROFILE_M4A", rate: 48000, channels: 2},
	{name: "aac-canonical-mono", env: "GO264_PROFILE_M4A", rate: 16000, channels: 1},
	{name: "wav-source-stereo", env: "GO264_PROFILE_WAV", rate: 48000, channels: 2},
	{name: "wav-canonical-mono", env: "GO264_PROFILE_WAV", rate: 16000, channels: 1},
}

func profileFixtures(b *testing.B, env string) [][]byte {
	b.Helper()
	paths := filepath.SplitList(os.Getenv(env))
	if len(paths) == 0 {
		b.Skipf("set %s to immutable fixture paths", env)
	}
	fixtures := make([][]byte, len(paths))
	for i, path := range paths {
		if path == "" {
			b.Fatalf("%s contains an empty fixture path", env)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		fixtures[i] = data
	}
	return fixtures
}

// BenchmarkProfileDecode measures complete open-and-decode operations over
// immutable retained media. It never generates fixtures or invokes FFmpeg.
func BenchmarkProfileDecode(b *testing.B) {
	for _, tc := range profileCases {
		b.Run(tc.name, func(b *testing.B) {
			fixtures := profileFixtures(b, tc.env)
			ctx := context.Background()
			buf := make([]int16, 4096)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, data := range fixtures {
					d, err := audio.Open(ctx, bytes.NewReader(data), int64(len(data)), audio.Options{TargetRate: tc.rate, TargetChannels: tc.channels})
					if err != nil {
						b.Fatal(err)
					}
					count := 0
					for {
						n, _, err := d.ReadPCM(ctx, buf)
						count += n
						if err == io.EOF {
							break
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					if err := d.Close(); err != nil {
						b.Fatal(err)
					}
					if count == 0 {
						b.Fatal("empty decode")
					}
				}
			}
		})
	}
}

// BenchmarkProfileOpen isolates container/configuration allocation from steady
// decode work. It opens and closes each retained fixture without reading PCM.
func BenchmarkProfileOpen(b *testing.B) {
	for _, tc := range profileCases {
		b.Run(tc.name, func(b *testing.B) {
			fixtures := profileFixtures(b, tc.env)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, data := range fixtures {
					d, err := audio.Open(ctx, bytes.NewReader(data), int64(len(data)), audio.Options{TargetRate: tc.rate, TargetChannels: tc.channels})
					if err != nil {
						b.Fatal(err)
					}
					if err := d.Close(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
