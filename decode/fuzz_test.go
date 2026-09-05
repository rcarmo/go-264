package decode

import (
	"strings"
	"testing"
)

func FuzzDecode(f *testing.F) {
	// Reuse small self-contained VCL vectors from the syntax regression tests.
	for _, vector := range decoderSyntaxVectors {
		f.Add(syntaxTestInput(f, vector.name))
	}
	// Exercise picture assembly using the same small wire fixtures as unit tests.
	prefix, _ := firstSyntaxTestSlice(f, "cavlc")
	multi := append(append([]byte(nil), prefix...), assemblyInput(pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149))...)
	if _, err := NewDecoder().Decode(multi); err != nil {
		f.Fatalf("multi-slice seed: %v", err)
	}
	f.Add(multi)
	// Parameter-set and truncated-input seeds.
	f.Add([]byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x1e, 0xd9, 0x01, 0x41, 0xfb, 0x01,
		0x10, 0x00, 0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x03, 0x20, 0xf1, 0x62, 0xe4, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xcb, 0x83, 0xcb, 0x20,
	})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0A})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		dec := NewDecoder()
		dec.MaxFrameMacroblocks = 64
		dec.MaxFrames = 2
		// The outer recovery guard is a last resort, not successful validation.
		if _, err := dec.Decode(data); err != nil && strings.Contains(err.Error(), "decode panic:") {
			t.Fatalf("unchecked malformed input: %v", err)
		}
	})
}
