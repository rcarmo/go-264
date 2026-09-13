package ac3

import (
	"fmt"

	"github.com/rcarmo/go-264/audio/pcm"
)

type bitReader struct {
	data   []byte
	pos    int
	failed bool
}

func (b *bitReader) read(bits uint) uint32 {
	if bits > 32 || int(bits) > b.remaining() {
		b.failed = true
		b.pos = len(b.data) * 8
		return 0
	}
	var value uint32
	for range bits {
		p := b.pos
		b.pos++
		value = value<<1 | uint32((b.data[p>>3]>>uint(7-(p&7)))&1)
	}
	return value
}

func (b *bitReader) skip(bits int) {
	if bits < 0 || bits > b.remaining() {
		b.failed = true
		b.pos = len(b.data) * 8
		return
	}
	b.pos += bits
}

func (b *bitReader) remaining() int {
	if b.pos < 0 || b.pos > len(b.data)*8 {
		return 0
	}
	return len(b.data)*8 - b.pos
}

var frameWords = [38][3]uint16{
	{64, 69, 96}, {64, 70, 96}, {80, 87, 120}, {80, 88, 120}, {96, 104, 144}, {96, 105, 144},
	{112, 121, 168}, {112, 122, 168}, {128, 139, 192}, {128, 140, 192}, {160, 174, 240}, {160, 175, 240},
	{192, 208, 288}, {192, 209, 288}, {224, 243, 336}, {224, 244, 336}, {256, 278, 384}, {256, 279, 384},
	{320, 348, 480}, {320, 349, 480}, {384, 417, 576}, {384, 418, 576}, {448, 487, 672}, {448, 488, 672},
	{512, 557, 768}, {512, 558, 768}, {640, 696, 960}, {640, 697, 960}, {768, 835, 1152}, {768, 836, 1152},
	{896, 975, 1344}, {896, 976, 1344}, {1024, 1114, 1536}, {1024, 1115, 1536},
	{1152, 1253, 1728}, {1152, 1254, 1728}, {1280, 1393, 1920}, {1280, 1394, 1920},
}

var bitRates = [19]int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 448, 512, 576, 640}
var sampleRates = [3]int{48000, 44100, 32000}
var fullBandChannels = [8]uint8{2, 1, 2, 3, 3, 4, 4, 5}

func parseHeader(frame []byte) (header, bitReader, error) {
	var h header
	br := bitReader{data: frame}
	if len(frame) < 7 {
		return h, br, fmt.Errorf("%w: short AC-3 syncframe", pcm.ErrMalformed)
	}
	if br.read(16) != 0x0b77 {
		return h, br, fmt.Errorf("%w: AC-3 syncword", pcm.ErrMalformed)
	}
	if looksLikeEAC3(frame) {
		return h, br, fmt.Errorf("%w: E-AC-3", pcm.ErrUnsupported)
	}
	br.skip(16) // crc1
	h.fscod = uint8(br.read(2))
	h.frmsizecod = uint8(br.read(6))
	if h.fscod == 3 || h.frmsizecod >= 38 {
		return h, br, fmt.Errorf("%w: AC-3 frame dimensions", pcm.ErrMalformed)
	}
	h.frameSize = int(frameWords[h.frmsizecod][h.fscod]) * 2
	if len(frame) < h.frameSize {
		return h, br, fmt.Errorf("%w: truncated AC-3 syncframe", pcm.ErrMalformed)
	}
	h.sampleRate = sampleRates[h.fscod]
	h.bitRateKbps = bitRates[h.frmsizecod>>1]
	h.bsid = uint8(br.read(5))
	if h.bsid >= 11 && h.bsid <= 16 {
		return h, br, fmt.Errorf("%w: E-AC-3", pcm.ErrUnsupported)
	}
	if h.bsid > 8 {
		return h, br, fmt.Errorf("%w: AC-3 bsid", pcm.ErrUnsupported)
	}
	h.bsmod = uint8(br.read(3))
	h.acmod = uint8(br.read(3))
	h.nfchans = fullBandChannels[h.acmod]
	if h.acmod&1 != 0 && h.acmod != 1 {
		h.cmixlev = uint8(br.read(2))
	}
	if h.acmod&4 != 0 {
		h.surmixlev = uint8(br.read(2))
	}
	if h.acmod == 2 {
		h.dsurmod = uint8(br.read(2))
	}
	h.lfeon = uint8(br.read(1))
	h.dialnorm = uint8(br.read(5))
	h.compre = uint8(br.read(1))
	if h.compre != 0 {
		h.compr = uint8(br.read(8))
	}
	skipOptional(&br, 8) // langcod
	if br.read(1) != 0 {
		br.skip(7) // mixlevel + roomtyp
	}
	if h.acmod == 0 {
		h.dialnorm2 = uint8(br.read(5))
		h.compre2 = uint8(br.read(1))
		if h.compre2 != 0 {
			h.compr2 = uint8(br.read(8))
		}
		skipOptional(&br, 8)
		if br.read(1) != 0 {
			br.skip(7)
		}
	}
	br.skip(2) // copyrightb, origbs
	skipOptional(&br, 14)
	skipOptional(&br, 14)
	if br.read(1) != 0 {
		br.skip((int(br.read(6)) + 1) * 8)
	}
	if br.failed || br.pos > h.frameSize*8-17 {
		return h, br, fmt.Errorf("%w: AC-3 bitstream information", pcm.ErrMalformed)
	}
	return h, br, nil
}

func looksLikeEAC3(frame []byte) bool {
	if len(frame) < 7 {
		return false
	}
	br := bitReader{data: frame}
	if br.read(16) != 0x0b77 {
		return false
	}
	streamType := br.read(2)
	br.skip(3 + 11)
	frequencyCode := br.read(2)
	if frequencyCode == 3 {
		if br.read(2) == 3 {
			return false
		}
	} else {
		br.skip(2)
	}
	br.skip(3 + 1)
	bitstreamID := br.read(5)
	return !br.failed && streamType <= 2 && bitstreamID >= 11 && bitstreamID <= 16
}

func skipOptional(br *bitReader, bits int) {
	if br.read(1) != 0 {
		br.skip(bits)
	}
}

func crc16(data []byte, bits int) uint16 {
	var crc uint16
	for i := 0; i < bits; i++ {
		input := (data[i>>3] >> uint(7-(i&7))) & 1
		top := byte(crc >> 15)
		crc <<= 1
		if top^input != 0 {
			crc ^= 0x8005
		}
	}
	return crc
}
