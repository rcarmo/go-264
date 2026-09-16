package wav

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func TestQualificationExactFourHourSparseWAV(t *testing.T) {
	t.Parallel()

	const (
		sampleRate = 16000
		channels   = 1
		bits       = 16
		seconds    = 4 * 60 * 60
	)
	frames := int64(sampleRate * seconds)
	dataBytes := frames * int64(channels*(bits/8))
	path := filepath.Join(t.TempDir(), "exact-4h.wav")
	writeSparsePCMFixture(t, path, channels, sampleRate, bits, dataBytes)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	r, err := Open(context.Background(), f, 44+dataBytes, pcm.Limits{MaxBytes: 512 << 20, MaxDurationSeconds: seconds})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	info := r.Info()
	if info.SampleRate != sampleRate || info.Channels != channels || info.BitsPerSample != bits || info.Frames != frames {
		t.Fatalf("Info() = %+v", info)
	}
	if err := r.SeekFrame(context.Background(), frames-1); err != nil {
		t.Fatalf("SeekFrame(last) error = %v", err)
	}
	buf := []float64{7, 7}
	n, err := r.ReadFrames(context.Background(), buf)
	if n != 1 || err != io.EOF {
		t.Fatalf("ReadFrames(last) = (%d, %v), want (1, EOF)", n, err)
	}
	if buf[0] != 0 || buf[1] != 7 {
		t.Fatalf("last-frame buffer = %v, want [0 7]", buf)
	}
	if err := r.SeekFrame(context.Background(), frames); err != nil {
		t.Fatalf("SeekFrame(end) error = %v", err)
	}
	if n, err := r.ReadFrames(context.Background(), buf[:1]); n != 0 || err != io.EOF {
		t.Fatalf("ReadFrames(end) = (%d, %v), want (0, EOF)", n, err)
	}
}

func TestQualificationWAVLimitBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("duration_rejects_over_4h_even_below_default_bytes", func(t *testing.T) {
		t.Parallel()

		const (
			sampleRate = 16000
			channels   = 1
			bits       = 16
			seconds    = 4 * 60 * 60
		)
		frames := int64(sampleRate*seconds) + 1
		dataBytes := frames * int64(channels*(bits/8))
		path := filepath.Join(t.TempDir(), "over-4h.wav")
		writeSparsePCMFixture(t, path, channels, sampleRate, bits, dataBytes)

		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		if _, err := Open(context.Background(), f, 44+dataBytes, pcm.Limits{}); !errors.Is(err, pcm.ErrLimit) {
			t.Fatalf("Open() error = %v, want ErrLimit", err)
		}
	})

	t.Run("byte_cap_is_inclusive", func(t *testing.T) {
		t.Parallel()

		const maxBytes = int64(512 << 20)
		path := filepath.Join(t.TempDir(), "exact-cap.wav")
		writeSparsePCMFixture(t, path, 1, 192000, 16, maxBytes-44)

		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		r, err := Open(context.Background(), f, maxBytes, pcm.Limits{MaxBytes: maxBytes})
		if err != nil {
			t.Fatalf("Open(exact cap) error = %v", err)
		}
		if got := r.Info().Frames; got != (maxBytes-44)/2 {
			t.Fatalf("Info().Frames = %d, want %d", got, (maxBytes-44)/2)
		}
	})

	t.Run("byte_cap_rejects_one_byte_over", func(t *testing.T) {
		t.Parallel()

		const maxBytes = int64(512 << 20)
		path := filepath.Join(t.TempDir(), "over-cap.wav")
		writeSparsePCMFixture(t, path, 1, 192000, 8, maxBytes+1-44)

		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		if _, err := Open(context.Background(), f, maxBytes+1, pcm.Limits{MaxBytes: maxBytes}); !errors.Is(err, pcm.ErrLimit) {
			t.Fatalf("Open(over cap) error = %v, want ErrLimit", err)
		}
	})

	t.Run("duration_limit_multiplication_overflow", func(t *testing.T) {
		t.Parallel()

		data := makePCMFixture(1, 1, 192000, 16, []int32{0}, nil, false)
		limits := pcm.Limits{MaxDurationSeconds: math.MaxInt64/192000 + 1}
		if _, err := Open(context.Background(), bytesReaderAt(data), int64(len(data)), limits); !errors.Is(err, pcm.ErrLimit) {
			t.Fatalf("Open(overflow) error = %v, want ErrLimit", err)
		}
	})
}

func writeSparsePCMFixture(t *testing.T, path string, channels, sampleRate, bits int, dataBytes int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if dataBytes < 0 {
		t.Fatalf("negative data bytes: %d", dataBytes)
	}
	blockAlign := uint16(channels * (bits / 8))
	byteRate := uint32(sampleRate) * uint32(blockAlign)
	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataBytes))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], uint16(bits))
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataBytes))
	if _, err := f.Write(header); err != nil {
		t.Fatal(err)
	}
	if dataBytes == 0 {
		return
	}
	if _, err := f.Seek(44+dataBytes-1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
}

type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
