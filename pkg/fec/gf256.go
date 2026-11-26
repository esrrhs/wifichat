package fec

// GF(2^8) arithmetic with primitive polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11d),
// where 2 is a primitive element of multiplicative order 255.

var (
	expTable [512]uint8
	logTable [256]uint8
	mulTable [256][256]uint8
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		expTable[i] = uint8(x)
		expTable[i+255] = uint8(x)
		logTable[x] = uint8(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for a := 1; a < 256; a++ {
		la := int(logTable[a])
		for b := 1; b < 256; b++ {
			mulTable[a][b] = expTable[la+int(logTable[b])]
		}
	}
}

func gfMul(a, b uint8) uint8 {
	return mulTable[a][b]
}

func gfInv(a uint8) uint8 {
	if a == 0 {
		panic("fec: division by zero in GF(256)")
	}
	return expTable[255-int(logTable[a])]
}

// gfMulAddSlice computes dst[i] ^= c * src[i] for all i.
func gfMulAddSlice(dst, src []byte, c uint8) {
	if c == 0 {
		return
	}
	if c == 1 {
		for i := range dst {
			dst[i] ^= src[i]
		}
		return
	}
	row := &mulTable[c]
	for i := range dst {
		dst[i] ^= row[src[i]]
	}
}

// gfScaleSlice computes dst[i] *= c for all i.
func gfScaleSlice(dst []byte, c uint8) {
	if c == 1 {
		return
	}
	row := &mulTable[c]
	for i := range dst {
		dst[i] = row[dst[i]]
	}
}
