package lc

import (
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/aac/internal/aacbits"
	"github.com/rcarmo/go-264/audio/aac/internal/huffman"
	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	maxAccessUnitBytes = 1 << 20
	maxGroups          = 8
	maxSFB             = 64
	longWindowLen      = 1024
	shortWindowLen     = 128
	noiseOffset        = 90
)

type geometry struct {
	sequence    WindowSequence
	shape       WindowShape
	maxSFB      int
	numGroups   int
	numWindows  int
	groupLength [8]int
	offsets     []int
}

type section struct {
	codebook uint8
	start    uint8
	end      uint8
}

type pulseData struct {
	startSFB int
	count    int
	offset   [4]int
	amp      [4]int
}

type parsedChannel struct {
	geom         geometry
	channel      Channel
	sections     [8][64]section
	sectionCount [8]int
}

// channelElements lists the raw_data_block channel elements implied by AAC
// channel configurations 1–7 (ISO/IEC 14496-3 Table 1.19), in bitstream order.
// Element instance tags need not start at zero, but each type/tag pair must
// be unique within the block.
var channelElements = [...][]ElementType{
	1: {ElementSCE},
	2: {ElementCPE},
	3: {ElementSCE, ElementCPE},
	4: {ElementSCE, ElementCPE, ElementSCE},
	5: {ElementSCE, ElementCPE, ElementCPE},
	6: {ElementSCE, ElementCPE, ElementCPE, ElementLFE},
	7: {ElementSCE, ElementCPE, ElementCPE, ElementCPE, ElementLFE},
}

// MaxChannels is the decoded channel count of channel configuration 7.
const MaxChannels = 8

// Elements returns the configured channel element sequence, or nil outside
// channel configurations 1–7. The returned slice must not be modified.
func Elements(channelConfig int) []ElementType {
	if channelConfig < 1 || channelConfig >= len(channelElements) {
		return nil
	}
	return channelElements[channelConfig]
}

// Parse parses one AAC-LC raw_data_block for the configured core sample rate
// and a single-element (mono or stereo) channel configuration. Multichannel
// configurations use Block, which avoids holding every element at once.
func Parse(data []byte, sampleRate, channelConfig int) (Frame, error) {
	var frame Frame
	b, err := NewBlock(data, sampleRate, channelConfig)
	if err != nil {
		return frame, err
	}
	if b.Remaining() != 1 {
		return frame, unsupportedf("AAC-LC Parse channel configuration %d", channelConfig)
	}
	if err := b.Next(&frame); err != nil {
		return frame, err
	}
	err = b.Finish()
	frame.Fills = b.Fills
	frame.DataElements = b.DataElements
	return frame, err
}

// Block parses one raw_data_block element by element. Each configured SCE,
// CPE or LFE must appear exactly once in Elements order; DSE/FIL may appear
// anywhere before END. A Block is a cursor and is not safe for concurrent use.
type Block struct {
	r        aacbits.Reader
	fsIndex  int
	config   int
	elements []ElementType
	next     int
	ids      int
	seenTags [4]uint16
	// Fills and DataElements retain metadata consumed so far.
	Fills        []FillElement
	DataElements []DataElement
}

// NewBlock validates the access-unit size, core sample rate and channel
// configuration without reading any bits.
func NewBlock(data []byte, sampleRate, channelConfig int) (Block, error) {
	var b Block
	if len(data) > maxAccessUnitBytes {
		return b, limitf("AAC access unit size %d", len(data))
	}
	fsIndex, ok := sampleRateToIndex[sampleRate]
	if !ok {
		return b, unsupportedf("AAC-LC sample rate %d", sampleRate)
	}
	elements := Elements(channelConfig)
	if elements == nil {
		return b, unsupportedf("AAC-LC channel configuration %d", channelConfig)
	}
	b.r = *aacbits.New(data)
	b.fsIndex = fsIndex
	b.config = channelConfig
	b.elements = elements
	return b, nil
}

// Remaining reports how many configured channel elements are still unparsed.
func (b *Block) Remaining() int { return len(b.elements) - b.next }

// Next parses the next configured channel element into dst, first consuming
// any DSE/FIL elements. dst is overwritten; its Fills/DataElements are kept.
func (b *Block) Next(dst *Frame) error {
	if b.Remaining() <= 0 {
		return malformedf("no AAC channel element remains for configuration %d", b.config)
	}
	want := b.elements[b.next]
	for {
		id, err := b.readID()
		if err != nil {
			return err
		}
		switch id {
		case 0, 1, 3:
			if got := ElementType(id); got != want {
				return malformedf("AAC %v where channel configuration %d expects %v", got, b.config, want)
			}
			if err := b.parseElement(dst, want); err != nil {
				return err
			}
			b.next++
			return nil
		case 7:
			if b.next == 0 {
				return malformedf("raw_data_block has no SCE/CPE")
			}
			return malformedf("raw_data_block has %d of %d configured channel elements", b.next, len(b.elements))
		default:
			if err := b.parseOther(id); err != nil {
				return err
			}
		}
	}
}

// Finish consumes trailing DSE/FIL elements through END and validates the
// alignment padding. It fails if a channel element is missing or repeated.
func (b *Block) Finish() error {
	if b.Remaining() != 0 {
		return malformedf("raw_data_block has %d of %d configured channel elements", b.next, len(b.elements))
	}
	for {
		id, err := b.readID()
		if err != nil {
			return err
		}
		switch id {
		case 0, 1, 3:
			return malformedf("multiple AAC channel elements")
		case 7:
			if err := consumeZeroPadToByte(&b.r); err != nil {
				return err
			}
			if b.r.Remaining() != 0 {
				return malformedf("trailing bytes after END")
			}
			return nil
		default:
			if err := b.parseOther(id); err != nil {
				return err
			}
		}
	}
}

func (b *Block) readID() (uint32, error) {
	if b.ids >= 64 {
		return 0, limitf("AAC element count")
	}
	b.ids++
	id, err := readBits(&b.r, 3)
	if err != nil {
		return 0, malformedf("raw_data_block missing END")
	}
	return id, nil
}

// parseOther handles non-channel elements other than END.
func (b *Block) parseOther(id uint32) error {
	switch id {
	case 2:
		return unsupportedf("AAC-LC CCE")
	case 4:
		meta, err := parseDSE(&b.r)
		if err != nil {
			return err
		}
		b.DataElements = append(b.DataElements, meta)
		return nil
	case 5:
		return unsupportedf("AAC-LC PCE in raw_data_block")
	case 6:
		meta, err := parseFIL(&b.r)
		if err != nil {
			return err
		}
		b.Fills = append(b.Fills, meta)
		return nil
	default:
		return malformedf("invalid id_syn_ele %d", id)
	}
}

func (b *Block) parseElement(dst *Frame, kind ElementType) error {
	r := &b.r
	// Reset element-level fields: a reused CPE must not inherit an M/S mask.
	dst.Element = kind
	dst.Count = 0
	dst.CommonWindow = false
	dst.MMode = 0
	dst.MS = [maxGroups][maxSFB]bool{}
	tag, err := readBits(r, 4)
	if err != nil {
		return err
	}
	mask := uint16(1) << tag
	if b.seenTags[kind]&mask != 0 {
		return malformedf("duplicate AAC element %v tag %d", kind, tag)
	}
	b.seenTags[kind] |= mask
	if kind != ElementCPE {
		ch, err := parseChannel(r, b.fsIndex, nil)
		if err != nil {
			return err
		}
		if kind == ElementLFE && ch.geom.sequence != SequenceOnlyLong {
			return malformedf("LFE window_sequence %d", ch.geom.sequence)
		}
		dst.Channels[0] = ch.channel
		dst.Count = 1
		return nil
	}
	commonWindow, err := readBool(r)
	if err != nil {
		return err
	}
	dst.CommonWindow = commonWindow
	var shared *geometry
	if commonWindow {
		g, err := parseICSInfo(r, b.fsIndex)
		if err != nil {
			return err
		}
		shared = &g
		if err := parseMSMask(r, dst, g); err != nil {
			return err
		}
	}
	left, err := parseChannel(r, b.fsIndex, shared)
	if err != nil {
		return err
	}
	right, err := parseChannel(r, b.fsIndex, shared)
	if err != nil {
		return err
	}
	dst.Channels[0] = left.channel
	dst.Channels[1] = right.channel
	dst.Count = 2
	return nil
}

func parseChannel(r *aacbits.Reader, fsIndex int, shared *geometry) (parsedChannel, error) {
	var st parsedChannel
	globalGain, err := readBits(r, 8)
	if err != nil {
		return st, err
	}
	if shared == nil {
		g, err := parseICSInfo(r, fsIndex)
		if err != nil {
			return st, err
		}
		st.geom = g
	} else {
		st.geom = *shared
	}
	st.channel = newChannel(st.geom)
	if err := parseSectionData(r, &st); err != nil {
		return st, err
	}
	if err := parseScaleFactorData(r, &st.channel, int(globalGain)); err != nil {
		return st, err
	}
	pulsePresent, err := readBool(r)
	if err != nil {
		return st, err
	}
	var pulse pulseData
	if pulsePresent {
		if st.geom.sequence == SequenceEightShort {
			return st, malformedf("pulse_data on EIGHT_SHORT_SEQUENCE")
		}
		pulse, err = parsePulseData(r, st.geom.offsets)
		if err != nil {
			return st, err
		}
	}
	present, err := readBool(r)
	if err != nil {
		return st, err
	}
	if present {
		if err := parseTNSData(r, &st.channel, st.geom); err != nil {
			return st, err
		}
	}
	gainControl, err := readBool(r)
	if err != nil {
		return st, err
	}
	if gainControl {
		return st, unsupportedf("AAC-LC gain_control_data")
	}
	if err := parseSpectralData(r, &st); err != nil {
		return st, err
	}
	if pulsePresent {
		if err := applyPulseData(&st.channel, pulse); err != nil {
			return st, err
		}
	}
	return st, nil
}

func parseICSInfo(r *aacbits.Reader, fsIndex int) (geometry, error) {
	var g geometry
	reserved, err := readBits(r, 1)
	if err != nil {
		return g, err
	}
	if reserved != 0 {
		return g, malformedf("ics_reserved_bit != 0")
	}
	seq, err := readBits(r, 2)
	if err != nil {
		return g, err
	}
	g.sequence = WindowSequence(seq)
	shape, err := readBits(r, 1)
	if err != nil {
		return g, err
	}
	g.shape = WindowShape(shape)
	if g.sequence == SequenceEightShort {
		maxSFB, err := readBits(r, 4)
		if err != nil {
			return g, err
		}
		grouping, err := readBits(r, 7)
		if err != nil {
			return g, err
		}
		g.maxSFB = int(maxSFB)
		g.offsets = shortOffsets(fsIndex)
		g.numWindows = 8
		g.numGroups = deriveGrouping(int(grouping), &g.groupLength)
	} else {
		maxSFB, err := readBits(r, 6)
		if err != nil {
			return g, err
		}
		predictor, err := readBits(r, 1)
		if err != nil {
			return g, err
		}
		if predictor != 0 {
			return g, unsupportedf("AAC-LC predictor_data_present")
		}
		g.maxSFB = int(maxSFB)
		g.offsets = longOffsets(fsIndex)
		g.numWindows = 1
		g.numGroups = 1
		g.groupLength[0] = 1
	}
	if g.maxSFB > len(g.offsets)-1 {
		return g, malformedf("max_sfb %d exceeds num_swb %d", g.maxSFB, len(g.offsets)-1)
	}
	if g.maxSFB > maxSFB || g.numGroups < 1 || g.numGroups > maxGroups {
		return g, malformedf("invalid ICS geometry")
	}
	return g, nil
}

func deriveGrouping(mask int, dst *[8]int) int {
	dst[0] = 1
	n := 1
	for i := 0; i < 7; i++ {
		if (mask>>(6-i))&1 == 0 {
			dst[n] = 1
			n++
		} else {
			dst[n-1]++
		}
	}
	return n
}

func newChannel(g geometry) Channel {
	var ch Channel
	ch.Sequence = g.sequence
	ch.Shape = g.shape
	ch.MaxSFB = g.maxSFB
	ch.NumGroups = g.numGroups
	copy(ch.GroupLength[:], g.groupLength[:])
	ch.NumOffsets = copy(ch.Offsets[:], g.offsets)
	return ch
}

func parseSectionData(r *aacbits.Reader, st *parsedChannel) error {
	lenBits := 5
	escVal := 31
	if st.geom.sequence == SequenceEightShort {
		lenBits = 3
		escVal = 7
	}
	for g := 0; g < st.geom.numGroups; g++ {
		k := 0
		for k < st.geom.maxSFB {
			cb, err := readBits(r, 4)
			if err != nil {
				return err
			}
			if cb == 12 {
				return unsupportedf("AAC reserved codebook 12")
			}
			sectLen := 0
			for {
				incr, err := readBits(r, lenBits)
				if err != nil {
					return err
				}
				sectLen += int(incr)
				if int(incr) != escVal {
					break
				}
				if sectLen > st.geom.maxSFB {
					return malformedf("section run overflow")
				}
			}
			if sectLen <= 0 {
				return malformedf("zero-length section")
			}
			end := k + sectLen
			if end > st.geom.maxSFB {
				return malformedf("section overruns max_sfb")
			}
			// Each nonempty section consumes at least one of at most 64 bands.
			st.sections[g][st.sectionCount[g]] = section{codebook: uint8(cb), start: uint8(k), end: uint8(end)}
			st.sectionCount[g]++
			for sfb := k; sfb < end; sfb++ {
				st.channel.Codebook[g][sfb] = uint8(cb)
			}
			k = end
		}
	}
	return nil
}

func parseScaleFactorData(r *aacbits.Reader, ch *Channel, globalGain int) error {
	lastSF := globalGain
	lastIS := 0
	lastNRG := globalGain - noiseOffset - 256
	noisePCM := true
	for g := 0; g < ch.NumGroups; g++ {
		for sfb := 0; sfb < ch.MaxSFB; sfb++ {
			cb := ch.Codebook[g][sfb]
			switch cb {
			case 0:
				continue
			case 13:
				if noisePCM {
					raw, err := readBits(r, 9)
					if err != nil {
						return err
					}
					noisePCM = false
					// The first PNS delta is unsigned on the wire; the -256
					// bias is already included in lastNRG. Sign extending here
					// subtracts that bias twice and silences bands with raw>=256.
					lastNRG += int(raw)
				} else {
					d, err := huffman.ReadScalefactor(r)
					if err != nil {
						return err
					}
					lastNRG += int(d)
				}
				if lastNRG < math.MinInt16 || lastNRG > math.MaxInt16 {
					return malformedf("noise energy out of range")
				}
				ch.Scale[g][sfb] = lastNRG
			case 14, 15:
				d, err := huffman.ReadScalefactor(r)
				if err != nil {
					return err
				}
				lastIS += int(d)
				if lastIS < math.MinInt8 || lastIS > math.MaxInt8 {
					return malformedf("intensity position out of range")
				}
				ch.Scale[g][sfb] = lastIS
			default:
				d, err := huffman.ReadScalefactor(r)
				if err != nil {
					return err
				}
				lastSF += int(d)
				if lastSF < 0 || lastSF > 255 {
					return malformedf("scalefactor out of range")
				}
				ch.Scale[g][sfb] = lastSF
			}
		}
	}
	return nil
}

func parsePulseData(r *aacbits.Reader, offsets []int) (pulseData, error) {
	var p pulseData
	n, err := readBits(r, 2)
	if err != nil {
		return p, err
	}
	p.count = int(n) + 1
	start, err := readBits(r, 6)
	if err != nil {
		return p, err
	}
	p.startSFB = int(start)
	if p.startSFB < 0 || p.startSFB >= len(offsets)-1 {
		return p, malformedf("pulse_start_sfb out of range")
	}
	for i := 0; i < p.count; i++ {
		off, err := readBits(r, 5)
		if err != nil {
			return p, err
		}
		amp, err := readBits(r, 4)
		if err != nil {
			return p, err
		}
		p.offset[i] = int(off)
		p.amp[i] = int(amp)
	}
	return p, nil
}

func applyPulseData(ch *Channel, p pulseData) error {
	k := ch.Offsets[p.startSFB]
	for i := 0; i < p.count; i++ {
		k += p.offset[i]
		if k < 0 || k >= longWindowLen {
			return malformedf("pulse offset overruns spectrum")
		}
		if ch.Quant[k] > 0 {
			ch.Quant[k] += int32(p.amp[i])
		} else {
			ch.Quant[k] -= int32(p.amp[i])
		}
	}
	return nil
}

func parseTNSData(r *aacbits.Reader, ch *Channel, g geometry) error {
	nFiltBits, lengthBits, orderBits := 2, 6, 5
	maxOrder := 12
	maxFilters := 3
	if g.sequence == SequenceEightShort {
		nFiltBits, lengthBits, orderBits = 1, 4, 3
		maxOrder = 7
		maxFilters = 1
	}
	for w := 0; w < g.numWindows; w++ {
		nFilt, err := readBits(r, nFiltBits)
		if err != nil {
			return err
		}
		if int(nFilt) > maxFilters {
			return malformedf("tns filter count out of range")
		}
		ch.TNS[w].Count = int(nFilt)
		if nFilt == 0 {
			continue
		}
		coefRes, err := readBool(r)
		if err != nil {
			return err
		}
		ch.TNS[w].CoefRes = coefRes
		for i := 0; i < int(nFilt); i++ {
			length, err := readBits(r, lengthBits)
			if err != nil {
				return err
			}
			order, err := readBits(r, orderBits)
			if err != nil {
				return err
			}
			if int(order) > maxOrder {
				return unsupportedf("AAC-LC TNS order %d", order)
			}
			f := &ch.TNS[w].Filters[i]
			f.Length = int(length)
			f.Order = int(order)
			if order == 0 {
				continue
			}
			direction, err := readBool(r)
			if err != nil {
				return err
			}
			compress, err := readBool(r)
			if err != nil {
				return err
			}
			f.Direction = direction
			f.CoefCompress = compress
			coefBits := 3
			if coefRes {
				coefBits++
			}
			if compress {
				coefBits--
			}
			for j := 0; j < int(order); j++ {
				v, err := readBits(r, coefBits)
				if err != nil {
					return err
				}
				f.Coef[j] = uint8(v)
			}
		}
	}
	return nil
}

func parseSpectralData(r *aacbits.Reader, st *parsedChannel) error {
	windowBase := 0
	for g := 0; g < st.geom.numGroups; g++ {
		wgl := st.geom.groupLength[g]
		groupLen := wgl * windowLen(st.geom.sequence)
		var groupBuf [1024]int32
		var groupOffsets [maxSFB + 1]int
		scaledGroupOffsets(groupOffsets[:st.geom.maxSFB+1], st.geom.offsets, st.geom.maxSFB, wgl)
		for _, sec := range st.sections[g][:st.sectionCount[g]] {
			dim, ok := spectralDim(sec.codebook)
			if !ok {
				continue
			}
			start := groupOffsets[sec.start]
			end := groupOffsets[sec.end]
			for k := start; k < end; k += dim {
				if k+dim > end {
					return malformedf("spectral section not aligned to codebook tuple width")
				}
				tuple, err := huffman.ReadSpectral(r, int(sec.codebook))
				if err != nil {
					return err
				}
				for j := 0; j < dim; j++ {
					groupBuf[k+j] = int32(tuple.Values[j])
				}
			}
		}
		if st.geom.sequence != SequenceEightShort {
			copy(st.channel.Quant[:], groupBuf[:longWindowLen])
			continue
		}
		src := 0
		j := 0
		for sfb := 0; sfb < len(st.geom.offsets)-1; sfb++ {
			width := st.geom.offsets[sfb+1] - st.geom.offsets[sfb]
			for win := 0; win < wgl; win++ {
				dst := (windowBase+win)*shortWindowLen + j
				copy(st.channel.Quant[dst:dst+width], groupBuf[src:src+width])
				src += width
			}
			j += width
		}
		if src != groupLen {
			return malformedf("short group deinterleave size mismatch")
		}
		windowBase += wgl
	}
	return nil
}

func scaledGroupOffsets(out, offsets []int, maxSFB, wgl int) {
	out[0] = 0
	for i := 0; i < maxSFB; i++ {
		out[i+1] = out[i] + (offsets[i+1]-offsets[i])*wgl
	}
}

func spectralDim(cb uint8) (int, bool) {
	switch {
	case cb >= 1 && cb <= 4:
		return 4, true
	case cb >= 5 && cb <= 11:
		return 2, true
	case cb == 12:
		return 0, false
	default:
		return 0, false
	}
}

func parseMSMask(r *aacbits.Reader, frame *Frame, g geometry) error {
	mode, err := readBits(r, 2)
	if err != nil {
		return err
	}
	frame.MMode = int(mode)
	switch mode {
	case 0:
		return nil
	case 1:
		for gr := 0; gr < g.numGroups; gr++ {
			for sfb := 0; sfb < g.maxSFB; sfb++ {
				bit, err := readBool(r)
				if err != nil {
					return err
				}
				frame.MS[gr][sfb] = bit
			}
		}
		return nil
	case 2:
		for gr := 0; gr < g.numGroups; gr++ {
			for sfb := 0; sfb < g.maxSFB; sfb++ {
				frame.MS[gr][sfb] = true
			}
		}
		return nil
	default:
		return malformedf("reserved ms_mask_present %d", mode)
	}
}

func parseDSE(r *aacbits.Reader) (DataElement, error) {
	var d DataElement
	tag, err := readBits(r, 4)
	if err != nil {
		return d, err
	}
	alignFlag, err := readBool(r)
	if err != nil {
		return d, err
	}
	count, err := readBits(r, 8)
	if err != nil {
		return d, err
	}
	payload := int(count)
	if count == 255 {
		esc, err := readBits(r, 8)
		if err != nil {
			return d, err
		}
		payload += int(esc)
	}
	if alignFlag {
		if err := consumeZeroPadToByte(r); err != nil {
			return d, err
		}
	}
	if err := skipBits(r, payload*8); err != nil {
		return d, err
	}
	d.Tag = int(tag)
	d.ByteAligned = alignFlag
	d.PayloadBytes = payload
	return d, nil
}

func parseFIL(r *aacbits.Reader) (FillElement, error) {
	var f FillElement
	count, err := readBits(r, 4)
	if err != nil {
		return f, err
	}
	payload := int(count)
	if count == 15 {
		esc, err := readBits(r, 8)
		if err != nil {
			return f, err
		}
		payload = int(esc) + 14
	}
	f.PayloadBytes = payload
	if payload == 0 {
		f.ExtensionType = FillExtensionNone
		return f, nil
	}
	ty, err := readBits(r, 4)
	if err != nil {
		return f, err
	}
	f.ExtensionType = FillExtensionType(ty)
	remainingBits := payload*8 - 4
	switch ty {
	case 0:
		return f, skipBits(r, remainingBits)
	case 1:
		if remainingBits < 4 {
			return f, malformedf("FIL fill_data too short")
		}
		nib, err := readBits(r, 4)
		if err != nil {
			return f, err
		}
		if nib != 0 {
			return f, malformedf("FIL fill_nibble %d", nib)
		}
		for i := 0; i < payload-1; i++ {
			b, err := readBits(r, 8)
			if err != nil {
				return f, err
			}
			if b != 0xA5 {
				return f, malformedf("FIL fill_byte 0x%02x", b)
			}
		}
		return f, nil
	case 13, 14:
		if err := skipBits(r, remainingBits); err != nil {
			return f, err
		}
		return f, unsupportedf("AAC FIL extension type %d", ty)
	default:
		if err := skipBits(r, remainingBits); err != nil {
			return f, err
		}
		return f, unsupportedf("AAC FIL extension type %d", ty)
	}
}

func windowLen(seq WindowSequence) int {
	if seq == SequenceEightShort {
		return shortWindowLen
	}
	return longWindowLen
}

func longOffsets(fsIndex int) []int {
	n := longWindowCountByIndex[fsIndex]
	// Geometry borrows immutable package data only while parsing. newChannel
	// copies it into the returned frame's array, so callers never alias globals.
	return longWindowOffsetsByIndex[fsIndex][:n]
}

func shortOffsets(fsIndex int) []int {
	n := shortWindowCountByIndex[fsIndex]
	return shortWindowOffsetsByIndex[fsIndex][:n]
}

func signedN(v, n int) int {
	sign := 1 << (n - 1)
	if v&sign == 0 {
		return v
	}
	return v - (1 << n)
}

func consumeZeroPadToByte(r *aacbits.Reader) error {
	pad := r.Position() & 7
	if pad == 0 {
		return nil
	}
	pad = 8 - pad
	v, err := r.Read(pad)
	if err != nil {
		return wrapMalformed("padding", err)
	}
	if v != 0 {
		return malformedf("non-zero alignment padding")
	}
	return nil
}

func skipBits(r *aacbits.Reader, n int) error {
	for n > 0 {
		step := n
		if step > 32 {
			step = 32
		}
		if _, err := r.Read(step); err != nil {
			return wrapMalformed("truncated payload", err)
		}
		n -= step
	}
	return nil
}

func readBits(r *aacbits.Reader, n int) (uint32, error) {
	v, err := r.Read(n)
	if err != nil {
		return 0, wrapMalformed("bitstream", err)
	}
	return v, nil
}

func readBool(r *aacbits.Reader) (bool, error) {
	v, err := readBits(r, 1)
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

func malformedf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{pcm.ErrMalformed}, args...)...)
}

func unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{pcm.ErrUnsupported}, args...)...)
}

func limitf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{pcm.ErrLimit}, args...)...)
}

func wrapMalformed(context string, err error) error {
	if err == nil {
		return nil
	}
	if classify(err) == pcm.ErrLimit {
		return fmt.Errorf("%w: %s", pcm.ErrLimit, context)
	}
	return fmt.Errorf("%w: %s", pcm.ErrMalformed, context)
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case is(err, pcm.ErrLimit):
		return pcm.ErrLimit
	case is(err, pcm.ErrUnsupported):
		return pcm.ErrUnsupported
	default:
		return pcm.ErrMalformed
	}
}

func is(err, target error) bool {
	return err != nil && target != nil && fmt.Errorf("%w", err) != nil && has(err, target)
}

func has(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
