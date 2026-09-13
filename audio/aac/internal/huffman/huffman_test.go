package huffman

import (
	"fmt"
	"testing"

	"github.com/rcarmo/go-264/audio/aac/internal/aacbits"
)

type writer struct {
	b []byte
	n int
}

func (w *writer) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[w.n/8] |= byte((v>>i)&1) << uint(7-w.n%8)
		w.n++
	}
}
func decodeSpectralIndexArithmetic(spec spectralSpec, idx int) Tuple {
	modulus, offset := spec.LAV+1, 0
	if !spec.Unsigned {
		modulus, offset = 2*spec.LAV+1, spec.LAV
	}
	out := Tuple{Count: spec.Dimension}
	remaining := idx
	for i := spec.Dimension - 1; i >= 0; i-- {
		out.Values[i] = int16(remaining%modulus - offset)
		remaining /= modulus
	}
	return out
}

func TestGeneratedSpectralTuplesMatchArithmetic(t *testing.T) {
	for book := 1; book <= 11; book++ {
		spec := spectralSpecs[book]
		for idx := range spec.table {
			got, err := DecodeSpectralIndex(book, idx)
			want := decodeSpectralIndexArithmetic(spec, idx)
			if err != nil || got != want {
				t.Fatalf("book%d idx%d got=%+v want=%+v err=%v", book, idx, got, want, err)
			}
		}
	}
}

func TestEveryCodeword(t *testing.T) {
	for book := 1; book <= 11; book++ {
		s := spectralSpecs[book]
		for idx, e := range s.table {
			var w writer
			w.put(e.code, int(e.bits))
			expected, _ := DecodeSpectralIndex(book, idx)
			if s.Unsigned {
				for i := 0; i < expected.Count; i++ {
					if expected.Values[i] != 0 {
						sign := uint32(i % 2)
						w.put(sign, 1)
						if sign != 0 {
							expected.Values[i] = -expected.Values[i]
						}
					}
				}
			}
			if s.Escape {
				for i := 0; i < expected.Count; i++ {
					if abs16(expected.Values[i]) == 16 {
						w.put(0, 1)
						w.put(0, 4)
					}
				}
			}
			r := aacbits.New(w.b)
			got, err := ReadSpectral(r, book)
			if err != nil || got != expected || r.Position() != w.n {
				t.Fatalf("book%d idx%d got%+v want%+v pos%d/%d %v", book, idx, got, expected, r.Position(), w.n, err)
			}
		}
	}
}
func TestEveryScalefactor(t *testing.T) {
	for i, e := range scalefactorHCOD {
		var w writer
		w.put(e.code, int(e.bits))
		got, err := ReadScalefactor(aacbits.New(w.b))
		if err != nil || int(got) != i-60 {
			t.Fatal(i, got, err)
		}
	}
}
func TestEscapeAndBounds(t *testing.T) {
	for _, v := range []int{16, 17, 31, 32, 8191} {
		p, w, e := EncodeEscape(v)
		if e != nil {
			t.Fatal(e)
		}
		var b writer
		for i := 0; i < p; i++ {
			b.put(1, 1)
		}
		b.put(0, 1)
		b.put(w, p+4)
		got, e := ReadEscape(aacbits.New(b.b))
		if e != nil || got != v {
			t.Fatal(got, e)
		}
	}
	if _, e := ReadEscape(aacbits.New([]byte{255, 255})); e == nil {
		t.Fatal("escape cap")
	}
	for _, book := range []int{-1, 0, 12} {
		if _, e := ReadSpectral(aacbits.New(nil), book); e == nil {
			t.Fatal(book)
		}
	}
	for b := 1; b <= 11; b++ {
		if _, e := ReadSpectral(aacbits.New(nil), b); e == nil {
			t.Fatal(b)
		}
	}
	if _, e := ReadScalefactor(aacbits.New(nil)); e == nil {
		t.Fatal("empty")
	}
}
func TestPrefixFree(t *testing.T) {
	tables := [][]tableEntry{scalefactorHCOD[:]}
	for i := 1; i <= 11; i++ {
		tables = append(tables, spectralSpecs[i].table)
	}
	for _, table := range tables {
		for i, a := range table {
			for j, b := range table {
				if i != j && a.bits <= b.bits && a.code == b.code>>uint(b.bits-a.bits) {
					t.Fatal("prefix collision", i, j)
				}
			}
		}
	}
}
func FuzzSpectral(f *testing.F) {
	f.Add([]byte{0}, uint8(1))
	f.Add([]byte{255, 255}, uint8(11))
	f.Fuzz(func(t *testing.T, b []byte, book uint8) {
		r := aacbits.New(b)
		_, _ = ReadSpectral(r, int(book))
		if r.Position() > len(b)*8 {
			t.Fatal("overrun")
		}
	})
}

func BenchmarkDecodeSpectralIndex(b *testing.B) {
	for book := 1; book <= 11; book++ {
		spec := spectralSpecs[book]
		b.Run(fmt.Sprintf("book%d", book), func(b *testing.B) {
			var tuple Tuple
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tuple, _ = DecodeSpectralIndex(book, i%len(spec.table))
			}
			if tuple.Count != spec.Dimension {
				b.Fatal(tuple)
			}
		})
	}
}

func TestTreeMatchesScalarDecoder(t *testing.T) {
	for book := 0; book <= 11; book++ {
		table, depth, tree := scalefactorHCOD[:], 19, &scalefactorTree
		if book > 0 {
			table = spectralSpecs[book].table
			depth = spectralSpecs[book].MaxBits
			tree = &spectralTrees[book]
		}
		for _, entry := range table {
			var w writer
			w.put(entry.code, int(entry.bits))
			a, b := aacbits.New(w.b), aacbits.New(w.b)
			x, e1 := decodeIndex(a, table, depth)
			y, e2 := decodeTree(b, tree)
			if e1 != nil || e2 != nil || x != y || a.Position() != b.Position() {
				t.Fatal(book, x, y, e1, e2)
			}
		}
	}
}
