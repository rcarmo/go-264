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
	Offsets     []int // Immutable after Parse returns.
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

// Frame is one parsed AAC-LC raw_data_block.
type Frame struct {
	Channels     [2]Channel
	Count        int
	CommonWindow bool
	MMode        int // 0 absent, 1 per-band, 2 all MS; resolved MS flags drive intensity inversion
	MS           [8][64]bool
	Fills        []FillElement
	DataElements []DataElement
}
