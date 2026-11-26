package crypto

import (
	"bytes"
	"testing"
)

func TestEphemeralIdentityRotateAndECDH(t *testing.T) {
	alice, err := NewIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewIdentity("Bob")
	if err != nil {
		t.Fatal(err)
	}
	ap1 := alice.Public()
	bp := bob.Public()
	if ap1.PeerID.IsZero() || bp.Nick != "Bob" {
		t.Fatalf("unexpected public identity: %+v / %+v", ap1, bp)
	}

	s1, err := alice.ECDH(bp.X25519Pub)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := bob.ECDH(ap1.X25519Pub)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("X25519 ECDH shared secrets do not match")
	}
	if SAS(ap1.X25519Pub, bp.X25519Pub) != SAS(bp.X25519Pub, ap1.X25519Pub) {
		t.Fatal("SAS must be symmetric")
	}

	// Rotate must generate a brand-new anonymous identity and break linkability.
	if err := alice.Rotate(); err != nil {
		t.Fatal(err)
	}
	ap2 := alice.Public()
	if ap2.X25519Pub == ap1.X25519Pub {
		t.Fatal("expected new X25519 key after Rotate")
	}
	if ap2.Nick == ap1.Nick && ap2.PeerID == ap1.PeerID {
		t.Fatal("expected rotated auto-nick and PeerID")
	}
}

func TestChannelKeysFrameSIVAndAEAD(t *testing.T) {
	k1 := DeriveChannelKeys("correct-horse-battery-staple")
	k2 := DeriveChannelKeys("wrong-passphrase")
	if k1.IsOpen() {
		t.Fatal("passphrase channel should not report IsOpen")
	}

	body := []byte("0123456789abcdefghijkl") // 22 bytes
	raw, err := k1.SealFrame(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("012345")) {
		t.Fatal("plaintext leaked into SIV frame")
	}
	got, err := k1.OpenFrame(raw)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("OpenFrame failed: %v", err)
	}

	// Wrong key and bit-flips must fail authentication.
	if _, err := k2.OpenFrame(raw); err != ErrFrameAuth {
		t.Fatalf("expected ErrFrameAuth under wrong key, got %v", err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := k1.OpenFrame(tampered); err != ErrFrameAuth {
		t.Fatalf("expected ErrFrameAuth on tampered frame, got %v", err)
	}

	// AEAD test
	ct, err := SealAEAD(k1.RoomKey(), []byte("seed1"), []byte("secret file bytes"), []byte("header-ad"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := OpenAEAD(k1.RoomKey(), []byte("seed1"), ct, []byte("header-ad"))
	if err != nil || string(pt) != "secret file bytes" {
		t.Fatalf("OpenAEAD failed: %v", err)
	}
	if _, err := OpenAEAD(k1.RoomKey(), []byte("seed1"), ct, []byte("tampered-ad")); err != ErrAEADAuth {
		t.Fatalf("expected ErrAEADAuth on AD tamper, got %v", err)
	}
}

func TestNickSanitizationOpenChannelAndMicroMAC(t *testing.T) {
	openCK := DeriveChannelKeys("")
	if !openCK.IsOpen() {
		t.Fatal("empty passphrase must report IsOpen() == true")
	}

	// Multibyte UTF-8 truncation + control/space stripping + reset to auto-nick
	id, err := NewIdentity("  昵称测试超长截断验证ABC\n\t ")
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Public().Nick) > MaxNickBytes {
		t.Fatalf("nick exceeded MaxNickBytes: %d", len(id.Public().Nick))
	}
	id.SetNick("")
	if len(id.Public().Nick) == 0 || id.Public().PeerID.Hex() == "" {
		t.Fatal("expected auto-nick fallback on empty SetNick")
	}
	id.SetNick("alice")
	if id.Public().Nick != "alice" {
		t.Fatalf("got nick %q, want alice", id.Public().Nick)
	}

	// Frame boundary checks
	if _, err := openCK.SealFrame(nil); err == nil {
		t.Fatal("expected empty SealFrame error")
	}
	if _, err := openCK.SealFrame(make([]byte, 33)); err == nil {
		t.Fatal("expected oversized SealFrame error")
	}
	if _, err := openCK.OpenFrame([]byte{1, 2, 3}); err != ErrFrameAuth {
		t.Fatal("expected short OpenFrame error")
	}

	// Micro SIV encryption & tamper check
	hdr7 := []byte{0xC5, 0xAA, 0xBB, 1, 2, 3, 4}
	mac, ct, err := openCK.SealMicroPayload(hdr7, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := openCK.OpenMicroPayload(hdr7, mac, ct)
	if err != nil || string(pt) != "hello" {
		t.Fatalf("OpenMicroPayload failed: %v", err)
	}
	badMAC := mac
	badMAC[0] ^= 0xFF
	if _, err := openCK.OpenMicroPayload(hdr7, badMAC, ct); err != ErrMicroAuth {
		t.Fatalf("expected ErrMicroAuth on bad MAC, got %v", err)
	}
}
