package cavlc

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/rcarmo/go-264/nal"
)

// Include malformed/truncated data, EPBs, every starting bit alignment, prior
// latched errors and subsequent reads. The legacy path is unchanged, so a
// speculative failure must preserve all externally visible reader state.
func TestWordBlockDifferential(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fast     func(*nal.Reader, int) (Block4x4, int, bool)
		fallback func(*nal.Reader, int) (Block4x4, int)
	}{
		{"full", decodeCAVLCBlockWord, decodeCAVLCBlockFallback},
		{"AC", decodeCAVLCBlockACWord, decodeCAVLCBlockACFallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(26464))
			fast, nonzero := 0, 0
			for n := 0; n < 100000; n++ {
				data := make([]byte, n%41)
				rng.Read(data)
				if len(data) > 4 && n%3 == 0 {
					at := rng.Intn(len(data) - 2)
					copy(data[at:], []byte{0, 0, 3})
				}
				a, b := nal.NewReader(data), nal.NewReader(data)
				for i := 0; i < n%8; i++ {
					a.ReadBit()
					b.ReadBit()
				}
				if n%29 == 0 {
					a.Fail(nal.ErrInvalidSyntax)
					b.Fail(nal.ErrInvalidSyntax)
				}
				for j := 0; j < 3; j++ {
					nC := []int{-1, 0, 1, 2, 3, 4, 7, 8, 16}[n%9]
					pos, err := a.Position(), fmt.Sprint(a.Err())
					got, gtc, ok := tc.fast(a, nC)
					if !ok {
						if a.Position() != pos || fmt.Sprint(a.Err()) != err {
							t.Fatal("failed speculation consumed input")
						}
						got, gtc = tc.fallback(a, nC)
					} else {
						fast++
						if gtc > 0 {
							nonzero++
						}
					}
					want, wtc := tc.fallback(b, nC)
					if got != want || gtc != wtc || a.Position() != b.Position() || fmt.Sprint(a.Err()) != fmt.Sprint(b.Err()) {
						t.Fatalf("n=%d j=%d data=%x nc=%d fast=%v got=%v/%d want=%v/%d positions=%d/%d errors=%v/%v", n, j, data, nC, ok, got, gtc, want, wtc, a.Position(), b.Position(), a.Err(), b.Err())
					}
				}
				if a.ReadBits(19) != b.ReadBits(19) || a.Position() != b.Position() || fmt.Sprint(a.Err()) != fmt.Sprint(b.Err()) {
					t.Fatal("continuation differs")
				}
			}
			t.Logf("fast successes %d, nonzero blocks %d", fast, nonzero)
			if nonzero == 0 {
				t.Fatal("test never exercised a nonzero fast block")
			}
		})
	}
}
