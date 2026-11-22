package lc

// WindowSequence maps AAC window_sequence.
type WindowSequence uint8

const (
	SequenceOnlyLong   WindowSequence = 0
	SequenceLongStart  WindowSequence = 1
	SequenceEightShort WindowSequence = 2
	SequenceLongStop   WindowSequence = 3
)

// WindowShape maps AAC window_shape.
type WindowShape uint8

const (
	ShapeSine WindowShape = 0
	ShapeKBD  WindowShape = 1
)

// ElementType is an AAC raw_data_block audio element id_syn_ele.
type ElementType uint8

const (
	ElementSCE ElementType = 0 // single_channel_element
	ElementCPE ElementType = 1 // channel_pair_element
	ElementLFE ElementType = 3 // lfe_channel_element
)

func (e ElementType) String() string {
	switch e {
	case ElementSCE:
		return "SCE"
	case ElementCPE:
		return "CPE"
	case ElementLFE:
		return "LFE"
	default:
		return "unknown"
	}
}

// FillExtensionType is the parsed FIL extension type.
type FillExtensionType int

const (
	FillExtensionNone FillExtensionType = -1
	FillExtensionFill FillExtensionType = 0
	FillExtensionData FillExtensionType = 1
)

// TNSFilter is one raw AAC-LC TNS filter payload.
type TNSFilter struct {
	Length       int
	Order        int
	Direction    bool
	CoefCompress bool
	Coef         [12]uint8
}

// TNSWindow is one window's raw TNS payload.
type TNSWindow struct {
	CoefRes bool
	Count   int
	Filters [4]TNSFilter
}

// Channel is one parsed AAC-LC individual channel stream.
type Channel struct {
	Sequence    WindowSequence
	Shape       WindowShape
	MaxSFB      int
	NumGroups   int
	GroupLength [8]int
	Offsets     [65]int // Frame-owned band offsets; first NumOffsets entries are valid.
	NumOffsets  int
	Quant       [1024]int32
	Codebook    [8][64]uint8
	Scale       [8][64]int
	TNS         [8]TNSWindow
}

// FillElement is FIL metadata retained by Parse.
type FillElement struct {
	PayloadBytes  int
	ExtensionType FillExtensionType
}

// DataElement is DSE metadata retained by Parse.
type DataElement struct {
	Tag          int
	ByteAligned  bool
	PayloadBytes int
}

// Frame is one parsed AAC-LC channel element. Parse returns the only element
// of a mono/stereo raw_data_block together with its DSE/FIL metadata; Block
// yields each element of multichannel blocks in bitstream order.
type Frame struct {
	Element      ElementType
	Channels     [2]Channel
	Count        int
	CommonWindow bool
	MMode        int // 0 absent, 1 per-band, 2 all MS; resolved MS flags drive intensity inversion
	MS           [8][64]bool
	Fills        []FillElement
	DataElements []DataElement
}
