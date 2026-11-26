package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"wifichat/pkg/ssid"
	"wifichat/pkg/wifi"
)

func startNode(t *testing.T, med *wifi.SimMedium, slots int, opts Options) *Engine {
	t.Helper()
	opts.Driver = med.NewDriver(slots, 6)
	opts.DownloadDir = t.TempDir()
	opts.Dwell = 4 * time.Millisecond
	opts.ScanInterval = 3 * time.Millisecond
	opts.HelloInterval = 5 * time.Second
	opts.MicroHoldTicks = 5
	eng, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	return eng
}

func waitPeers(t *testing.T, a, b *Engine) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		ap, bp := a.ListPeers(), b.ListPeers()
		if len(ap) > 0 && ap[0].HasPubKey && len(bp) > 0 && bp[0].HasPubKey {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for mutual SSID peer discovery (a=%d b=%d)", len(a.ListPeers()), len(b.ListPeers()))
}

func TestMicroAndLongChatAndE2EEPrivacy(t *testing.T) {
	med := wifi.NewSimMedium(wifi.SimConfig{LossProb: 0.10, Seed: 7})

	var (
		mu       sync.Mutex
		bobMsgs  []ChatMessage
		eveMsgs  []ChatMessage
		outMsgs  []ChatMessage
	)

	alice := startNode(t, med, 2, Options{Nick: "alice", Passphrase: "secret-room"})
	bob := startNode(t, med, 2, Options{
		Nick:       "bob",
		Passphrase: "secret-room",
		OnChat: func(m ChatMessage) {
			mu.Lock()
			bobMsgs = append(bobMsgs, m)
			mu.Unlock()
		},
	})
	// Eve is in the SAME room passphrase, so she can see broadcasts, but MUST NOT see Alice->Bob Direct E2EE!
	eve := startNode(t, med, 1, Options{
		Nick:       "eve",
		Passphrase: "secret-room",
		OnChat: func(m ChatMessage) {
			mu.Lock()
			eveMsgs = append(eveMsgs, m)
			mu.Unlock()
		},
	})
	// Outsider has wrong passphrase; must see ZERO messages or peers!
	outsider := startNode(t, med, 1, Options{
		Nick:       "outsider",
		Passphrase: "wrong-password",
		OnChat: func(m ChatMessage) {
			mu.Lock()
			outMsgs = append(outMsgs, m)
			mu.Unlock()
		},
	})
	_ = eve
	_ = outsider

	waitPeers(t, alice, bob)

	ctx := context.Background()
	// 1. Short 1-SSID MicroFrame broadcast
	if err := alice.SendText(ctx, "hi!"); err != nil {
		t.Fatal(err)
	}

	// 2. Long multi-chunk compressed broadcast
	longMsg := strings.Repeat("附近热点匿名广播测试 — 无需连接WiFi即可高速传输! ", 20)
	if err := alice.SendText(ctx, longMsg); err != nil {
		t.Fatal(err)
	}

	// 3. Direct X25519 E2EE message to Bob (Eve must NOT decrypt!)
	if err := alice.SendDirectText(ctx, "bob", "top-secret-coordinates-42"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(bobMsgs)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bobMsgs) != 3 {
		t.Fatalf("expected Bob to receive 3 messages, got %d: %+v", len(bobMsgs), bobMsgs)
	}
	if !bobMsgs[0].Micro || bobMsgs[0].Text != "hi!" {
		t.Fatalf("expected msg[0] to be 1-SSID MicroFrame 'hi!', got %+v", bobMsgs[0])
	}
	if bobMsgs[1].Text != longMsg {
		t.Fatalf("msg[1] mismatch")
	}
	if !bobMsgs[2].Direct || bobMsgs[2].Text != "top-secret-coordinates-42" {
		t.Fatalf("expected msg[2] to be Direct E2EE, got %+v", bobMsgs[2])
	}

	for _, em := range eveMsgs {
		if strings.Contains(em.Text, "top-secret-coordinates") {
			t.Fatal("Eve decrypted Alice->Bob X25519 E2EE direct message!")
		}
	}
	if len(outMsgs) != 0 || len(outsider.ListPeers()) != 0 {
		t.Fatalf("outsider with wrong Argon2id passphrase saw traffic: msgs=%d peers=%d", len(outMsgs), len(outsider.ListPeers()))
	}
}

func TestBinaryFileTransferUnderHeavyPacketLossAndMultiSlot(t *testing.T) {
	// 30% wireless scan drop rate! Cauchy GF(256) MDS fountain + selective bitmap ACK must recover 100%.
	med := wifi.NewSimMedium(wifi.SimConfig{LossProb: 0.30, Seed: 2026})

	gotFile := make(chan ReceivedFile, 1)
	alice := startNode(t, med, 8, Options{
		Passphrase: "file-key",
		Encoding:   ssid.ModeRaw32,
	})
	bob := startNode(t, med, 4, Options{
		Nick:       "receiver",
		Passphrase: "file-key",
		Encoding:   ssid.ModeRaw32,
		OnFile: func(rf ReceivedFile) {
			gotFile <- rf
		},
	})

	waitPeers(t, alice, bob)

	// 25 KB incompressible random binary file (~18 generations across 8 parallel SSID slots)
	origData := make([]byte, 25*1024+37)
	if _, err := rand.Read(origData); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := alice.SendFileBytes(ctx, "receiver", "../payload.bin", origData); err != nil {
		t.Fatalf("SendFileBytes failed under 30%% loss: %v", err)
	}

	select {
	case rf := <-gotFile:
		if rf.FileName != "payload.bin" || !rf.Direct || rf.Size != len(origData) {
			t.Fatalf("unexpected ReceivedFile metadata: %+v", rf)
		}
		diskBytes, err := os.ReadFile(rf.SavedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(diskBytes, origData) {
			t.Fatal("saved binary file does not match original byte-for-byte!")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnFile delivery")
	}
}

func TestPassiveRXOnlyBlindFileBroadcastAndIdentityRotation(t *testing.T) {
	med := wifi.NewSimMedium(wifi.SimConfig{LossProb: 0.15, Seed: 99})

	gotPassiveFile := make(chan ReceivedFile, 1)
	sender := startNode(t, med, 4, Options{
		Prefix:          "TP-",
		BlindRedundancy: 0.8, // 1.8x MDS parity for passive zero-ACK listener under 15% loss
	})
	passiveListener := startNode(t, med, 1, Options{
		Prefix: "TP-",
		RXOnly: true, // NEVER transmits a single Wi-Fi SSID!
		OnFile: func(rf ReceivedFile) {
			gotPassiveFile <- rf
		},
	})

	oldPub := sender.PublicIdentity()
	newPub, err := sender.RotateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if oldPub.PeerID == newPub.PeerID || oldPub.X25519Pub == newPub.X25519Pub {
		t.Fatal("RotateIdentity failed to rotate ephemeral PeerID and X25519 key")
	}

	fileBytes := append(bytes.Repeat([]byte("DEAD-DROP-HEADER-"), 100), 0x00, 0x01, 0xFE, 0xFF)
	if err := sender.SendFileBytes(context.Background(), "", "dead_drop.dat", fileBytes); err != nil {
		t.Fatal(err)
	}

	select {
	case rf := <-gotPassiveFile:
		saved, err := os.ReadFile(rf.SavedPath)
		if err != nil || !bytes.Equal(saved, fileBytes) {
			t.Fatalf("passive rx-only file mismatch: err=%v", err)
		}
		txCount, _ := passiveListener.FrameCounters()
		if txCount != 0 {
			t.Fatalf("rx-only listener transmitted %d frames, want 0!", txCount)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("passive rx-only listener did not reconstruct broadcast file")
	}
}

func TestKeySwitchStealthAndCollisionSafeFileSave(t *testing.T) {
	med := wifi.NewSimMedium(wifi.SimConfig{Seed: 55})

	gotFiles := make(chan ReceivedFile, 3)
	dlDir := t.TempDir()

	a := startNode(t, med, 4, Options{
		Nick:       "a",
		Passphrase: "initial-key",
		DecoySSID:  "TP-LINK_DECOY",
	})
	bOpts := Options{
		Driver:       med.NewDriver(2, 6),
		Nick:         "b",
		Passphrase:   "different-key",
		DownloadDir:  dlDir,
		Dwell:        4 * time.Millisecond,
		ScanInterval: 3 * time.Millisecond,
		OnFile: func(rf ReceivedFile) {
			gotFiles <- rf
		},
	}
	b, err := New(bOpts)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Stop)

	// Switch both to the same new passphrase dynamically
	a.SetPassphrase("synced-key")
	b.SetPassphrase("synced-key")
	if a.IsOpenChannel() {
		t.Fatal("synced-key should not be open channel")
	}
	waitPeers(t, a, b)

	// Send two files with the EXACT same filename ("dup.bin") -> must never overwrite!
	ctx := context.Background()
	if err := a.SendFileBytes(ctx, "b", "dup.bin", []byte("FIRST_VERSION")); err != nil {
		t.Fatal(err)
	}
	if err := a.SendFileBytes(ctx, "b", "dup.bin", []byte("SECOND_VERSION")); err != nil {
		t.Fatal(err)
	}

	rf1 := <-gotFiles
	rf2 := <-gotFiles
	if rf1.SavedPath == rf2.SavedPath {
		t.Fatalf("expected collision-safe distinct paths, both got %s", rf1.SavedPath)
	}
	b1, _ := os.ReadFile(rf1.SavedPath)
	b2, _ := os.ReadFile(rf2.SavedPath)
	if string(b1) != "FIRST_VERSION" || string(b2) != "SECOND_VERSION" {
		t.Fatalf("saved contents corrupted: %q vs %q", b1, b2)
	}

	// Stealth toggle sets decoy SSID immediately when idle
	a.SetStealth(true)
	cur := a.CurrentSSIDs()
	if len(cur) != 1 || cur[0] != "TP-LINK_DECOY" {
		t.Fatalf("expected decoy SSID in stealth mode, got %v", cur)
	}
}
