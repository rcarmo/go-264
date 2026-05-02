//go:build amd64

package filter

// Clip operations are the core of deblocking.
// SSE2 has PMINSW/PMAXSW for int16, PMINUB/PMAXUB for uint8.
// For now, the deblocking filter uses scalar code with well-optimized
// Go compiler output. The filter processes one 4-pixel edge at a time
// which is too small for SIMD to provide significant benefit.
// Future: process multiple edges in parallel using SSE2 PMINUB/PMAXUB.

var HasSIMD = true // placeholder for future SIMD deblocking
