package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCLIEndToEndTextAndBinaryFileOverVirtualAir(t *testing.T) {
	airDir := t.TempDir()
	dlDir := t.TempDir()

	// 1. Help & drivers subcommands
	var out bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
	if !strings.Contains(out.String(), "WiFiChat") {
		t.Fatalf("unexpected help output: %s", out.String())
	}

	out.Reset()
	if code := run([]string{"drivers"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatalf("drivers exit=%d", code)
	}
	if !strings.Contains(out.String(), "sim") {
		t.Fatalf("drivers missing sim: %s", out.String())
	}

	// 2. Passive `recv --once` + `send` over shared virtual 802.11 SSID directory
	var recvOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		code := run([]string{
			"--driver", "sim",
			"--sim-dir", airDir,
			"--key", "cli-pass",
			"--scan-interval", "10ms",
			"--once",
			"recv",
		}, strings.NewReader(""), &recvOut, &recvOut)
		if code != 0 {
			t.Errorf("recv exit=%d out=%s", code, recvOut.String())
		}
	}()

	time.Sleep(40 * time.Millisecond)
	var sendOut bytes.Buffer
	if code := run([]string{
		"--driver", "sim",
		"--sim-dir", airDir,
		"--key", "cli-pass",
		"--dwell", "15ms",
		"send", "紧急集合!",
	}, strings.NewReader(""), &sendOut, &sendOut); code != 0 {
		t.Fatalf("send exit=%d: %s", code, sendOut.String())
	}
	wg.Wait()
	if !strings.Contains(recvOut.String(), "紧急集合!") {
		t.Fatalf("receiver missed text message: %s", recvOut.String())
	}

	// 3. Binary file transfer via `sendfile` -> `recv --once`
	srcFile := filepath.Join(t.TempDir(), "archive.bin")
	origBytes := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x7F, 0x80, 0xFF}
	origBytes = append(origBytes, bytes.Repeat([]byte("BINARY_PAYLOAD_BLOCK_"), 120)...)
	if err := os.WriteFile(srcFile, origBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	var fileRecvOut bytes.Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		code := run([]string{
			"--driver", "sim",
			"--sim-dir", airDir,
			"--key", "cli-pass",
			"--download-dir", dlDir,
			"--scan-interval", "10ms",
			"--once",
			"recv",
		}, strings.NewReader(""), &fileRecvOut, &fileRecvOut)
		if code != 0 {
			t.Errorf("file recv exit=%d out=%s", code, fileRecvOut.String())
		}
	}()

	time.Sleep(40 * time.Millisecond)
	var fileSendOut bytes.Buffer
	if code := run([]string{
		"--driver", "sim",
		"--sim-dir", airDir,
		"--key", "cli-pass",
		"--slots", "4",
		"--dwell", "12ms",
		"sendfile", srcFile,
	}, strings.NewReader(""), &fileSendOut, &fileSendOut); code != 0 {
		t.Fatalf("sendfile exit=%d: %s", code, fileSendOut.String())
	}
	wg.Wait()

	receivedPath := filepath.Join(dlDir, "archive.bin")
	savedBytes, err := os.ReadFile(receivedPath)
	if err != nil {
		t.Fatalf("expected saved file at %s: %v (out=%s)", receivedPath, err, fileRecvOut.String())
	}
	if !bytes.Equal(savedBytes, origBytes) {
		t.Fatal("CLI received binary file mismatch")
	}

	// 4. Test flags placed AFTER subcommand (`wifichat recv --once ...`) and error exit codes
	var afterRecv bytes.Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		code := run([]string{
			"recv",
			"--driver", "sim",
			"--sim-dir", airDir,
			"-k", "after-flag-key",
			"--scan-interval=10ms",
			"--once",
		}, strings.NewReader(""), &afterRecv, &afterRecv)
		if code != 0 {
			t.Errorf("reordered recv exit=%d", code)
		}
	}()
	time.Sleep(40 * time.Millisecond)
	var afterSend bytes.Buffer
	if code := run([]string{
		"send", "flags-after-cmd-ok",
		"--driver", "sim",
		"--sim-dir", airDir,
		"-k", "after-flag-key",
		"--dwell", "12ms",
	}, strings.NewReader(""), &afterSend, &afterSend); code != 0 {
		t.Fatalf("reordered send exit=%d: %s", code, afterSend.String())
	}
	wg.Wait()
	if !strings.Contains(afterRecv.String(), "flags-after-cmd-ok") {
		t.Fatalf("missed message with trailing flags: %s", afterRecv.String())
	}

	// Unknown command & missing args checks
	var errBuf bytes.Buffer
	if code := run([]string{"unknown-subcmd"}, strings.NewReader(""), &errBuf, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit code for unknown subcommand")
	}
	if code := run([]string{"send"}, strings.NewReader(""), &errBuf, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit code for empty send")
	}
	if code := run([]string{"sendfile"}, strings.NewReader(""), &errBuf, &errBuf); code == 0 {
		t.Fatal("expected non-zero exit code for empty sendfile")
	}
}
