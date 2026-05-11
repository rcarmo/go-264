package decode

// Frame is a thin read-only interface over a decoded YUV 4:2:0 picture.
// Callers (cmd/, conformance tests, external consumers) should prefer this
// interface over *frame.Frame so the internal plane representation can change
// without breaking callers.
//
// *frame.Frame satisfies Frame via the DecodedFrame alias; callers that already
// hold *frame.Frame can continue using it or switch to this interface.
type Frame interface {
	// Dimensions
	GetWidth() int
	GetHeight() int

	// Pixel access (x, y in luma coordinates; chroma = luma/2)
	PixelY(x, y int) uint8
	SafePixelY(x, y int) uint8
	PixelU(x, y int) uint8
	PixelV(x, y int) uint8

	// Metadata
	GetPOC() int
	GetFrameNum() int
	IsIDRFrame() bool
	IsRefFrame() bool
}
