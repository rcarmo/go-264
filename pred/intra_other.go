//go:build !amd64

package pred

var HasSSE2 = false

func IntraPred16x16DC_ASM(pred *uint8, dc uint8)   { panic("no SSE2") }
func IntraPred16x16V_ASM(pred *uint8, top *uint8)   { panic("no SSE2") }
func IntraPred16x16H_ASM(pred *uint8, left *uint8)  { panic("no SSE2") }
