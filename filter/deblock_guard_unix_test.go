//go:build (linux || darwin) && (amd64 || arm64) && !purego

package filter

import (
	"bytes"
	"runtime"
	"syscall"
	"testing"
)

func guardedDeblockPage(t *testing.T) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Munmap(mem) })
	return mem[page : 2*page]
}

func TestVerticalDeblockGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	for _, atEnd := range []bool{false, true} {
		for _, bs := range [][4]int{{1, 2, 3, 1}, {4, 4, 4, 4}, {0, 2, 0, 3}, {1, 4, 3, 4}} {
			for _, chroma := range []bool{false, true} {
				stride, rows, edgeX := 8, 16, 4
				samples := []byte{104, 106, 109, 110, 113, 116, 118, 119}
				if chroma {
					stride, rows, edgeX = 4, 8, 2
					samples = []byte{106, 110, 113, 119}
				}
				// Each row is exactly the readable p3..q3 (luma) or p1..q1
				// (chroma) window, including the last row at the guard page.
				n, off := stride*rows, 0
				if atEnd {
					off = page - n
				}
				mem := guardedDeblockPage(t)
				got := mem[off : off+n : off+n]
				for row := 0; row < rows; row++ {
					for col, sample := range samples {
						got[row*stride+col] = sample + byte(row%3)
					}
				}
				want := append([]byte(nil), got...)
				if chroma {
					FilterChromaEdgeV(got, stride, edgeX, 0, rows, bs, 31, 31)
					filterChromaEdgeVReference(want, stride, edgeX, 0, rows, bs, 31, 31)
				} else {
					FilterLumaEdgeV(got, stride, edgeX, 0, rows, bs, 31, 31)
					filterLumaEdgeVReference(want, stride, edgeX, 0, rows, bs, 31, 31)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("chroma=%t end=%t bs=%v", chroma, atEnd, bs)
				}
			}
		}
	}
}

func TestHorizontalDeblockGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	for _, atEnd := range []bool{false, true} {
		for _, bs := range [][4]int{{1, 2, 3, 1}, {4, 4, 4, 4}, {0, 2, 0, 3}, {1, 4, 3, 4}} {
			for _, chroma := range []bool{false, true} {
				rows, edgeY, cols := 8, 4, 16
				samples := []byte{104, 106, 109, 110, 113, 116, 118, 119}
				if chroma {
					rows, edgeY, cols = 4, 2, 8
					samples = []byte{106, 110, 113, 119}
				}
				stride := cols
				n, off := stride*rows, 0
				if atEnd {
					off = page - n
				}
				mem := guardedDeblockPage(t)
				got := mem[off : off+n : off+n]
				for row, sample := range samples {
					for col := 0; col < cols; col++ {
						got[row*stride+col] = sample + byte(col%3)
					}
				}
				want := append([]byte(nil), got...)
				if chroma {
					FilterChromaEdgeH(got, stride, edgeY, 0, cols, bs, 31, 31)
					filterChromaEdgeHReference(want, stride, edgeY, 0, cols, bs, 31, 31)
				} else {
					FilterLumaEdgeH(got, stride, edgeY, 0, cols, bs, 31, 31)
					filterLumaEdgeHReference(want, stride, edgeY, 0, cols, bs, 31, 31)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("chroma=%t end=%t bs=%v", chroma, atEnd, bs)
				}
			}
		}
	}
}

// Invalid public geometry must never reach a raw NEON pointer calculation.
// The scalar fallback may panic on malformed input, but a Go bounds panic must
// not become an unrecoverable assembly read from an overflowed address.
func TestDeblockSIMDRejectsOverflowingFootprints(t *testing.T) {
	if runtime.GOARCH != "arm64" || !HasSIMD {
		t.Skip("ARM64 NEON dispatcher unavailable")
	}
	bs := [4]int{1, 1, 1, 1}
	for _, kernel := range []struct {
		name string
		call func([]byte, int, int) bool
	}{
		{"luma H", func(p []byte, s, r int) bool { return filterLumaNormalHSIMD(p, s, r+4, 0, 1, 64, 16, 31) }},
		{"luma V", func(p []byte, s, r int) bool { return filterLumaNormalVSIMD(p, s, 4, r, 1, 64, 16, 31) }},
		{"luma pair H", func(p []byte, s, r int) bool { return filterLumaPairHSIMD(p, s, r+4, 0, 1, 1, 64, 16, 31) }},
		{"luma pair V", func(p []byte, s, r int) bool { return filterLumaPairVSIMD(p, s, 4, r, 1, 1, 64, 16, 31) }},
		{"chroma H", func(p []byte, s, r int) bool { return filterChromaHSIMD(p, s, r+2, 0, 8, &bs, 64, 16, 31) }},
		{"chroma V", func(p []byte, s, r int) bool { return filterChromaVSIMD(p, s, 2, r, 8, &bs, 64, 16, 31) }},
	} {
		t.Run(kernel.name, func(t *testing.T) {
			for _, geometry := range [][2]int{{1 << 62, 0}, {8, 1 << 61}} {
				plane := bytes.Repeat([]byte{110}, 128)
				want := bytes.Clone(plane)
				if kernel.call(plane, geometry[0], geometry[1]) {
					t.Fatalf("accepted overflowing stride/row %v", geometry)
				}
				if !bytes.Equal(plane, want) {
					t.Fatal("rejected footprint modified the plane")
				}
			}
		})
	}
}
