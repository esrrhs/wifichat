package protocol

import (
	"bytes"
	"strings"
	"testing"

	"wifichat/pkg/crypto"
)

func TestFramesAndMicroFastPath(t *testing.T) {
	ck := crypto.DeriveChannelKeys("room-secret")
	alice, _ := crypto.NewIdentity("alice")
	bodySize := 22 // ASCII91 26B - 4B frame MAC

	// 1-SSID MicroFrame
	mb, mid, ok := TryEncodeMicroBody(ck, alice.Public().PeerID, alice.NextSalt(), "收到!", bodySize)
	if !ok {
		t.Fatal("expected short UTF-8 message to fit in 1-SSID MicroFrame")
	}
	pf, err := DecodeBody(ck, mb)
	if err != nil || pf.Type != FrameMicro || pf.Micro.Text != "收到!" || pf.Micro.MicroID != mid {
		t.Fatalf("DecodeBody Micro failed: %+v err=%v", pf, err)
	}

	// Long non-compressible string should fall back (ok == false).
	if _, _, ok := TryEncodeMicroBody(ck, alice.Public().PeerID, alice.NextSalt(), "abcdefghijklmnopqrstuvwxyz0123456789", bodySize); ok {
		t.Fatal("expected long message to require multi-chunk DataFrame")
	}

	// DataFrame roundtrip
	chunk := bytes.Repeat([]byte{0x5A}, ChunkDataSize(bodySize))
	db, err := EncodeDataBody(DataFrame{
		XferID:   0xBEEF,
		Gen:      1234,
		FinalGen: true,
		K:        42,
		Seq:      99,
		Chunk:    chunk,
	}, bodySize)
	if err != nil {
		t.Fatal(err)
	}
	dpf, err := DecodeBody(ck, db)
	if err != nil || dpf.Type != FrameData {
		t.Fatalf("DecodeBody Data failed: %v", err)
	}
	if dpf.Data.XferID != 0xBEEF || dpf.Data.Gen != 1234 || !dpf.Data.FinalGen || dpf.Data.K != 42 || dpf.Data.Seq != 99 {
		t.Fatalf("DataFrame header mismatch: %+v", dpf.Data)
	}

	// AckFrame roundtrip
	ab, err := EncodeAckBody(AckFrame{
		XferID:    0xBEEF,
		PeerID:    alice.Public().PeerID,
		BaseGen:   7,
		GenBitmap: 0xDEADBEEF,
		BaseNeed:  3,
		Complete:  true,
	}, bodySize)
	if err != nil {
		t.Fatal(err)
	}
	apf, err := DecodeBody(ck, ab)
	if err != nil || apf.Type != FrameAck || !apf.Ack.Complete || apf.Ack.GenBitmap != 0xDEADBEEF || apf.Ack.BaseNeed != 3 {
		t.Fatalf("AckFrame mismatch: %+v err=%v", apf, err)
	}
}

func TestEnvelopeCompressionAndDirectE2EE(t *testing.T) {
	ck := crypto.DeriveChannelKeys("shared-room")
	alice, _ := crypto.NewIdentity("alice")
	bob, _ := crypto.NewIdentity("bob")
	eve, _ := crypto.NewIdentity("eve")

	// Compressible repetitive text broadcast
	longText := strings.Repeat("匿名WiFi热点通信测试! ", 80)
	env, err := SealText(SealOptions{XferID: 10, Sender: alice, Channel: ck}, longText)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) >= len(longText) {
		t.Fatalf("expected flate compression to shrink envelope: env=%d raw=%d", len(env), len(longText))
	}
	// Simulate trailing zero padding from the final FEC chunk
	padded := append(append([]byte(nil), env...), make([]byte, 13)...)
	dec, err := OpenEnvelope(10, bob, ck, padded)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Compressed || dec.Text.Text != longText || dec.Text.Nick != "alice" {
		t.Fatalf("unexpected decoded broadcast text: %+v", dec.Text)
	}

	// Direct X25519 E2EE binary file (Eve in the same room must NOT be able to decrypt!)
	fileData := []byte{0x00, 0xFF, 0x10, 0x20, 0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	dmEnv, err := SealFile(SealOptions{
		XferID:        77,
		Sender:        alice,
		Channel:       ck,
		Direct:        true,
		RecipientID:   bob.Public().PeerID,
		RecipientXPub: bob.Public().X25519Pub,
	}, "../../etc/secret.png", fileData)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := OpenEnvelope(77, eve, ck, dmEnv); err != ErrNotForUs {
		t.Fatalf("expected Eve to get ErrNotForUs, got %v", err)
	}

	bobDec, err := OpenEnvelope(77, bob, ck, dmEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !bobDec.Direct || bobDec.File.FileName != "secret.png" || !bytes.Equal(bobDec.File.Data, fileData) {
		t.Fatalf("unexpected decoded file: %+v", bobDec.File)
	}
	if bobDec.SenderPub != alice.Public().X25519Pub {
		t.Fatal("Bob should learn Alice's verified X25519 public key from Direct envelope")
	}
}

func TestEnvelopeSecurityChecksAndSanitizeFileName(t *testing.T) {
	ck := crypto.DeriveChannelKeys("sec-test")
	alice, _ := crypto.NewIdentity("alice")
	bob, _ := crypto.NewIdentity("bob")

	// 1. Hello roundtrip
	helloRaw, err := SealHello(SealOptions{XferID: 3, Sender: alice, Channel: ck}, 8)
	if err != nil {
		t.Fatal(err)
	}
	hDec, err := OpenEnvelope(3, bob, ck, helloRaw)
	if err != nil || hDec.Kind != KindHello || hDec.Hello.Slots != 8 || hDec.SenderPub != alice.Public().X25519Pub {
		t.Fatalf("Hello roundtrip failed: %+v err=%v", hDec, err)
	}

	// 2. Compressible MicroFrame (20 identical chars > 11B raw, compresses into 1-SSID MicroFrame!)
	mb, _, ok := TryEncodeMicroBody(ck, alice.Public().PeerID, alice.NextSalt(), strings.Repeat("A", 20), 22)
	if !ok {
		t.Fatal("expected repetitive 20-byte string to compress into 1-SSID MicroFrame")
	}
	mpf, err := DecodeBody(ck, mb)
	if err != nil || mpf.Micro.Text != strings.Repeat("A", 20) {
		t.Fatalf("compressed MicroFrame decode mismatch: %+v err=%v", mpf, err)
	}

	// 3. Stale timestamp rejection (replay defense)
	staleInner := encodeText(TextPayload{
		Timestamp: 1000, // 1970 -> expired
		Nick:      "alice",
		Text:      "replayed old beacon",
	})
	staleEnv, err := sealRawEnvelope(SealOptions{XferID: 9, Sender: alice, Channel: ck}, KindText, staleInner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenEnvelope(9, bob, ck, staleEnv); err != ErrReplayTimestamp {
		t.Fatalf("expected ErrReplayTimestamp, got %v", err)
	}

	// 4. Corrupted file SHA-256 detection
	badFileInner := encodeFile(FilePayload{
		Timestamp: hDec.Hello.Timestamp,
		Nick:      "alice",
		FileName:  "a.bin",
		SHA256:    [32]byte{1, 2, 3}, // wrong digest
		Data:      []byte("actual bytes"),
	})
	badFileEnv, err := sealRawEnvelope(SealOptions{XferID: 12, Sender: alice, Channel: ck}, KindFile, badFileInner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenEnvelope(12, bob, ck, badFileEnv); err != ErrFileIntegrity {
		t.Fatalf("expected ErrFileIntegrity, got %v", err)
	}

	// 5. Filename traversal & control char sanitization
	cases := map[string]string{
		"../../../etc/passwd":             "passwd",
		`C:\Windows\System32\drivers\etc`: "etc",
		"...":                             "received.bin",
		"":                                "received.bin",
		"bad\x00name:1.txt":               "bad_name_1.txt",
	}
	for in, want := range cases {
		if got := SanitizeFileName(in); got != want {
			t.Fatalf("SanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
