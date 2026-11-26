package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"wifichat/pkg/engine"
	"wifichat/pkg/ssid"
	"wifichat/pkg/wifi"
)

type BenchResult struct {
	Scenario        string
	Mode            string
	Slots           int
	DwellReal       time.Duration
	ScanReal        time.Duration
	LossPct         int
	PayloadDesc     string
	PayloadBytes    int
	SimRealDuration time.Duration
	EffectiveBps    float64
	TxFrames        uint64
}

// RunBenchmarks executes end-to-end transfers over SimMedium using a 15x accelerated
// virtual clock that preserves exact Dwell/Scan phase ratios, random wireless scan
// drops, compression, ChaCha20-Poly1305/X25519 AEAD, and Cauchy GF(256) MDS repair.
func RunBenchmarks() ([]BenchResult, error) {
	const timeScale = 30 // 30x faster wall-clock while preserving Dwell:Scan ratio

	type spec struct {
		name         string
		enc          ssid.Mode
		slots        int
		dwellReal    time.Duration
		scanReal     time.Duration
		loss         float64
		rxOnly       bool
		isText       bool
		textData     string
		binBytes     int
		compressible bool
		desc         string
	}

	// Realistic non-repetitive multi-paragraph UTF-8 text (~1.2 KB)
	var chatBuilder strings.Builder
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&chatBuilder, "[%02d] 附近热点匿名测试：坐标(31.%04d, 121.%04d)，电量%d%%，信道正常。\n",
			i, 2300+i*17, 4700+i*29, 98-i)
	}
	longChatText := chatBuilder.String()

	specs := []spec{
		{
			name: "Mobile Hotspot (1 slot, ascii91)", enc: ssid.ModeASCII91, slots: 1,
			dwellReal: 850 * time.Millisecond, scanReal: 550 * time.Millisecond, loss: 0.15,
			isText: true, textData: "B2集合!", desc: "Short chat (9B, 1-SSID)",
		},
		{
			name: "Mobile Hotspot (1 slot, ascii91)", enc: ssid.ModeASCII91, slots: 1,
			dwellReal: 850 * time.Millisecond, scanReal: 550 * time.Millisecond, loss: 0.15,
			isText: true, textData: longChatText, desc: "Multi-line UTF-8 text (1.2KB)",
		},
		{
			name: "Mobile Hotspot (1 slot, ascii91)", enc: ssid.ModeASCII91, slots: 1,
			dwellReal: 850 * time.Millisecond, scanReal: 550 * time.Millisecond, loss: 0.15,
			binBytes: 2048, desc: "Random binary (2KB)",
		},
		{
			name: "Linux nmcli + iw (1 slot, ascii91)", enc: ssid.ModeASCII91, slots: 1,
			dwellReal: 650 * time.Millisecond, scanReal: 350 * time.Millisecond, loss: 0.15,
			binBytes: 4096, desc: "Random binary (4KB)",
		},
		{
			name: "Linux hostapd (1 slot, raw32)", enc: ssid.ModeRaw32, slots: 1,
			dwellReal: 160 * time.Millisecond, scanReal: 120 * time.Millisecond, loss: 0.15,
			binBytes: 8192, desc: "Random binary (8KB)",
		},
		{
			name: "Linux hostapd (4 slots, raw32)", enc: ssid.ModeRaw32, slots: 4,
			dwellReal: 160 * time.Millisecond, scanReal: 120 * time.Millisecond, loss: 0.15,
			binBytes: 16384, desc: "Random binary (16KB)",
		},
		{
			name: "Dual-NIC hostapd (8 slots, raw32)", enc: ssid.ModeRaw32, slots: 8,
			dwellReal: 120 * time.Millisecond, scanReal: 80 * time.Millisecond, loss: 0.10,
			binBytes: 32768, desc: "Random binary (32KB, 10% loss)",
		},
		{
			name: "Dual-NIC hostapd (8 slots, raw32)", enc: ssid.ModeRaw32, slots: 8,
			dwellReal: 120 * time.Millisecond, scanReal: 80 * time.Millisecond, loss: 0.30,
			binBytes: 32768, desc: "Random binary (32KB, 30% loss)",
		},
		{
			name: "Dual-NIC hostapd (8 slots, raw32)", enc: ssid.ModeRaw32, slots: 8,
			dwellReal: 120 * time.Millisecond, scanReal: 80 * time.Millisecond, loss: 0.10,
			binBytes: 32768, compressible: true, desc: "Log / JSON file (32KB)",
		},
		{
			name: "Stealth RX-Only (8 slots, raw32)", enc: ssid.ModeRaw32, slots: 8,
			dwellReal: 120 * time.Millisecond, scanReal: 80 * time.Millisecond, loss: 0.15,
			rxOnly: true, binBytes: 16384, desc: "Blind passive RX binary (16KB)",
		},
	}

	var results []BenchResult

	for idx, sp := range specs {
		med := wifi.NewSimMedium(wifi.SimConfig{
			LossProb: sp.loss,
			Seed:     uint64(1000 + idx),
		})
		dlDir, err := os.MkdirTemp("", "wc-bench-*")
		if err != nil {
			return nil, err
		}

		dwellScaled := sp.dwellReal / timeScale
		if dwellScaled < 2*time.Millisecond {
			dwellScaled = 2 * time.Millisecond
		}
		scanScaled := sp.scanReal / timeScale
		if scanScaled < 2*time.Millisecond {
			scanScaled = 2 * time.Millisecond
		}
		actualScale := float64(sp.dwellReal) / float64(dwellScaled)

		gotChat := make(chan time.Time, 1)
		gotFile := make(chan time.Time, 1)

		txEng, err := engine.New(engine.Options{
			Driver:          med.NewDriver(sp.slots, 6),
			Nick:            "sender",
			Passphrase:      "",
			Encoding:        sp.enc,
			Dwell:           dwellScaled,
			ScanInterval:    scanScaled,
			HelloInterval:   30 * time.Second,
			MicroHoldTicks:  4,
			BlindRedundancy: 0.55,
			DownloadDir:     dlDir,
		})
		if err != nil {
			_ = os.RemoveAll(dlDir)
			return nil, err
		}

		rxSlots := 1
		if sp.slots > 2 {
			rxSlots = 2
		}
		rxEng, err := engine.New(engine.Options{
			Driver:        med.NewDriver(rxSlots, 6),
			Nick:          "receiver",
			Passphrase:    "",
			Encoding:      sp.enc,
			RXOnly:        sp.rxOnly,
			Dwell:         dwellScaled,
			ScanInterval:  scanScaled,
			HelloInterval: 30 * time.Second,
			DownloadDir:   dlDir,
			OnChat: func(_ engine.ChatMessage) {
				select {
				case gotChat <- time.Now():
				default:
				}
			},
			OnFile: func(_ engine.ReceivedFile) {
				select {
				case gotFile <- time.Now():
				default:
				}
			},
		})
		if err != nil {
			_ = os.RemoveAll(dlDir)
			return nil, err
		}

		_ = txEng.Start()
		_ = rxEng.Start()

		// Wait for startup Hello handshake to finish completely so we measure pure steady-state transfer.
		if !sp.rxOnly {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				_, ok1 := txEng.ResolvePeer("receiver")
				_, ok2 := rxEng.ResolvePeer("sender")
				if ok1 == nil && ok2 == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Wait a few extra dwell ticks for any reactive Hello tail frames to retire.
			time.Sleep(dwellScaled * 12)
		} else {
			time.Sleep(dwellScaled * 12)
		}

		if sp.compressible {
			// Realistic structured JSON log with varying numbers (not trivial identical repeat)
		}

		txBefore, _ := txEng.FrameCounters()
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

		var payloadSize int
		var tRecv time.Time

		if sp.isText {
			payloadSize = len(sp.textData)
			errCh := make(chan error, 1)
			go func() {
				errCh <- txEng.SendText(ctx, sp.textData)
			}()
			select {
			case tRecv = <-gotChat:
			case <-ctx.Done():
				cancel()
				rxEng.Stop()
				txEng.Stop()
				_ = os.RemoveAll(dlDir)
				return nil, fmt.Errorf("benchmark %q timed out waiting for chat", sp.name)
			}
			<-errCh
		} else {
			payloadSize = sp.binBytes
			data := make([]byte, payloadSize)
			if sp.compressible {
				var lb strings.Builder
				for line := 0; lb.Len() < payloadSize; line++ {
					fmt.Fprintf(&lb, `{"seq":%d,"ts":%d,"peer":"anon-%04x","rssi":-%d,"status":"active"}`+"\n",
						line, 1764140000+line*3, (line*7919)&0xffff, 42+(line%31))
				}
				copy(data, []byte(lb.String())[:payloadSize])
			} else {
				_, _ = rand.Read(data)
			}
			target := "receiver"
			if sp.rxOnly {
				target = ""
			}
			errCh := make(chan error, 1)
			go func() {
				errCh <- txEng.SendFileBytes(ctx, target, "bench.bin", data)
			}()
			select {
			case tRecv = <-gotFile:
			case <-ctx.Done():
				cancel()
				rxEng.Stop()
				txEng.Stop()
				_ = os.RemoveAll(dlDir)
				return nil, fmt.Errorf("benchmark %q timed out waiting for file", sp.name)
			}
			<-errCh
		}

		txAfter, _ := txEng.FrameCounters()
		cancel()
		rxEng.Stop()
		txEng.Stop()
		_ = os.RemoveAll(dlDir)

		wallElapsed := tRecv.Sub(t0)
		realSimDur := time.Duration(float64(wallElapsed) * actualScale)
		bps := float64(payloadSize) / realSimDur.Seconds()

		results = append(results, BenchResult{
			Scenario:        sp.name,
			Mode:            string(sp.enc),
			Slots:           sp.slots,
			DwellReal:       sp.dwellReal,
			ScanReal:        sp.scanReal,
			LossPct:         int(sp.loss * 100),
			PayloadDesc:     sp.desc,
			PayloadBytes:    payloadSize,
			SimRealDuration: realSimDur,
			EffectiveBps:    bps,
			TxFrames:        txAfter - txBefore,
		})
	}
	return results, nil
}

func CmdBench(w io.Writer) int {
	fmt.Fprintf(w, "%sRunning WiFiChat end-to-end SSID air-channel throughput simulation...%s\n\n", colorBold, colorReset)
	res, err := RunBenchmarks()
	if err != nil {
		fmt.Fprintf(w, "Benchmark error: %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "%-34s %-28s %6s %9s %10s %8s\n",
		"HARDWARE PROFILE", "PAYLOAD", "LOSS", "SIM TIME", "SPEED", "SSIDs")
	fmt.Fprintln(w, strings.Repeat("-", 102))
	for _, r := range res {
		speedStr := fmt.Sprintf("%.0f B/s", r.EffectiveBps)
		if r.EffectiveBps >= 1024 {
			speedStr = fmt.Sprintf("%.2f KB/s", r.EffectiveBps/1024.0)
		}
		fmt.Fprintf(w, "%-34s %-28s %5d%% %9s %10s %8d\n",
			r.Scenario,
			r.PayloadDesc,
			r.LossPct,
			r.SimRealDuration.Round(10*time.Millisecond),
			speedStr,
			r.TxFrames,
		)
	}
	fmt.Fprintln(w, strings.Repeat("-", 102))
	return 0
}
