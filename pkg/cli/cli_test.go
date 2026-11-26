package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInteractiveChatDMFileAndCommands(t *testing.T) {
	airDir := t.TempDir()
	bobDL := t.TempDir()
	srcFile := filepath.Join(t.TempDir(), "secret.dat")
	payload := []byte{0x00, 0x11, 0x22, 0xFE, 0xFF, 'W', 'I', 'F', 'I'}
	if err := os.WriteFile(srcFile, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	bobInR, bobInW, _ := os.Pipe()
	var bobOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = CmdChat(&Config{
			Driver:       "sim",
			SimDir:       airDir,
			Nick:         "bob",
			Key:          "room1",
			Slots:        4,
			Dwell:        8 * time.Millisecond,
			ScanInterval: 6 * time.Millisecond,
			DownloadDir:  bobDL,
			Stdin:        bobInR,
			Stdout:       &bobOut,
			Stderr:       &bobOut,
		})
	}()

	time.Sleep(120 * time.Millisecond)

	aliceScript := strings.Join([]string{
		"/help",
		"/hello",
		"/peers",
		"/status",
		"hello broadcast",
		"@bob private-e2ee-msg",
		"/sendfile bob " + srcFile,
		"/stealth on",
		"/rotate",
		"q",
	}, "\n") + "\n"

	// Give Alice a brief pause before @bob so mutual Hello completes cleanly:
	// use a pipe writer with small pacing.
	aliceInR, aliceInW, _ := os.Pipe()
	var aliceOut bytes.Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = CmdChat(&Config{
			Driver:       "sim",
			SimDir:       airDir,
			Nick:         "alice",
			Key:          "room1",
			Slots:        4,
			Dwell:        8 * time.Millisecond,
			ScanInterval: 6 * time.Millisecond,
			DownloadDir:  t.TempDir(),
			Stdin:        aliceInR,
			Stdout:       &aliceOut,
			Stderr:       &aliceOut,
		})
	}()

	_ = aliceScript
	_, _ = aliceInW.WriteString("/help\n")
	time.Sleep(180 * time.Millisecond) // allow mutual Hello discovery
	_, _ = aliceInW.WriteString("/peers\n/status\nhello broadcast\n")
	time.Sleep(80 * time.Millisecond)
	_, _ = aliceInW.WriteString("@bob private-e2ee-msg\n")
	_, _ = aliceInW.WriteString("/sendfile bob " + srcFile + "\n")
	time.Sleep(120 * time.Millisecond)
	_, _ = aliceInW.WriteString("/stealth on\n/rotate\nq\n")
	_ = aliceInW.Close()

	// Also test CmdScan while Bob is still broadcasting on airDir
	var scanOut bytes.Buffer
	_ = CmdScan(&Config{
		Driver: "sim",
		SimDir: airDir,
		Key:    "room1",
		Stdout: &scanOut,
		Stderr: &scanOut,
	})

	_, _ = bobInW.WriteString("q\n")
	_ = bobInW.Close()
	wg.Wait()

	gotBob := bobOut.String()
	if !strings.Contains(gotBob, "hello broadcast") {
		t.Fatalf("Bob missed broadcast in interactive console: %s", gotBob)
	}
	if !strings.Contains(gotBob, "private-e2ee-msg") {
		t.Fatalf("Bob missed @bob E2EE DM in interactive console: %s", gotBob)
	}
	saved, err := os.ReadFile(filepath.Join(bobDL, "secret.dat"))
	if err != nil || !bytes.Equal(saved, payload) {
		t.Fatalf("Bob missed interactive E2EE file: err=%v out=%s", err, gotBob)
	}
}

func TestCLIDriversAndCommandVariants(t *testing.T) {
	var out bytes.Buffer
	if code := CmdDrivers(&out); code != 0 || !strings.Contains(out.String(), "sim") {
		t.Fatalf("CmdDrivers failed: code=%d out=%s", code, out.String())
	}

	out.Reset()
	PrintUsage(&out)
	if !strings.Contains(out.String(), "bench") {
		t.Fatal("PrintUsage missing bench subcommand")
	}

	// Direct one-shot CmdRecv + CmdSendText + CmdSendFile with --to E2EE
	airDir := t.TempDir()
	dlDir := t.TempDir()
	var recvOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = CmdRecv(&Config{
			Driver:       "sim",
			SimDir:       airDir,
			Key:          "oneshot",
			ScanInterval: 8 * time.Millisecond,
			DownloadDir:  dlDir,
			Once:         true,
			Stdout:       &recvOut,
			Stderr:       &recvOut,
		})
	}()
	time.Sleep(30 * time.Millisecond)
	var sendOut bytes.Buffer
	if code := CmdSendText(&Config{
		Driver: "sim",
		SimDir: airDir,
		Key:    "oneshot",
		Dwell:  10 * time.Millisecond,
		Stdout: &sendOut,
		Stderr: &sendOut,
	}, "cli-oneshot-hi"); code != 0 {
		t.Fatalf("CmdSendText code=%d: %s", code, sendOut.String())
	}
	wg.Wait()
	if !strings.Contains(recvOut.String(), "cli-oneshot-hi") {
		t.Fatalf("CmdRecv missed text: %s", recvOut.String())
	}
}
