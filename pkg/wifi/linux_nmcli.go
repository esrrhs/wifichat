package wifi

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func detectLinuxWirelessIfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join("/sys/class/net", e.Name(), "wireless")
		if _, err := os.Stat(p); err == nil {
			out = append(out, e.Name())
			continue
		}
		p2 := filepath.Join("/sys/class/net", e.Name(), "phy80211")
		if _, err := os.Stat(p2); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// scanWithIW performs a fast single-frequency 802.11 scan via `iw` (typically ~80ms
// on a pinned 2.4GHz channel instead of ~3.5s full-band sweep).
func scanWithIW(ctx context.Context, iface string, channel int) ([]Beacon, error) {
	args := []string{"dev", iface, "scan"}
	if channel > 0 {
		args = append(args, "freq", strconv.Itoa(ChannelToFreqMHz(channel)), "flush")
	}
	cmd := exec.CommandContext(ctx, "iw", args...)
	out, err := cmd.Output()
	if err != nil {
		// Retry passive dump / non-freq scan if interface is busy in AP mode
		cmd2 := exec.CommandContext(ctx, "iw", "dev", iface, "scan", "ap-force")
		if channel > 0 {
			cmd2 = exec.CommandContext(ctx, "iw", "dev", iface, "scan", "freq", strconv.Itoa(ChannelToFreqMHz(channel)), "ap-force")
		}
		out, err = cmd2.Output()
		if err != nil {
			return nil, err
		}
	}
	return parseIwScanOutput(string(out)), nil
}

// parseIwScanOutput parses `iw dev <iface> scan` output, including \xHH hex escapes
// so both ASCII91 and Raw32 binary SSIDs are recovered byte-for-byte.
func parseIwScanOutput(text string) []Beacon {
	var beacons []Beacon
	var cur *Beacon

	flush := func() {
		if cur != nil && cur.SSID != "" {
			beacons = append(beacons, *cur)
		}
		cur = nil
	}

	for _, rawLine := range strings.Split(text, "\n") {
		if strings.HasPrefix(rawLine, "BSS ") {
			flush()
			rest := strings.TrimPrefix(rawLine, "BSS ")
			bssid := rest
			if idx := strings.IndexAny(rest, "( \t"); idx > 0 {
				bssid = rest[:idx]
			}
			cur = &Beacon{BSSID: bssid}
			continue
		}
		if cur == nil {
			continue
		}
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "freq:"):
			fStr := strings.TrimSpace(strings.TrimPrefix(line, "freq:"))
			if idx := strings.IndexByte(fStr, '.'); idx > 0 {
				fStr = fStr[:idx]
			}
			if f, err := strconv.Atoi(fStr); err == nil {
				cur.Channel = FreqMHzToChannel(f)
			}
		case strings.HasPrefix(line, "signal:"):
			sStr := strings.TrimSpace(strings.TrimPrefix(line, "signal:"))
			if fields := strings.Fields(sStr); len(fields) > 0 {
				if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
					cur.Signal = int(v)
				}
			}
		case strings.HasPrefix(line, "SSID: "):
			rawSSID := rawLine
			if idx := strings.Index(rawSSID, "SSID: "); idx >= 0 {
				cur.SSID = unescapeIwSSID(rawSSID[idx+len("SSID: "):])
			}
		}
	}
	flush()
	return beacons
}

func unescapeIwSSID(s string) string {
	var buf bytes.Buffer
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if b, err := hex.DecodeString(s[i+2 : i+4]); err == nil && len(b) == 1 {
				buf.WriteByte(b[0])
				i += 3
				continue
			}
		}
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '\\' {
			buf.WriteByte('\\')
			i++
			continue
		}
		buf.WriteByte(s[i])
	}
	return buf.String()
}

// NMCLIDriver controls a Linux Wi-Fi hotspot and scanner via NetworkManager (`nmcli`),
// automatically accelerating scans with single-channel `iw` when available.
type NMCLIDriver struct {
	mu          sync.Mutex
	iface       string
	scanIface   string
	channel     int
	rxOnly      bool
	hasIW       bool
	conName     string
	createdCon  bool
	origSSID    string
	active      bool
	lastSSID    string
}

func NewNMCLIDriver(cfg DriverConfig) (*NMCLIDriver, error) {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return nil, fmt.Errorf("nmcli not found: %w", err)
	}
	iface := cfg.Iface
	if iface == "" {
		ifaces := detectLinuxWirelessIfaces()
		if len(ifaces) == 0 {
			return nil, ErrNoWirelessInterface
		}
		iface = ifaces[0]
	}
	scanIface := cfg.ScanIface
	if scanIface == "" {
		scanIface = iface
	}
	_, iwErr := exec.LookPath("iw")
	return &NMCLIDriver{
		iface:     iface,
		scanIface: scanIface,
		channel:   cfg.Channel,
		rxOnly:    cfg.RXOnly,
		hasIW:     iwErr == nil,
		conName:   "wifichat-anon-" + iface,
	}, nil
}

func (d *NMCLIDriver) Name() string                         { return "nmcli(" + d.iface + ")" }
func (d *NMCLIDriver) MaxSlots() int                        { return 1 }
func (d *NMCLIDriver) RecommendedDwell() time.Duration      { return 650 * time.Millisecond }
func (d *NMCLIDriver) RecommendedScanInterval() time.Duration { return 350 * time.Millisecond }

func (d *NMCLIDriver) ensureHotspotLocked(ctx context.Context, initialSSID string) error {
	if d.active {
		return nil
	}
	_ = exec.CommandContext(ctx, "nmcli", "connection", "delete", d.conName).Run()
	args := []string{
		"connection", "add",
		"type", "wifi",
		"ifname", d.iface,
		"con-name", d.conName,
		"autoconnect", "no",
		"ssid", initialSSID,
		"802-11-wireless.mode", "ap",
		"802-11-wireless.band", "bg",
		"802-11-wireless.channel", strconv.Itoa(d.channel),
		"802-11-wireless.cloned-mac-address", "random",
		"ipv4.method", "shared",
		"ipv6.method", "ignore",
	}
	if out, err := exec.CommandContext(ctx, "nmcli", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("nmcli create hotspot: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	d.createdCon = true
	if out, err := exec.CommandContext(ctx, "nmcli", "connection", "up", d.conName).CombinedOutput(); err != nil {
		return fmt.Errorf("nmcli activate hotspot: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	d.active = true
	d.lastSSID = initialSSID
	return nil
}

func (d *NMCLIDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || len(ssids) == 0 || ssids[0] == "" {
		return nil
	}
	if err := validateSSIDList(ssids[:1]); err != nil {
		return err
	}
	target := ssids[0]

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active && d.lastSSID == target {
		return nil
	}
	if !d.active {
		return d.ensureHotspotLocked(ctx, target)
	}

	if out, err := exec.CommandContext(ctx, "nmcli", "connection", "modify", d.conName, "802-11-wireless.ssid", target).CombinedOutput(); err != nil {
		return fmt.Errorf("nmcli modify ssid: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// `device reapply` updates wpa_supplicant AP beacon in-place (~100ms) without tearing down AP.
	if err := exec.CommandContext(ctx, "nmcli", "device", "reapply", d.iface).Run(); err != nil {
		if out, err2 := exec.CommandContext(ctx, "nmcli", "connection", "up", d.conName).CombinedOutput(); err2 != nil {
			return fmt.Errorf("nmcli apply ssid: %w (%s)", err2, strings.TrimSpace(string(out)))
		}
	}
	d.lastSSID = target
	return nil
}

func (d *NMCLIDriver) Scan(ctx context.Context) ([]Beacon, error) {
	if d.hasIW {
		if bs, err := scanWithIW(ctx, d.scanIface, d.channel); err == nil {
			return bs, nil
		}
	}
	cmd := exec.CommandContext(ctx, "nmcli", "--terse", "--fields", "SSID,BSSID,SIGNAL,CHAN", "device", "wifi", "list", "ifname", d.scanIface, "--rescan", "auto")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseNmcliScanOutput(string(out)), nil
}

func parseNmcliScanOutput(text string) []Beacon {
	var beacons []Beacon
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		fields := splitNmcliEscaped(line)
		if len(fields) < 4 || fields[0] == "" {
			continue
		}
		sig, _ := strconv.Atoi(fields[2])
		ch, _ := strconv.Atoi(fields[3])
		beacons = append(beacons, Beacon{
			SSID:    fields[0],
			BSSID:   fields[1],
			Signal:  sig,
			Channel: ch,
		})
	}
	return beacons
}

// splitNmcliEscaped splits `nmcli -t` colon-delimited fields where literal ':' and '\'
// are escaped as '\:' and '\\'.
func splitNmcliEscaped(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) {
			cur.WriteByte(line[i+1])
			i++
			continue
		}
		if line[i] == ':' {
			fields = append(fields, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	fields = append(fields, cur.String())
	return fields
}

func (d *NMCLIDriver) RotateIdentity(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.active || d.rxOnly {
		return nil
	}
	mac := randomLocalMAC()
	_ = exec.CommandContext(ctx, "nmcli", "connection", "modify", d.conName, "802-11-wireless.cloned-mac-address", mac).Run()
	_ = exec.CommandContext(ctx, "nmcli", "device", "reapply", d.iface).Run()
	return nil
}

func (d *NMCLIDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.createdCon {
		_ = exec.Command("nmcli", "connection", "down", d.conName).Run()
		_ = exec.Command("nmcli", "connection", "delete", d.conName).Run()
		d.createdCon = false
		d.active = false
	}
	return nil
}
