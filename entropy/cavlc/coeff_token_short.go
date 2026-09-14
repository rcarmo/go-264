package cavlc

// Short coeff_token codes need only an eight-bit lookup. Keeping those 2 KiB
// hot avoids indexing 512 KiB with unrelated following residual bits. Longer
// codes still use the original complete table, with the same invalid sentinel.
var coeffTokenShortLookup [4][256]uint16

func init() {
	lens := [4]*[68]uint8{&ctLen0, &ctLen1, &ctLen2, &ctLen3}
	codes := [4]*[68]uint8{&ctBits0, &ctBits1, &ctBits2, &ctBits3}
	for table := range lens {
		for i, length := range lens[table] {
			n := int(length)
			if n == 0 || n > 8 {
				continue
			}
			prefix := int(codes[table][i]) << uint(8-n)
			for tail := 0; tail < 1<<uint(8-n); tail++ {
				coeffTokenShortLookup[table][prefix|tail] = uint16(n<<8 | i)
			}
		}
	}
}
