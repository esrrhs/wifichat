package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"wifichat/pkg/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wifichat", flag.ContinueOnError)
	fs.SetOutput(stderr)

	cfg := &cli.Config{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	defaultKey := os.Getenv("WIFICHAT_KEY")

	fs.StringVar(&cfg.Nick, "n", "", "Optional ephemeral alias")
	fs.StringVar(&cfg.Nick, "nick", "", "Optional ephemeral alias")
	fs.StringVar(&cfg.Key, "k", defaultKey, "Shared room passphrase (Argon2id)")
	fs.StringVar(&cfg.Key, "key", defaultKey, "Shared room passphrase (Argon2id)")
	fs.StringVar(&cfg.Driver, "d", "auto", "Wi-Fi backend: auto|hostapd|nmcli|android|macos|windows|cmd|sim")
	fs.StringVar(&cfg.Driver, "driver", "auto", "Wi-Fi backend: auto|hostapd|nmcli|android|macos|windows|cmd|sim")
	fs.StringVar(&cfg.Iface, "iface", "", "Wireless interface for hotspot SSID broadcast")
	fs.StringVar(&cfg.ScanIface, "scan-iface", "", "Optional dedicated wireless interface for scanning")
	fs.IntVar(&cfg.Channel, "channel", 6, "2.4GHz Wi-Fi channel (1..13)")
	fs.IntVar(&cfg.Slots, "slots", 1, "Parallel SSID slots (1..16)")
	fs.StringVar(&cfg.Encoding, "encoding", "ascii91", "SSID encoding: ascii91 | raw32")
	fs.StringVar(&cfg.Prefix, "prefix", "", "Camouflage SSID prefix (e.g. CMCC-)")
	fs.StringVar(&cfg.Decoy, "decoy", "", "Idle decoy SSID")
	fs.BoolVar(&cfg.Stealth, "stealth", false, "Quiet idle mode (no periodic Hello beacons)")
	fs.BoolVar(&cfg.RXOnly, "rx-only", false, "Passive listener mode (never transmit SSIDs)")
	fs.BoolVar(&cfg.Once, "once", false, "Exit after first message/file in 'recv'")
	fs.StringVar(&cfg.To, "to", "", "Target peer alias/ID for E2EE 'send' or 'sendfile'")
	fs.StringVar(&cfg.DownloadDir, "download-dir", "./downloads", "Directory for received binary files")
	fs.StringVar(&cfg.SimDir, "sim-dir", "", "Shared virtual air directory for --driver sim")
	fs.StringVar(&cfg.TxCmd, "tx-cmd", "", "Custom SSID setter command for --driver cmd")
	fs.StringVar(&cfg.RxCmd, "rx-cmd", "", "Custom SSID scanner command for --driver cmd")
	fs.DurationVar(&cfg.Dwell, "dwell", 0, "SSID rotation interval (0 = driver optimal)")
	fs.DurationVar(&cfg.ScanInterval, "scan-interval", 0, "Wi-Fi scan interval (0 = driver optimal)")
	fs.Float64Var(&cfg.Redundancy, "redundancy", 0.55, "Cauchy MDS parity ratio for broadcasts")

	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}

	rest := fs.Args()
	cmd := "chat"
	if len(rest) > 0 {
		cmd = rest[0]
	}

	switch cmd {
	case "chat":
		return cli.CmdChat(cfg)
	case "send":
		if len(rest) < 2 {
			fmt.Fprintln(stderr, "Usage: wifichat [flags] send <message...>")
			return 1
		}
		return cli.CmdSendText(cfg, strings.Join(rest[1:], " "))
	case "sendfile":
		if len(rest) < 2 {
			fmt.Fprintln(stderr, "Usage: wifichat [flags] sendfile <filepath>")
			return 1
		}
		return cli.CmdSendFile(cfg, rest[1])
	case "recv":
		return cli.CmdRecv(cfg)
	case "scan":
		return cli.CmdScan(cfg)
	case "bench":
		return cli.CmdBench(stdout)
	case "drivers":
		return cli.CmdDrivers(stdout)
	case "help", "-h", "--help":
		cli.PrintUsage(stdout)
		return 0
	default:
		_ = time.Second
		fmt.Fprintf(stderr, "Unknown command: %q\n\n", cmd)
		cli.PrintUsage(stderr)
		return 1
	}
}

type boolFlag interface {
	IsBoolFlag() bool
}

// reorderFlags allows flags to appear either before or after the subcommand
// (e.g. both `wifichat --once recv` and `wifichat recv --once` work naturally).
func reorderFlags(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, pos...)
}
