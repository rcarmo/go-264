package wav

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	formatPCM        = 0x0001
	formatIEEEFloat  = 0x0003
	formatExtensible = 0xFFFE
	riffMagic        = "RIFF"
	rifxMagic        = "RIFX"
	rf64Magic        = "RF64"
	waveMagic        = "WAVE"
	chunkFmt         = "fmt "
	chunkData        = "data"
	scratchBytes     = 8192
)

// Reader reads RIFF/WAVE PCM sample data from an io.ReaderAt.
type Reader struct {
	src        io.ReaderAt
	info       pcm.Info
	dataOffset int64
	blockAlign int64
	pos        int64
	scratch    [scratchBytes]byte
}

type waveFormat struct {
	channels      int
	sampleRate    int
	bitsPerSample int
	blockAlign    uint16
}

// Open validates a RIFF/WAVE file and returns a bounded PCM reader.
func Open(ctx context.Context, src io.ReaderAt, size int64, limits pcm.Limits) (*Reader, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("%w: nil source", pcm.ErrMalformed)
	}
	limits, err := limits.Validated()
	if err != nil {
		return nil, err
	}
	if size < 12 {
		return nil, fmt.Errorf("%w: file too short", pcm.ErrMalformed)
	}
	if size > limits.MaxBytes {
		return nil, fmt.Errorf("%w: file size %d exceeds limit %d", pcm.ErrLimit, size, limits.MaxBytes)
	}

	var header [12]byte
	if err := readFullAt(ctx, src, 0, header[:]); err != nil {
		return nil, wrapKind(pcm.ErrMalformed, "read RIFF header", err)
	}

	riffID := string(header[0:4])
	switch riffID {
	case riffMagic:
		// little-endian RIFF/WAVE
	case rifxMagic, rf64Magic:
		return nil, fmt.Errorf("%w: %s not supported", pcm.ErrUnsupported, riffID)
	default:
		return nil, fmt.Errorf("%w: missing RIFF header", pcm.ErrMalformed)
	}
	if string(header[8:12]) != waveMagic {
		return nil, fmt.Errorf("%w: missing WAVE form type", pcm.ErrMalformed)
	}

	riffSize := int64(binary.LittleEndian.Uint32(header[4:8]))
	if riffSize+8 != size {
		return nil, fmt.Errorf("%w: RIFF size %d does not match source size %d", pcm.ErrMalformed, riffSize+8, size)
	}

	var (
		format     waveFormat
		haveFmt    bool
		haveData   bool
		dataOffset int64
		dataBytes  int64
	)

	for offset, chunks := int64(12), 0; offset < size; chunks++ {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		if chunks >= limits.MaxChunks {
			return nil, fmt.Errorf("%w: chunk count exceeds limit %d", pcm.ErrLimit, limits.MaxChunks)
		}
		if size-offset < 8 {
			return nil, fmt.Errorf("%w: truncated chunk header at offset %d", pcm.ErrMalformed, offset)
		}

		var chunkHeader [8]byte
		if err := readFullAt(ctx, src, offset, chunkHeader[:]); err != nil {
			return nil, wrapKind(pcm.ErrMalformed, fmt.Sprintf("read chunk header at offset %d", offset), err)
		}
		chunkID := string(chunkHeader[0:4])
		chunkSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		chunkDataOffset, ok := addInt64(offset, 8)
		if !ok {
			return nil, fmt.Errorf("%w: chunk header overflow at offset %d", pcm.ErrMalformed, offset)
		}
		chunkEnd, ok := addInt64(chunkDataOffset, chunkSize)
		if !ok || chunkEnd > size {
			return nil, fmt.Errorf("%w: chunk %q exceeds RIFF bounds", pcm.ErrMalformed, chunkID)
		}
		paddedEnd := chunkEnd
		if chunkSize&1 != 0 {
			paddedEnd, ok = addInt64(chunkEnd, 1)
			if !ok || paddedEnd > size {
				return nil, fmt.Errorf("%w: chunk %q missing pad byte", pcm.ErrMalformed, chunkID)
			}
		}

		switch chunkID {
		case chunkFmt:
			if haveFmt {
				return nil, fmt.Errorf("%w: duplicate fmt chunk", pcm.ErrMalformed)
			}
			if haveData {
				return nil, fmt.Errorf("%w: fmt chunk after data", pcm.ErrMalformed)
			}
			format, err = parseFormatChunk(ctx, src, chunkDataOffset, chunkSize)
			if err != nil {
				return nil, err
			}
			haveFmt = true
		case chunkData:
			if !haveFmt {
				return nil, fmt.Errorf("%w: data chunk before fmt chunk", pcm.ErrMalformed)
			}
			if haveData {
				return nil, fmt.Errorf("%w: duplicate data chunk", pcm.ErrMalformed)
			}
			haveData = true
			dataOffset = chunkDataOffset
			dataBytes = chunkSize
		}

		offset = paddedEnd
	}

	if !haveFmt {
		return nil, fmt.Errorf("%w: missing fmt chunk", pcm.ErrMalformed)
	}
	if !haveData {
		return nil, fmt.Errorf("%w: missing data chunk", pcm.ErrMalformed)
	}
	if int64(format.blockAlign) <= 0 {
		return nil, fmt.Errorf("%w: invalid block align", pcm.ErrMalformed)
	}
	if dataBytes%int64(format.blockAlign) != 0 {
		return nil, fmt.Errorf("%w: data size %d is not aligned to block size %d", pcm.ErrMalformed, dataBytes, format.blockAlign)
	}
	frames := dataBytes / int64(format.blockAlign)
	if limits.MaxDurationSeconds > 0 {
		maxFrames, ok := mulInt64(limits.MaxDurationSeconds, int64(format.sampleRate))
		if !ok {
			return nil, fmt.Errorf("%w: duration limit overflow", pcm.ErrLimit)
		}
		if frames > maxFrames {
			return nil, fmt.Errorf("%w: duration exceeds limit of %d seconds", pcm.ErrLimit, limits.MaxDurationSeconds)
		}
	}

	return &Reader{
		src: src,
		info: pcm.Info{
			SampleRate:    format.sampleRate,
			Channels:      format.channels,
			Frames:        frames,
			BitsPerSample: format.bitsPerSample,
		},
		dataOffset: dataOffset,
		blockAlign: int64(format.blockAlign),
	}, nil
}

// Info returns the decoded PCM stream metadata.
func (r *Reader) Info() pcm.Info {
	return r.info
}

// SeekFrame seeks to an absolute frame position in [0, Frames].
func (r *Reader) SeekFrame(ctx context.Context, frame int64) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("%w: frame %d out of range [0,%d]", pcm.ErrMalformed, frame, r.info.Frames)
	}
	r.pos = frame
	return nil
}

// ReadS16Frames reads native PCM16 samples directly into caller storage. It is
// available only for 16-bit WAV input; callers must preserve the source channel
// layout. The byte scratch remains reader-owned and never aliases dst.
func (r *Reader) ReadS16Frames(ctx context.Context, dst []int16) (int, error) {
	if r.info.BitsPerSample != 16 {
		return 0, fmt.Errorf("%w: direct S16 requires 16-bit PCM", pcm.ErrUnsupported)
	}
	if err := checkContext(ctx); err != nil {
		return 0, err
	}
	if len(dst) == 0 {
		return 0, nil
	}
	channels := r.info.Channels
	if channels <= 0 || len(dst)%channels != 0 {
		return 0, fmt.Errorf("%w: destination length %d is not a multiple of channels %d", pcm.ErrMalformed, len(dst), channels)
	}
	if r.pos >= r.info.Frames {
		return 0, io.EOF
	}

	requestFrames := int64(len(dst) / channels)
	framesToRead := min(requestFrames, r.info.Frames-r.pos)
	maxScratchFrames := int64(len(r.scratch)) / r.blockAlign
	if maxScratchFrames <= 0 {
		return 0, fmt.Errorf("%w: invalid scratch configuration", pcm.ErrMalformed)
	}

	decodedFrames := 0
	for remaining := framesToRead; remaining > 0; {
		if err := checkContext(ctx); err != nil {
			return decodedFrames, err
		}
		batchFrames := min(remaining, maxScratchFrames)
		byteCount := int(batchFrames * r.blockAlign)
		buf := r.scratch[:byteCount]
		byteOffset, ok := mulInt64(r.pos, r.blockAlign)
		if !ok {
			return decodedFrames, fmt.Errorf("%w: data offset overflow", pcm.ErrLimit)
		}
		dataOffset, ok := addInt64(r.dataOffset, byteOffset)
		if !ok {
			return decodedFrames, fmt.Errorf("%w: data offset overflow", pcm.ErrLimit)
		}
		if err := readFullAt(ctx, r.src, dataOffset, buf); err != nil {
			readErr := wrapKind(pcm.ErrMalformed, "read PCM data", err)
			return decodedFrames, readErr
		}
		start := decodedFrames * channels
		for i, j := start, 0; j < len(buf); i, j = i+1, j+2 {
			dst[i] = int16(binary.LittleEndian.Uint16(buf[j : j+2]))
		}
		decodedFrames += int(batchFrames)
		r.pos += batchFrames
		remaining -= batchFrames
	}
	if framesToRead < requestFrames {
		return decodedFrames, io.EOF
	}
	return decodedFrames, nil
}

// ReadFrames reads interleaved normalised float64 samples and returns the
// number of complete frames decoded into dst.
func (r *Reader) ReadFrames(ctx context.Context, dst []float64) (int, error) {
	if err := checkContext(ctx); err != nil {
		return 0, err
	}
	if len(dst) == 0 {
		return 0, nil
	}
	channels := r.info.Channels
	if channels <= 0 {
		return 0, fmt.Errorf("%w: invalid channel count", pcm.ErrMalformed)
	}
	if len(dst)%channels != 0 {
		return 0, fmt.Errorf("%w: destination length %d is not a multiple of channels %d", pcm.ErrMalformed, len(dst), channels)
	}
	if r.pos >= r.info.Frames {
		return 0, io.EOF
	}

	requestFrames := int64(len(dst) / channels)
	availableFrames := r.info.Frames - r.pos
	framesToRead := requestFrames
	if framesToRead > availableFrames {
		framesToRead = availableFrames
	}

	maxScratchFrames := int64(len(r.scratch)) / r.blockAlign
	if maxScratchFrames <= 0 {
		return 0, fmt.Errorf("%w: invalid scratch configuration", pcm.ErrMalformed)
	}

	decodedFrames := 0
	for remaining := framesToRead; remaining > 0; {
		if err := checkContext(ctx); err != nil {
			if decodedFrames > 0 {
				return decodedFrames, err
			}
			return 0, err
		}
		batchFrames := remaining
		if batchFrames > maxScratchFrames {
			batchFrames = maxScratchFrames
		}
		byteCount := int(batchFrames * r.blockAlign)
		buf := r.scratch[:byteCount]
		byteOffset, ok := mulInt64(r.pos, r.blockAlign)
		if !ok {
			return decodedFrames, fmt.Errorf("%w: data offset overflow", pcm.ErrLimit)
		}
		dataOffset, ok := addInt64(r.dataOffset, byteOffset)
		if !ok {
			return decodedFrames, fmt.Errorf("%w: data offset overflow", pcm.ErrLimit)
		}
		if err := readFullAt(ctx, r.src, dataOffset, buf); err != nil {
			readErr := wrapKind(pcm.ErrMalformed, "read PCM data", err)
			if decodedFrames > 0 {
				return decodedFrames, readErr
			}
			return 0, readErr
		}

		start := decodedFrames * channels
		decodePCM(dst[start:start+int(batchFrames)*channels], buf, r.info.BitsPerSample)
		decodedFrames += int(batchFrames)
		r.pos += batchFrames
		remaining -= batchFrames
	}

	if framesToRead < requestFrames {
		return decodedFrames, io.EOF
	}
	return decodedFrames, nil
}

func parseFormatChunk(ctx context.Context, src io.ReaderAt, offset, size int64) (waveFormat, error) {
	if size < 16 {
		return waveFormat{}, fmt.Errorf("%w: fmt chunk too short", pcm.ErrMalformed)
	}
	readLen := 16
	if size > 16 {
		if size < 18 {
			return waveFormat{}, fmt.Errorf("%w: fmt chunk extension truncated", pcm.ErrMalformed)
		}
		readLen = 18
	}

	var buf [18]byte
	if err := readFullAt(ctx, src, offset, buf[:readLen]); err != nil {
		return waveFormat{}, wrapKind(pcm.ErrMalformed, "read fmt chunk", err)
	}

	tag := binary.LittleEndian.Uint16(buf[0:2])
	channels := binary.LittleEndian.Uint16(buf[2:4])
	sampleRate := binary.LittleEndian.Uint32(buf[4:8])
	byteRate := binary.LittleEndian.Uint32(buf[8:12])
	blockAlign := binary.LittleEndian.Uint16(buf[12:14])
	bitsPerSample := binary.LittleEndian.Uint16(buf[14:16])

	switch tag {
	case formatPCM:
		// supported below
	case formatIEEEFloat:
		return waveFormat{}, fmt.Errorf("%w: IEEE float WAV not supported", pcm.ErrUnsupported)
	case formatExtensible:
		return waveFormat{}, fmt.Errorf("%w: WAVE_FORMAT_EXTENSIBLE not supported", pcm.ErrUnsupported)
	default:
		return waveFormat{}, fmt.Errorf("%w: format tag 0x%04x not supported", pcm.ErrUnsupported, tag)
	}

	if size > 16 {
		cbSize := int64(binary.LittleEndian.Uint16(buf[16:18]))
		if 18+cbSize != size {
			return waveFormat{}, fmt.Errorf("%w: fmt extension length mismatch", pcm.ErrMalformed)
		}
	}

	if channels != 1 && channels != 2 {
		return waveFormat{}, fmt.Errorf("%w: %d channels not supported", pcm.ErrUnsupported, channels)
	}
	if sampleRate < 8000 || sampleRate > 192000 {
		return waveFormat{}, fmt.Errorf("%w: sample rate %d not supported", pcm.ErrUnsupported, sampleRate)
	}
	if bitsPerSample != 8 && bitsPerSample != 16 && bitsPerSample != 24 && bitsPerSample != 32 {
		return waveFormat{}, fmt.Errorf("%w: %d-bit PCM not supported", pcm.ErrUnsupported, bitsPerSample)
	}

	expectedBlockAlign := uint32(channels) * uint32(bitsPerSample/8)
	if blockAlign != uint16(expectedBlockAlign) {
		return waveFormat{}, fmt.Errorf("%w: block align %d does not match channels/bits %d", pcm.ErrMalformed, blockAlign, expectedBlockAlign)
	}
	expectedByteRate := uint64(sampleRate) * uint64(blockAlign)
	if expectedByteRate > math.MaxUint32 || byteRate != uint32(expectedByteRate) {
		return waveFormat{}, fmt.Errorf("%w: byte rate %d does not match sample rate/block align %d", pcm.ErrMalformed, byteRate, expectedByteRate)
	}

	return waveFormat{
		channels:      int(channels),
		sampleRate:    int(sampleRate),
		bitsPerSample: int(bitsPerSample),
		blockAlign:    blockAlign,
	}, nil
}

func decodePCM(dst []float64, src []byte, bitsPerSample int) {
	switch bitsPerSample {
	case 8:
		decodePCM8(dst, src)
	case 16:
		decodePCM16(dst, src)
	case 24:
		decodePCM24Scalar(dst, src)
	case 32:
		decodePCM32(dst, src)
	}
}

func decodePCM8Scalar(dst []float64, src []byte) {
	for i, b := range src {
		dst[i] = (float64(b) - 128) / 128
	}
}

func decodePCM16Scalar(dst []float64, src []byte) {
	for i, j := 0, 0; i < len(dst); i, j = i+1, j+2 {
		v := int16(binary.LittleEndian.Uint16(src[j : j+2]))
		dst[i] = float64(v) / 32768
	}
}

func decodePCM24Scalar(dst []float64, src []byte) {
	for i, j := 0, 0; i < len(dst); i, j = i+1, j+3 {
		v := int32(src[j]) | int32(src[j+1])<<8 | int32(src[j+2])<<16
		if v&0x00800000 != 0 {
			v |= ^0x00FFFFFF
		}
		dst[i] = float64(v) / 8388608
	}
}

func decodePCM32Scalar(dst []float64, src []byte) {
	for i, j := 0, 0; i < len(dst); i, j = i+1, j+4 {
		v := int32(binary.LittleEndian.Uint32(src[j : j+4]))
		dst[i] = float64(v) / 2147483648
	}
}

func readFullAt(ctx context.Context, src io.ReaderAt, off int64, dst []byte) error {
	for len(dst) > 0 {
		if err := checkContext(ctx); err != nil {
			return err
		}
		n, err := src.ReadAt(dst, off)
		if n > 0 {
			off += int64(n)
			dst = dst[n:]
		}
		if err != nil {
			if (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) && len(dst) > 0 && n > 0 {
				continue
			}
			if len(dst) == 0 && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func wrapKind(kind error, msg string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", kind, msg, err)
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > 0 && b > 0 && a > math.MaxInt64/b {
		return 0, false
	}
	if a > 0 && b < 0 && b < math.MinInt64/a {
		return 0, false
	}
	if a < 0 && b > 0 && a < math.MinInt64/b {
		return 0, false
	}
	if a < 0 && b < 0 && a < math.MaxInt64/b {
		return 0, false
	}
	return a * b, true
}
