package ssid

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
)

func TestASCII91CapacityAndRoundTrip(t *testing.T) {
	c, err := NewCodec(ModeASCII91, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.RawBytes() != 26 {
		t.Fatalf("expected 26 raw bytes in 32-char ASCII91, got %d", c.RawBytes())
	}

	// Boundary cases: all-zero, all-0xFF, and 1000 random frames.
	cases := [][]byte{
		bytes.Repeat([]byte{0x00}, 26),
		bytes.Repeat([]byte{0xFF}, 26),
	}
	for i := 0; i < 1000; i++ {
		b := make([]byte, 26)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, b)
	}

	for idx, orig := range cases {
		enc, err := c.Encode(orig)
		if err != nil {
			t.Fatalf("case %d encode: %v", idx, err)
		}
		if len(enc) != MaxSSIDBytes {
			t.Fatalf("case %d encoded len=%d, want 32", idx, len(enc))
		}
		if strings.ContainsAny(enc, "\"'\\ \t\r\n") {
			t.Fatalf("case %d encoded unsafe chars: %q", idx, enc)
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatalf("case %d decode: %v", idx, err)
		}
		if !bytes.Equal(dec, orig) {
			t.Fatalf("case %d mismatch: got %x want %x", idx, dec, orig)
		}
	}
}

func TestASCII91OverflowRejected(t *testing.T) {
	c, err := NewCodec(ModeASCII91, "")
	if err != nil {
		t.Fatal(err)
	}
	// Highest base91 digit repeated 32 times equals 91^32 - 1 > 256^26 - 1.
	maxChar := string([]byte{alphabet91[90]})
	over := strings.Repeat(maxChar, 32)
	if _, err := c.Decode(over); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow for 91^32-1, got %v", err)
	}
}

func TestPrefixCamouflageAndRaw32(t *testing.T) {
	c, err := NewCodec(ModeASCII91, "CMCC-")
	if err != nil {
		t.Fatal(err)
	}
	if c.RawBytes() != 21 {
		t.Fatalf("expected 21 raw bytes with 5-char prefix, got %d", c.RawBytes())
	}
	raw := make([]byte, c.RawBytes())
	_, _ = rand.Read(raw)
	s, err := c.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "CMCC-") || len(s) != 32 {
		t.Fatalf("unexpected camouflaged SSID: %q", s)
	}
	got, err := c.Decode(s)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("decode camouflaged SSID failed: %v", err)
	}

	// Ordinary Wi-Fi names must be rejected cleanly.
	for _, normal := range []string{"Starbucks WiFi", "CMCC-402", "TP-LINK_1234", strings.Repeat("A", 31)} {
		if _, err := c.Decode(normal); err == nil {
			t.Fatalf("ordinary SSID %q should be rejected", normal)
		}
	}

	// Raw32 mode supports full 32 bytes including NUL bytes.
	rc, err := NewCodec(ModeRaw32, "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.RawBytes() != 32 {
		t.Fatalf("expected 32 raw bytes in raw32, got %d", rc.RawBytes())
	}
	r32 := make([]byte, 32)
	r32[0] = 0x00
	r32[15] = 0xFF
	r32[31] = 0x00
	es, err := rc.Encode(r32)
	if err != nil {
		t.Fatal(err)
	}
	dr, err := rc.Decode(es)
	if err != nil || !bytes.Equal(dr, r32) {
		t.Fatalf("raw32 roundtrip failed: %v", err)
	}
}

func TestInvalidPrefixRejected(t *testing.T) {
	if _, err := NewCodec(ModeASCII91, strings.Repeat("X", 20)); err == nil {
		t.Fatal("expected overly long prefix to fail")
	}
	if _, err := NewCodec(ModeASCII91, `bad"quote`); err == nil {
		t.Fatal("expected quote in prefix to fail")
	}
	if _, err := NewCodec(Mode("bogus"), ""); err == nil {
		t.Fatal("expected unsupported mode to fail")
	}

	def, err := NewCodec("", "WC-")
	if err != nil {
		t.Fatal(err)
	}
	if def.Mode() != ModeASCII91 || def.Prefix() != "WC-" || def.SSIDLength() != MaxSSIDBytes {
		t.Fatalf("unexpected codec metadata: mode=%s prefix=%s len=%d", def.Mode(), def.Prefix(), def.SSIDLength())
	}
	if _, err := def.Encode([]byte("short")); err == nil {
		t.Fatal("expected wrong raw length to fail Encode")
	}
	// Space inside ASCII91 payload must be rejected
	badCharSSID := "WC-" + strings.Repeat(" ", 29)
	if _, err := def.Decode(badCharSSID); err != ErrInvalidSSID {
		t.Fatalf("expected ErrInvalidSSID for space char, got %v", err)
	}

	decoy := RandomDecoySSID()
	if len(decoy) < 6 || len(decoy) > 32 {
		t.Fatalf("unexpected decoy SSID: %q", decoy)
	}
}
