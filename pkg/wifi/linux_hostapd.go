package wifi

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HostapdDriver drives `hostapd` + `hostapd_cli` + `iw` on Linux.
// Key speed advantages:
//  1. Multi-BSSID virtual APs (--slots 1..16) broadcast up to 16 SSIDs in parallel.
//  2. `hostapd_cli SET ssid2 <hex>` + `UPDATE_BEACON` updates the kernel 802.11
//     beacon template in <10ms without restarting the AP, supporting both ASCII91 and Raw32.
//  3. `iw dev <scanIface> scan freq <MHz> flush` scans a single channel in ~80ms.
type HostapdDriver struct {
	mu        sync.Mutex
	iface     string
	scanIface string
	channel   int
	slots     int
	rxOnly    bool

	workDir string
	ctrlDir string
	cmd     *exec.Cmd
	bssNames []string
	lastHex  []string
}

func NewHostapdDriver(cfg DriverConfig) (*HostapdDriver, error) {
	if !cfg.RXOnly {
		if _, err := exec.LookPath("hostapd"); err != nil {
			return nil, fmt.Errorf("hostapd not found: %w", err)
		}
		if _, err := exec.LookPath("hostapd_cli"); err != nil {
			return nil, fmt.Errorf("hostapd_cli not found: %w", err)
		}
	}
	if _, err := exec.LookPath("iw"); err != nil {
		return nil, fmt.Errorf("iw not found: %w", err)
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
	slots := cfg.Slots
	if slots < 1 {
		slots = 1
	}
	return &HostapdDriver{
		iface:     iface,
		scanIface: scanIface,
		channel:   cfg.Channel,
		slots:     slots,
		rxOnly:    cfg.RXOnly,
		lastHex:   make([]string, slots),
	}, nil
}

func (d *HostapdDriver) Name() string {
	return fmt.Sprintf("hostapd(%s,slots=%d)", d.iface, d.slots)
}
func (d *HostapdDriver) MaxSlots() int                        { return d.slots }
func (d *HostapdDriver) RecommendedDwell() time.Duration      { return 160 * time.Millisecond }
func (d *HostapdDriver) RecommendedScanInterval() time.Duration { return 120 * time.Millisecond }

func (d *HostapdDriver) startLocked(initial []string) error {
	dir, err := os.MkdirTemp("", "wifichat-hostapd-*")
	if err != nil {
		return err
	}
	ctrlDir := filepath.Join(dir, "ctrl")
	if err := os.MkdirAll(ctrlDir, 0o700); err != nil {
		return err
	}
	d.workDir = dir
	d.ctrlDir = ctrlDir

	confPath := filepath.Join(dir, "hostapd.conf")
	baseMAC := randomLocalMAC()[:15] // "02:xx:xx:xx:xx:"

	var b strings.Builder
	fmt.Fprintf(&b, "interface=%s\n", d.iface)
	fmt.Fprintf(&b, "driver=nl80211\n")
	fmt.Fprintf(&b, "ctrl_interface=%s\n", ctrlDir)
	fmt.Fprintf(&b, "hw_mode=g\n")
	fmt.Fprintf(&b, "channel=%d\n", d.channel)
	fmt.Fprintf(&b, "beacon_int=50\n") // 50 TU (~51.2ms) fast beacon rate!
	fmt.Fprintf(&b, "bssid=%s00\n", baseMAC)
	fmt.Fprintf(&b, "ssid2=%s\n", hex.EncodeToString([]byte(initial[0])))
	fmt.Fprintf(&b, "auth_algs=1\n")
	fmt.Fprintf(&b, "ignore_broadcast_ssid=0\n")

	d.bssNames = []string{d.iface}
	for i := 1; i < d.slots; i++ {
		bss := fmt.Sprintf("%s_wc%d", d.iface, i)
		s := initial[0]
		if i < len(initial) && initial[i] != "" {
			s = initial[i]
		}
		fmt.Fprintf(&b, "\nbss=%s\n", bss)
		fmt.Fprintf(&b, "bssid=%s%02x\n", baseMAC, i)
		fmt.Fprintf(&b, "ctrl_interface=%s\n", ctrlDir)
		fmt.Fprintf(&b, "ssid2=%s\n", hex.EncodeToString([]byte(s)))
		d.bssNames = append(d.bssNames, bss)
	}

	if err := os.WriteFile(confPath, []byte(b.String()), 0o600); err != nil {
		return err
	}

	cmd := exec.Command("hostapd", confPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hostapd: %w", err)
	}
	d.cmd = cmd

	// Wait briefly for control socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	sock := filepath.Join(ctrlDir, d.iface)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	for i := 0; i < len(d.bssNames) && i < len(initial); i++ {
		d.lastHex[i] = hex.EncodeToString([]byte(initial[i]))
	}
	return nil
}

func (d *HostapdDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || len(ssids) == 0 {
		return nil
	}
	if err := validateSSIDList(ssids); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cmd == nil {
		return d.startLocked(ssids)
	}
	for i := 0; i < len(d.bssNames) && i < len(ssids); i++ {
		if ssids[i] == "" {
			continue
		}
		hx := hex.EncodeToString([]byte(ssids[i]))
		if d.lastHex[i] == hx {
			continue
		}
		bss := d.bssNames[i]
		if out, err := exec.CommandContext(ctx, "hostapd_cli", "-p", d.ctrlDir, "-i", bss, "set", "ssid2", hx).CombinedOutput(); err != nil {
			return fmt.Errorf("hostapd_cli set ssid2 (%s): %w (%s)", bss, err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.CommandContext(ctx, "hostapd_cli", "-p", d.ctrlDir, "-i", bss, "update_beacon").CombinedOutput(); err != nil {
			return fmt.Errorf("hostapd_cli update_beacon (%s): %w (%s)", bss, err, strings.TrimSpace(string(out)))
		}
		d.lastHex[i] = hx
	}
	return nil
}

func (d *HostapdDriver) Scan(ctx context.Context) ([]Beacon, error) {
	return scanWithIW(ctx, d.scanIface, d.channel)
}

func (d *HostapdDriver) RotateIdentity(_ context.Context) error {
	d.mu.Lock()
	last := make([]string, 0, len(d.lastHex))
	for _, hx := range d.lastHex {
		if b, err := hex.DecodeString(hx); err == nil && len(b) > 0 {
			last = append(last, string(b))
		}
	}
	d.stopLocked()
	var err error
	if len(last) > 0 && !d.rxOnly {
		err = d.startLocked(last)
	}
	d.mu.Unlock()
	return err
}

func (d *HostapdDriver) stopLocked() {
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func(c *exec.Cmd) {
			_ = c.Wait()
			close(done)
		}(d.cmd)
		select {
		case <-done:
		case <-time.After(800 * time.Millisecond):
			_ = d.cmd.Process.Kill()
		}
		d.cmd = nil
	}
	if d.workDir != "" {
		_ = os.RemoveAll(d.workDir)
		d.workDir = ""
	}
}

func (d *HostapdDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked()
	return nil
}
