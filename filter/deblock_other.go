//go:build (!amd64 && !arm64) || (arm64 && (purego || !go1.27))

package filter

var HasSIMD = false
