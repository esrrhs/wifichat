package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"wifichat/pkg/crypto"
	"wifichat/pkg/engine"
	"wifichat/pkg/ssid"
	"wifichat/pkg/wifi"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

type Config struct {
	Driver       string
	Iface        string
	ScanIface    string
	Channel      int
	Slots        int
	Nick         string
	Key          string
	Encoding     string
	Prefix       string
	Decoy        string
	Stealth      bool
	RXOnly       bool
	Once         bool
	To           string
	DownloadDir  string
	SimDir       string
	TxCmd        string
	RxCmd        string
	Dwell        time.Duration
	ScanInterval time.Duration
	Redundancy   float64

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (c *Config) out() io.Writer {
	if c.Stdout != nil {
		return c.Stdout
	}
	return os.Stdout
}

func (c *Config) errOut() io.Writer {
	if c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
}

func (c *Config) in() io.Reader {
	if c.Stdin != nil {
		return c.Stdin
	}
	return os.Stdin
}

func PrintUsage(w io.Writer) {
	fmt.Fprintf(w, `%sWiFiChat%s — Anonymous nearby text & binary file communication via Wi-Fi SSIDs
Zero Wi-Fi association, zero IP/UDP/TCP, zero servers, ephemeral RAM-only keys.

%sUsage:%s
  wifichat [flags] [command]

%sCommands:%s
  chat                    Interactive anonymous nearby chat & file console (default)
  send <text...>          Broadcast (or --to <peer> E2EE) a chat message and exit
  sendfile <filepath>     Send a binary file (broadcast or --to <peer> E2EE) and exit
  recv                    Passive zero-TX listener: receive messages & files silently
  scan                    Scan nearby Wi-Fi networks once and inspect active signals
  bench                   Simulate end-to-end throughput across Wi-Fi hardware profiles
  drivers                 Show detected Wi-Fi hotspot/scanner backends on this host
  help                    Show this help message

%sFlags:%s
  -n, --nick <alias>          Optional ephemeral alias (default: random anon-xxxx in RAM)
  -k, --key <passphrase>      Room passphrase (Argon2id); empty = open nearby channel
  -d, --driver <backend>      auto | hostapd | nmcli | android | macos | windows | cmd | sim
      --iface <wlan0>         Wi-Fi interface for hotspot SSID broadcast
      --scan-iface <wlan1>    Optional second Wi-Fi interface for simultaneous fast scan
      --channel <1..13>       Pinned 2.4GHz channel for ~80ms single-freq scan (default: 6)
      --slots <1..16>         Parallel SSIDs per beacon sweep (hostapd/cmd/sim; default: 1)
      --encoding <mode>       ascii91 (26B/SSID universal, default) | raw32 (32B/SSID Linux/sim)
      --prefix <str>          Camouflage SSID prefix, e.g. "CMCC-" or "TP-"
      --decoy <ssid>          Decoy SSID shown when idle (default: random router name)
      --stealth               Quiet idle mode: do not emit periodic Hello beacons
      --rx-only               Pure passive receive mode: never modify hotspot SSID
      --once                  In 'recv', exit after receiving the first message or file
      --to <peer>             Target peer alias or ID prefix for X25519 E2EE 'send'/'sendfile'
      --download-dir <dir>    Directory to store incoming binary files (default: ./downloads)
      --dwell <duration>      Override SSID dwell/rotation interval (e.g. 200ms, 600ms)
      --scan-interval <dur>   Override Wi-Fi scan interval (e.g. 150ms, 400ms)
      --redundancy <ratio>    Cauchy MDS parity ratio for broadcast transfers (default: 0.55)
      --sim-dir <dir>         Shared virtual air directory for --driver sim
      --tx-cmd <cmd>          Custom SSID setter command for --driver cmd ({ssid} / $WIFICHAT_SSID)
      --rx-cmd <cmd>          Custom Wi-Fi scanner command for --driver cmd (one SSID per line)
`, colorBold, colorReset, colorYellow, colorReset, colorYellow, colorReset, colorYellow, colorReset)
}

func CmdDrivers(w io.Writer) int {
	fmt.Fprintf(w, "%sAvailable Wi-Fi SSID Backends:%s\n", colorBold, colorReset)
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")
	for _, st := range wifi.DetectDrivers() {
		mark := colorRed + "unavailable" + colorReset
		if st.Available {
			mark = colorGreen + "ready      " + colorReset
		}
		fmt.Fprintf(w, "  %-10s [%s]  %s\n", st.Name, mark, st.Detail)
	}
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")
	return 0
}

func buildEngine(cfg *Config, extra engine.Options) (*engine.Engine, error) {
	drv, err := wifi.Open(wifi.DriverConfig{
		Driver:    cfg.Driver,
		Iface:     cfg.Iface,
		ScanIface: cfg.ScanIface,
		Channel:   cfg.Channel,
		Slots:     cfg.Slots,
		RXOnly:    cfg.RXOnly || extra.RXOnly,
		SimDir:    cfg.SimDir,
		TxCmd:     cfg.TxCmd,
		RxCmd:     cfg.RxCmd,
	})
	if err != nil {
		return nil, err
	}
	extra.Driver = drv
	extra.Nick = cfg.Nick
	extra.Passphrase = cfg.Key
	extra.Encoding = ssid.Mode(cfg.Encoding)
	extra.Prefix = cfg.Prefix
	extra.DecoySSID = cfg.Decoy
	extra.Stealth = cfg.Stealth
	extra.RXOnly = cfg.RXOnly || extra.RXOnly
	extra.DownloadDir = cfg.DownloadDir
	extra.Dwell = cfg.Dwell
	extra.ScanInterval = cfg.ScanInterval
	extra.BlindRedundancy = cfg.Redundancy

	eng, err := engine.New(extra)
	if err != nil {
		_ = drv.Close()
		return nil, err
	}
	return eng, nil
}

func CmdScan(cfg *Config) int {
	cfgCopy := *cfg
	cfgCopy.RXOnly = true
	drv, err := wifi.Open(wifi.DriverConfig{
		Driver:    cfgCopy.Driver,
		Iface:     cfgCopy.Iface,
		ScanIface: cfgCopy.ScanIface,
		Channel:   cfgCopy.Channel,
		Slots:     1,
		RXOnly:    true,
		SimDir:    cfgCopy.SimDir,
		RxCmd:     cfgCopy.RxCmd,
	})
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "scan open driver: %v\n", err)
		return 1
	}
	defer drv.Close()

	mode := ssid.Mode(cfg.Encoding)
	if mode == "" {
		mode = ssid.ModeASCII91
	}
	codec, err := ssid.NewCodec(mode, cfg.Prefix)
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "codec: %v\n", err)
		return 1
	}
	ck := crypto.DeriveChannelKeys(cfg.Key)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	beacons, err := drv.Scan(ctx)
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "scan failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(cfg.out(), "Scanned %d nearby Wi-Fi beacon(s) via %s:\n", len(beacons), drv.Name())
	for _, b := range beacons {
		tag := "ordinary AP"
		if raw, err := codec.Decode(b.SSID); err == nil {
			if _, err := ck.OpenFrame(raw); err == nil {
				tag = colorGreen + "WIFICHAT ENCRYPTED FRAME" + colorReset
			}
		}
		fmt.Fprintf(cfg.out(), "  CH=%-2d RSSI=%-4d BSSID=%-17s [%s] %q\n",
			b.Channel, b.Signal, b.BSSID, tag, b.SSID)
	}
	return 0
}

func waitTargetPeerIfNeeded(ctx context.Context, eng *engine.Engine, target string, out io.Writer) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	fmt.Fprintf(out, "Waiting to discover E2EE public key for nearby peer %q...\n", target)
	_ = eng.QueueHello()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := eng.ResolvePeer(target); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting to discover peer %q on air", target)
}

func CmdSendText(cfg *Config, text string) int {
	eng, err := buildEngine(cfg, engine.Options{})
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "Error: %v\n", err)
		return 1
	}
	if err := eng.Start(); err != nil {
		fmt.Fprintf(cfg.errOut(), "Start failed: %v\n", err)
		return 1
	}
	defer eng.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := waitTargetPeerIfNeeded(ctx, eng, cfg.To, cfg.out()); err != nil {
		fmt.Fprintf(cfg.errOut(), "Error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(cfg.To) != "" {
		err = eng.SendDirectText(ctx, cfg.To, text)
	} else {
		err = eng.SendText(ctx, text)
	}
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "Send failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(cfg.out(), "Message transmitted via Wi-Fi SSID (%s).\n", eng.DriverName())
	return 0
}

func CmdSendFile(cfg *Config, path string) int {
	out := cfg.out()
	eng, err := buildEngine(cfg, engine.Options{
		OnProgress: func(p engine.Progress) {
			if p.Direction == "tx" {
				fmt.Fprintf(out, "\r[TX %s] gen %d/%d | chunks %d/%d | %.1f B/s   ",
					p.Label, p.DoneGens, p.TotalGens, p.DoneChunks, p.TotalChunks, p.BytesPerSec)
				if p.Complete {
					fmt.Fprintln(out)
				}
			}
		},
	})
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "Error: %v\n", err)
		return 1
	}
	if err := eng.Start(); err != nil {
		fmt.Fprintf(cfg.errOut(), "Start failed: %v\n", err)
		return 1
	}
	defer eng.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := waitTargetPeerIfNeeded(ctx, eng, cfg.To, out); err != nil {
		fmt.Fprintf(cfg.errOut(), "Error: %v\n", err)
		return 1
	}
	start := time.Now()
	if err := eng.SendFile(ctx, cfg.To, path); err != nil {
		fmt.Fprintf(cfg.errOut(), "\nFile transfer failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "\nFile %q transmitted in %s.\n", path, time.Since(start).Round(time.Millisecond))
	return 0
}

func CmdRecv(cfg *Config) int {
	out := cfg.out()
	doneOnce := make(chan struct{}, 1)
	triggerOnce := func() {
		if cfg.Once {
			select {
			case doneOnce <- struct{}{}:
			default:
			}
		}
	}

	eng, err := buildEngine(cfg, engine.Options{
		RXOnly: true,
		OnChat: func(m engine.ChatMessage) {
			mode := "BCAST"
			if m.Micro {
				mode = "1-SSID"
			}
			fmt.Fprintf(out, "[%s] [%s#%s]: %s\n", mode, m.Nick, m.SenderID.Short(), m.Text)
			triggerOnce()
		},
		OnFile: func(rf engine.ReceivedFile) {
			fmt.Fprintf(out, "[FILE] from %s#%s -> %s (%d bytes, sha256=%s..., %s)\n",
				rf.Nick, rf.SenderID.Short(), rf.SavedPath, rf.Size, rf.SHA256Hex[:12], rf.Elapsed.Round(time.Millisecond))
			triggerOnce()
		},
	})
	if err != nil {
		fmt.Fprintf(cfg.errOut(), "Error: %v\n", err)
		return 1
	}
	if err := eng.Start(); err != nil {
		fmt.Fprintf(cfg.errOut(), "Start failed: %v\n", err)
		return 1
	}
	defer eng.Stop()

	fmt.Fprintf(out, "Passive stealth receiver active on %s (zero Wi-Fi hotspot TX; saving files to %s)...\n",
		eng.DriverName(), cfg.DownloadDir)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-doneOnce:
	}
	return 0
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func CmdChat(cfg *Config) int {
	out := &syncWriter{w: cfg.out()}
	errOut := &syncWriter{w: cfg.errOut()}
	var eng *engine.Engine

	printPrompt := func() {
		fmt.Fprint(out, "wifichat> ")
	}

	opts := engine.Options{
		OnChat: func(m engine.ChatMessage) {
			tag := "NEARBY"
			col := colorGreen
			if m.Direct {
				tag = "E2EE-DM"
				col = colorCyan
			} else if m.Micro {
				tag = "1-SSID"
			}
			fmt.Fprintf(out, "\r\033[K%s[%s %s#%s]:%s %s\n",
				col, tag, m.Nick, m.SenderID.Short(), colorReset, m.Text)
			printPrompt()
		},
		OnFile: func(rf engine.ReceivedFile) {
			tag := "FILE"
			if rf.Direct {
				tag = "E2EE-FILE"
			}
			fmt.Fprintf(out, "\r\033[K%s[%s]%s %s#%s sent %q (%d bytes, sha256=%s...) -> saved to %s (%s)\n",
				colorGreen, tag, colorReset, rf.Nick, rf.SenderID.Short(),
				rf.FileName, rf.Size, rf.SHA256Hex[:12], rf.SavedPath, rf.Elapsed.Round(time.Millisecond))
			printPrompt()
		},
		OnPeer: func(p engine.PeerInfo) {
			fmt.Fprintf(out, "\r\033[K%s[Nearby]%s %s (id=%s, SAS=%s, slots=%d)\n",
				colorYellow, colorReset, p.Nick, p.PeerID.Hex(), p.SAS, p.Slots)
			printPrompt()
		},
		OnEvent: func(ev string) {
			fmt.Fprintf(out, "\r\033[K%s[Info]%s %s\n", colorYellow, colorReset, ev)
			printPrompt()
		},
	}

	var err error
	eng, err = buildEngine(cfg, opts)
	if err != nil {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if err := eng.Start(); err != nil {
		fmt.Fprintf(errOut, "Start failed: %v\n", err)
		return 1
	}
	defer eng.Stop()

	pub := eng.PublicIdentity()
	chanDesc := "Passphrase Protected (Argon2id + ChaCha20-Poly1305)"
	if eng.IsOpenChannel() {
		chanDesc = "Open Nearby Plaza (use -k <secret> for private room, or @peer for X25519 E2EE)"
	}

	fmt.Fprintln(out, "================================================================================")
	fmt.Fprintf(out, "%sWiFiChat Anonymous Air-Gap Console%s | Alias: %s%s%s | Ephemeral ID: %s\n",
		colorBold, colorReset, colorCyan, pub.Nick, colorReset, pub.PeerID.Hex())
	fmt.Fprintf(out, "Driver: %s | Codec: %s (%dB/SSID) | Channel: %s\n",
		eng.DriverName(), eng.Codec().Mode(), eng.Codec().RawBytes(), chanDesc)
	fmt.Fprintln(out, "--------------------------------------------------------------------------------")
	fmt.Fprintln(out, "Commands: <text> (broadcast)   @<peer> <text> (X25519 E2EE DM)   /peers")
	fmt.Fprintln(out, "          /sendfile [peer] <path>   /rotate   /key <pass>   /status   /help   q")
	fmt.Fprintln(out, "================================================================================")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(cfg.in())
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}
		close(lines)
	}()

	printPrompt()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(out, "\n%sCleaning up hotspot SSID and ephemeral keys...%s\n", colorYellow, colorReset)
			return 0
		case line, ok := <-lines:
			if !ok {
				return 0
			}
			if line == "" {
				printPrompt()
				continue
			}
			if line == "q" || line == "quit" || line == "/quit" || line == "exit" || line == "/exit" {
				return 0
			}
			handleInteractiveLine(ctx, eng, line, out)
			printPrompt()
		}
	}
}

func waitPeerShort(ctx context.Context, eng *engine.Engine, target string, maxWait time.Duration) error {
	if _, err := eng.ResolvePeer(target); err == nil {
		return nil
	}
	_ = eng.QueueHello()
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if _, err := eng.ResolvePeer(target); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("peer %q not discovered yet", target)
}

func handleInteractiveLine(ctx context.Context, eng *engine.Engine, line string, out io.Writer) {
	switch {
	case line == "/help":
		fmt.Fprintln(out, "  <message>                 Broadcast encrypted message to nearby channel")
		fmt.Fprintln(out, "  @<peer> <message>         Send X25519 E2EE direct message to <peer>")
		fmt.Fprintln(out, "  /dm <peer> <message>      Same as @<peer> <message>")
		fmt.Fprintln(out, "  /sendfile <path>          Broadcast binary file with Cauchy GF(256) MDS coding")
		fmt.Fprintln(out, "  /sendfile <peer> <path>   Send X25519 E2EE binary file directly to <peer>")
		fmt.Fprintln(out, "  /peers or /who            List nearby anonymous peers & SAS verification codes")
		fmt.Fprintln(out, "  /rotate                   Rotate ephemeral X25519 keypair, ID, alias & AP MAC")
		fmt.Fprintln(out, "  /key [passphrase]         Switch room passphrase (Argon2id) on the fly")
		fmt.Fprintln(out, "  /stealth [on|off]         Toggle quiet decoy-SSID idle mode")
		fmt.Fprintln(out, "  /hello                    Emit ephemeral X25519 key announcement now")
		fmt.Fprintln(out, "  /status                   Show active SSIDs on air & frame counters")
		fmt.Fprintln(out, "  q / /quit                 Exit and wipe RAM keys")

	case line == "/peers" || line == "/who":
		peers := eng.ListPeers()
		if len(peers) == 0 {
			fmt.Fprintln(out, "No nearby peers discovered yet (use /hello to announce).")
			return
		}
		fmt.Fprintln(out, "--------------------------------------------------------------------------------")
		fmt.Fprintf(out, "%-16s %-10s %-9s %-6s %-7s %s\n", "ALIAS", "ID", "SAS", "SLOTS", "RSSI", "SEEN")
		fmt.Fprintln(out, "--------------------------------------------------------------------------------")
		for _, p := range peers {
			sas := p.SAS
			if !p.HasPubKey {
				sas = "(pending)"
			}
			fmt.Fprintf(out, "%-16s %-10s %-9s %-6d %-7d %s\n",
				p.Nick, p.PeerID.Hex(), sas, p.Slots, p.Signal, p.LastSeen.Format("15:04:05"))
		}
		fmt.Fprintln(out, "--------------------------------------------------------------------------------")

	case line == "/rotate":
		pub, err := eng.RotateIdentity()
		if err != nil {
			fmt.Fprintf(out, "%sRotate failed: %v%s\n", colorRed, err, colorReset)
			return
		}
		fmt.Fprintf(out, "%sNew anonymous identity:%s %s (ID %s)\n", colorGreen, colorReset, pub.Nick, pub.PeerID.Hex())

	case line == "/hello":
		_ = eng.QueueHello()
		fmt.Fprintln(out, "Queued anonymous Hello key announcement.")

	case line == "/status":
		pub := eng.PublicIdentity()
		tx, rx := eng.FrameCounters()
		fmt.Fprintf(out, "Alias: %s | ID: %s | Driver: %s\n", pub.Nick, pub.PeerID.Hex(), eng.DriverName())
		fmt.Fprintf(out, "Frames TX: %d | Frames RX: %d | Active SSIDs: %v\n", tx, rx, eng.CurrentSSIDs())

	case strings.HasPrefix(line, "/key"):
		pass := strings.TrimSpace(strings.TrimPrefix(line, "/key"))
		eng.SetPassphrase(pass)

	case strings.HasPrefix(line, "/stealth"):
		arg := strings.TrimSpace(strings.TrimPrefix(line, "/stealth"))
		on := arg == "" || arg == "on" || arg == "true" || arg == "1"
		eng.SetStealth(on)
		fmt.Fprintf(out, "Stealth idle mode: %v\n", on)

	case strings.HasPrefix(line, "/sendfile ") || strings.HasPrefix(line, "/bcastfile "):
		isBcast := strings.HasPrefix(line, "/bcastfile ")
		rest := strings.TrimSpace(line[strings.IndexByte(line, ' ')+1:])
		var target, path string
		if isBcast {
			path = rest
		} else {
			parts := strings.Fields(rest)
			if len(parts) == 1 {
				path = parts[0]
			} else if len(parts) >= 2 {
				tail := strings.TrimSpace(rest[len(parts[0]):])
				if strings.HasPrefix(parts[0], "@") {
					target = parts[0]
					path = tail
				} else if _, err := eng.ResolvePeer(parts[0]); err == nil {
					target = parts[0]
					path = tail
				} else if _, err := os.Stat(tail); err == nil {
					target = parts[0]
					path = tail
				} else {
					path = rest
				}
			}
		}
		if path == "" {
			fmt.Fprintln(out, "Usage: /sendfile [peer] <filepath>")
			return
		}
		if target != "" {
			_ = waitPeerShort(ctx, eng, target, 4*time.Second)
		}
		start := time.Now()
		if err := eng.SendFile(ctx, target, path); err != nil {
			fmt.Fprintf(out, "%s[File Failed]: %v%s\n", colorRed, err, colorReset)
		} else {
			mode := "broadcast"
			if target != "" {
				mode = "E2EE -> " + target
			}
			fmt.Fprintf(out, "%s[File Sent (%s) in %s]:%s %s\n",
				colorCyan, mode, time.Since(start).Round(time.Millisecond), colorReset, path)
		}

	case strings.HasPrefix(line, "@") || strings.HasPrefix(line, "/dm "):
		raw := line
		if strings.HasPrefix(raw, "/dm ") {
			raw = strings.TrimSpace(raw[4:])
		} else {
			raw = raw[1:]
		}
		parts := strings.SplitN(raw, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			fmt.Fprintln(out, "Usage: @<peer> <message>")
			return
		}
		_ = waitPeerShort(ctx, eng, parts[0], 4*time.Second)
		if err := eng.SendDirectText(ctx, parts[0], parts[1]); err != nil {
			fmt.Fprintf(out, "%s[DM Failed]: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Fprintf(out, "%s[E2EE -> %s]:%s %s\n", colorCyan, parts[0], colorReset, parts[1])
		}

	case strings.HasPrefix(line, "/"):
		fmt.Fprintf(out, "Unknown command %q (type /help)\n", line)

	default:
		if err := eng.SendText(ctx, line); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(out, "%s[Send Failed]: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Fprintf(out, "%s[Broadcast]:%s %s\n", colorCyan, colorReset, line)
		}
	}
}
