package decode

import "github.com/rcarmo/go-264/frame"

// intraNeighborAvailable applies the macroblock part of H.264 6.4.8 and 8.3:
// prediction cannot cross a slice boundary, and constrained intra prediction
// also excludes inter-coded neighbours. The current MB is handled separately:
// its already reconstructed blocks remain available before mbIsIntra is stored.
// Callers still enforce picture bounds and the intra-block decoding order.
func (d *Decoder) intraNeighborAvailable(currentMB, targetMB int) bool {
	if d.picture == nil || d.slice == nil {
		// Standalone reconstruction tests have no active slice.
		return true
	}
	if targetMB == currentMB {
		return true
	}
	if targetMB < 0 || targetMB >= len(d.picture.mbSliceID) || d.picture.mbSliceID[targetMB] != d.slice.id {
		return false
	}
	return !d.slice.pps.ConstrainedIntraPred || d.picture.mbIsIntra[targetMB]
}

// intraLumaSampleAvailable maps a picture-space luma coordinate to its owning
// macroblock. Chroma 4:2:0 callers multiply their coordinates by two.
func (d *Decoder) intraLumaSampleAvailable(f *frame.Frame, mbX, mbY, x, y int) bool {
	if x < 0 || y < 0 || x >= f.Width || y >= f.Height {
		return false
	}
	mbWidth := f.Width / 16
	return d.intraNeighborAvailable(mbY*mbWidth+mbX, (y/16)*mbWidth+x/16)
}
