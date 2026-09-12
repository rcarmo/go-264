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

// Parse parses one AAC-LC raw_data_block for the configured core sample rate
// and channel count.
func Parse(data []byte, sampleRate, channels int) (Frame, error) {
	var frame Frame
	if len(data) > maxAccessUnitBytes {
		return frame, limitf("AAC access unit size %d", len(data))
	}
	fsIndex, ok := sampleRateToIndex[sampleRate]
	if !ok {
		return frame, unsupportedf("AAC-LC sample rate %d", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return frame, unsupportedf("AAC-LC channels %d", channels)
	}

	r := aacbits.New(data)
	audioSeen := false
	for elements := 0; ; elements++ {
		if elements >= 64 {
			return frame, limitf("AAC element count")
		}
		id, err := readBits(r, 3)
		if err != nil {
			return frame, malformedf("raw_data_block missing END")
		}
		switch id {
		case 0:
			if audioSeen {
				return frame, malformedf("multiple AAC channel elements")
			}
			if channels != 1 {
				return frame, malformedf("mono SCE does not match configured stereo")
			}
			if _, err := readBits(r, 4); err != nil {
				return frame, err
			}
			ch, err := parseChannel(r, fsIndex, nil)
			if err != nil {
				return frame, err
			}
			frame.Channels[0] = ch.channel
			frame.Count = 1
			audioSeen = true
		case 1:
			if audioSeen {
				return frame, malformedf("multiple AAC channel elements")
			}
			if channels != 2 {
				return frame, malformedf("stereo CPE does not match configured mono")
			}
			if _, err := readBits(r, 4); err != nil {
				return frame, err
			}
			commonWindow, err := readBool(r)
			if err != nil {
				return frame, err
			}
			frame.CommonWindow = commonWindow
			var shared *geometry
			if commonWindow {
				g, err := parseICSInfo(r, fsIndex)
				if err != nil {
					return frame, err
				}
				shared = &g
				if err := parseMSMask(r, &frame, g); err != nil {
					return frame, err
				}
			}
			left, err := parseChannel(r, fsIndex, shared)
			if err != nil {
				return frame, err
			}
			right, err := parseChannel(r, fsIndex, shared)
			if err != nil {
				return frame, err
			}
			frame.Channels[0] = left.channel
			frame.Channels[1] = right.channel
			frame.Count = 2
			audioSeen = true
		case 2:
			return frame, unsupportedf("AAC-LC CCE")
		case 3:
			return frame, unsupportedf("AAC-LC LFE")
		case 4:
			meta, err := parseDSE(r)
			if err != nil {
				return frame, err
			}
			frame.DataElements = append(frame.DataElements, meta)
		case 5:
			return frame, unsupportedf("AAC-LC PCE in raw_data_block")
		case 6:
			meta, err := parseFIL(r)
			if err != nil {
				return frame, err
			}
			frame.Fills = append(frame.Fills, meta)
		case 7:
			if !audioSeen {
				return frame, malformedf("raw_data_block has no SCE/CPE")
			}
			if err := consumeZeroPadToByte(r); err != nil {
				return frame, err
			}
			if r.Remaining() != 0 {
				return frame, malformedf("trailing bytes after END")
			}
			return frame, nil
		default:
			return frame, malformedf("invalid id_syn_ele %d", id)
		}
	}
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
