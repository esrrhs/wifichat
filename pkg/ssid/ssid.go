package ssid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	// MaxSSIDBytes is the IEEE 802.11 maximum SSID length in octets.
	MaxSSIDBytes = 32

	// MinRawCapacity is the minimum raw byte capacity needed per SSID frame
	// (4-byte SIV MAC + 10-byte Ack/Header minimum).
	MinRawCapacity = 14
)

type Mode string

const (
	// ModeASCII91 encodes 26 raw bytes into 32 safe printable ASCII chars
	// (0x21..0x7E excluding '"', '\'', '\\'). Works across mobile hotspots,
	// nmcli, hostapd, iw, macOS, and Windows without UTF-8 or escaping issues.
	ModeASCII91 Mode = "ascii91"

	// ModeRaw32 packs up to 32 raw binary octets directly into the 32-byte
	// 802.11 SSID IE. Supported by Linux hostapd (ssid2 hex), iw, and sim.
	ModeRaw32 Mode = "raw32"
)

var (
	ErrInvalidMode   = errors.New("ssid: unsupported encoding mode")
	ErrPrefixTooLong = errors.New("ssid: camouflage prefix leaves insufficient frame capacity")
	ErrBadPrefix     = errors.New("ssid: prefix must use safe printable ASCII without quotes or backslashes")
	ErrWrongLength   = errors.New("ssid: input length mismatch")
	ErrInvalidSSID   = errors.New("ssid: not a valid encoded SSID for this codec")
	ErrOverflow      = errors.New("ssid: base91 value exceeds raw byte capacity")
)

var (
	alphabet91 [91]byte
	decode91   [256]int8
)

func init() {
	for i := range decode91 {
		decode91[i] = -1
	}
	idx := 0
	for c := byte(0x21); c <= 0x7E; c++ {
		if c == '"' || c == '\'' || c == '\\' {
			continue
		}
		alphabet91[idx] = c
		decode91[c] = int8(idx)
		idx++
	}
	if idx != 91 {
		panic("ssid: alphabet91 must have exactly 91 characters")
	}
}

// Codec maps fixed-size raw binary frames to/from 32-byte Wi-Fi SSIDs.
type Codec struct {
	mode     Mode
	prefix   string
	availLen int
	rawBytes int
}

// NewCodec creates an SSID frame codec for the given encoding mode and
// optional visible camouflage prefix (e.g. "", "CMCC-", "TP-LINK_").
func NewCodec(mode Mode, prefix string) (*Codec, error) {
	if mode == "" {
		mode = ModeASCII91
	}
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if c < 0x21 || c > 0x7E || c == '"' || c == '\'' || c == '\\' {
			return nil, ErrBadPrefix
		}
	}
	if len(prefix) >= MaxSSIDBytes {
		return nil, ErrPrefixTooLong
	}
	avail := MaxSSIDBytes - len(prefix)
	var rawBytes int
	switch mode {
	case ModeASCII91:
		rawBytes = computeBase91Capacity(avail)
	case ModeRaw32:
		rawBytes = avail
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}
	if rawBytes < MinRawCapacity {
		return nil, fmt.Errorf("%w: prefix %q (%d chars) leaves only %d raw bytes (need >= %d)",
			ErrPrefixTooLong, prefix, len(prefix), rawBytes, MinRawCapacity)
	}
	return &Codec{
		mode:     mode,
		prefix:   prefix,
		availLen: avail,
		rawBytes: rawBytes,
	}, nil
}

func computeBase91Capacity(availChars int) int {
	cap91 := new(big.Int).Exp(big.NewInt(91), big.NewInt(int64(availChars)), nil)
	cur := big.NewInt(1)
	b256 := big.NewInt(256)
	raw := 0
	for {
		next := new(big.Int).Mul(cur, b256)
		if next.Cmp(cap91) <= 0 {
			cur = next
			raw++
		} else {
			break
		}
	}
	return raw
}

func (c *Codec) Mode() Mode      { return c.mode }
func (c *Codec) Prefix() string  { return c.prefix }
func (c *Codec) RawBytes() int   { return c.rawBytes }
func (c *Codec) SSIDLength() int { return MaxSSIDBytes }

// Encode converts a raw frame of length c.RawBytes() into a 32-byte SSID.
func (c *Codec) Encode(raw []byte) (string, error) {
	if len(raw) != c.rawBytes {
		return "", fmt.Errorf("%w: got %d bytes, want %d", ErrWrongLength, len(raw), c.rawBytes)
	}
	if c.mode == ModeRaw32 {
		var out [MaxSSIDBytes]byte
		n := copy(out[:], c.prefix)
		copy(out[n:], raw)
		return string(out[:]), nil
	}

	// Fixed-width big-endian base-91 conversion without heap allocations.
	var work [MaxSSIDBytes]byte
	copy(work[:c.rawBytes], raw)

	var out [MaxSSIDBytes]byte
	copy(out[:len(c.prefix)], c.prefix)

	for pos := MaxSSIDBytes - 1; pos >= len(c.prefix); pos-- {
		rem := 0
		for i := 0; i < c.rawBytes; i++ {
			cur := (rem << 8) | int(work[i])
			work[i] = byte(cur / 91)
			rem = cur % 91
		}
		out[pos] = alphabet91[rem]
	}
	return string(out[:]), nil
}

// Decode parses a 32-byte SSID back into a c.RawBytes()-byte raw frame.
func (c *Codec) Decode(s string) ([]byte, error) {
	if len(s) != MaxSSIDBytes {
		return nil, ErrInvalidSSID
	}
	if len(c.prefix) > 0 && !strings.HasPrefix(s, c.prefix) {
		return nil, ErrInvalidSSID
	}
	payload := s[len(c.prefix):]
	if c.mode == ModeRaw32 {
		out := make([]byte, c.rawBytes)
		copy(out, payload)
		return out, nil
	}

	var acc [MaxSSIDBytes]byte
	for i := 0; i < len(payload); i++ {
		d := decode91[payload[i]]
		if d < 0 {
			return nil, ErrInvalidSSID
		}
		carry := int(d)
		for j := c.rawBytes - 1; j >= 0; j-- {
			cur := int(acc[j])*91 + carry
			acc[j] = byte(cur & 0xFF)
			carry = cur >> 8
		}
		if carry != 0 {
			return nil, ErrOverflow
		}
	}
	out := make([]byte, c.rawBytes)
	copy(out, acc[:c.rawBytes])
	return out, nil
}

var decoyTemplates = []string{
	"TP-LINK_",
	"CMCC-",
	"ChinaNet-",
	"Xiaomi_",
	"HUAWEI-",
	"CU_",
}

// RandomDecoySSID generates an ordinary-looking consumer Wi-Fi SSID for
// idle/stealth camouflage when the user does not specify --decoy.
func RandomDecoySSID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	prefix := decoyTemplates[int(b[0])%len(decoyTemplates)]
	suffix := strings.ToUpper(hex.EncodeToString(b[1:3]))
	return prefix + suffix
}
