package aac

// MaxChannels is the decoded channel count of channel configuration 7 (7.1).
const MaxChannels = 8

// Channel identifies a decoded loudspeaker position. Constants are ordered by
// WAVE_FORMAT_EXTENSIBLE speaker-mask bit, which is also native output order.
type Channel uint8

const (
	FrontLeft Channel = iota
	FrontRight
	FrontCenter
	LFE
	BackLeft
	BackRight
	FrontLeftOfCenter
	FrontRightOfCenter
	BackCenter
	SideLeft
	SideRight
)

func (c Channel) String() string {
	switch c {
	case FrontLeft:
		return "FL"
	case FrontRight:
		return "FR"
	case FrontCenter:
		return "FC"
	case LFE:
		return "LFE"
	case BackLeft:
		return "BL"
	case BackRight:
		return "BR"
	case FrontLeftOfCenter:
		return "FLC"
	case FrontRightOfCenter:
		return "FRC"
	case BackCenter:
		return "BC"
	case SideLeft:
		return "SL"
	case SideRight:
		return "SR"
	default:
		return "unknown"
	}
}

// codedLayouts lists channel positions in raw_data_block element order for
// channel configurations 1–7 (ISO/IEC 14496-3 Table 1.19). Position names
// follow the specification-defined layouts. Configuration 7 is 7.1 wide:
// its first pair is outside-front, second front and third back. FFmpeg's
// default compatibility mode reinterprets the outside-front pair as side
// speakers; strict decoder mode preserves the specification positions.
var codedLayouts = [...][]Channel{
	1: {FrontCenter},
	2: {FrontLeft, FrontRight},
	3: {FrontCenter, FrontLeft, FrontRight},
	4: {FrontCenter, FrontLeft, FrontRight, BackCenter},
	5: {FrontCenter, FrontLeft, FrontRight, BackLeft, BackRight},
	6: {FrontCenter, FrontLeft, FrontRight, BackLeft, BackRight, LFE},
	7: {FrontCenter, FrontLeftOfCenter, FrontRightOfCenter, FrontLeft, FrontRight, BackLeft, BackRight, LFE},
}

// Layout returns the native interleaved output order for an AAC channel
// configuration, or nil outside 1–7. Bitstream element order (centre first)
// is reordered to WAVE/SMPTE order, the order FFmpeg and AC-3 Layout use:
//
//	1: FC
//	2: FL FR
//	3: FL FR FC
//	4: FL FR FC BC
//	5: FL FR FC BL BR
//	6: FL FR FC LFE BL BR
//	7: FL FR FC LFE BL BR FLC FRC
func Layout(channelConfig int) []Channel {
	if channelConfig < 1 || channelConfig >= len(codedLayouts) {
		return nil
	}
	layout, n := sortedLayout(codedLayouts[channelConfig])
	return append([]Channel(nil), layout[:n]...)
}

func sortedLayout(coded []Channel) (out [MaxChannels]Channel, n int) {
	n = copy(out[:], coded)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, n
}

// outputSlots maps each coded channel to its native Layout index.
func outputSlots(coded []Channel) (slots [MaxChannels]int) {
	layout, n := sortedLayout(coded)
	for c, role := range coded {
		for i, want := range layout[:n] {
			if want == role {
				slots[c] = i
			}
		}
	}
	return slots
}
