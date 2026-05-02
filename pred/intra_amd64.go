//go:build amd64

package pred

// IntraPred16x16DC_ASM fills a 16x16 block with the DC value using SSE2.
//go:noescape
func IntraPred16x16DC_ASM(pred *uint8, dc uint8)

// IntraPred16x16V_ASM fills a 16x16 block by replicating the top row.
//go:noescape
func IntraPred16x16V_ASM(pred *uint8, top *uint8)

// IntraPred16x16H_ASM fills a 16x16 block by replicating each left pixel.
//go:noescape
func IntraPred16x16H_ASM(pred *uint8, left *uint8)

var HasSSE2 = true // All amd64 has SSE2
