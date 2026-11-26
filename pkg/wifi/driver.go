package wifi

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"wifichat/pkg/ssid"
)

// Beacon represents one nearby Wi-Fi network observed during a passive or active scan.
type Beacon struct {
	SSID    string
	BSSID   string
	Signal  int
	Channel int
}

// Driver abstracts modifying local Wi-Fi hotspot SSID(s) and scanning nearby SSIDs
// without ever associating (connecting) to any access point.
type Driver interface {
	Name() string
	MaxSlots() int
	RecommendedDwell() time.Duration
	RecommendedScanInterval() time.Duration
	SetSSIDs(ctx context.Context, ssids []string) error
	Scan(ctx context.Context) ([]Beacon, error)
	RotateIdentity(ctx context.Context) error
	Close() error
}

type DriverConfig struct {
	Driver    string // auto, nmcli, hostapd, android, macos, windows, cmd, sim
	Iface     string // TX / hotspot wireless interface
	ScanIface string // optional dedicated RX wireless interface for simultaneous scan
	Channel   int    // 2.4GHz channel (1..13, default 6 -> 2437 MHz)
	Slots     int    // parallel SSIDs (1..16)
	RXOnly    bool   // pure passive scan mode; never transmit or touch hotspot
	SimDir    string // shared virtual air directory for "sim" driver
	TxCmd     string // custom command for "cmd" driver
	RxCmd     string // custom scan command for "cmd" driver
}

type DriverStatus struct {
	Name      string
	Available bool
	Detail    string
}

func ChannelToFreqMHz(ch int) int {
	switch {
	case ch >= 1 && ch <= 13:
		return 2407 + ch*5
	case ch == 14:
		return 2484
	case ch >= 32 && ch <= 177:
		return 5000 + ch*5
	default:
		return 2437 // Channel 6 default
	}
}

func FreqMHzToChannel(freq int) int {
	switch {
	case freq == 2484:
		return 14
	case freq >= 2412 && freq <= 2472:
		return (freq - 2407) / 5
	case freq >= 5160 && freq <= 5885:
		return (freq - 5000) / 5
	default:
		return 0
	}
}

func randomLocalMAC() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	// Locally administered (bit 1 = 1), unicast (bit 0 = 0).
	b[0] = (b[0] & 0xFC) | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func validateSSIDList(ssids []string) error {
	for _, s := range ssids {
		if len(s) > ssid.MaxSSIDBytes {
			return fmt.Errorf("wifi: SSID exceeds 32 bytes (%d)", len(s))
		}
	}
	return nil
}

// DetectDrivers probes installed OS tools and wireless interfaces.
func DetectDrivers() []DriverStatus {
	var out []DriverStatus
	hasBin := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}

	if runtime.GOOS == "linux" {
		ifaces := detectLinuxWirelessIfaces()
		ifaceNote := "no /sys/class/net/*/wireless detected"
		if len(ifaces) > 0 {
			ifaceNote = fmt.Sprintf("interfaces: %v", ifaces)
		}
		out = append(out, DriverStatus{
			Name:      "hostapd",
			Available: hasBin("hostapd") && hasBin("hostapd_cli") && len(ifaces) > 0,
			Detail:    "Fastest Linux multi-BSSID beacon (<10ms UPDATE_BEACON) + iw single-freq scan; " + ifaceNote,
		})
		out = append(out, DriverStatus{
			Name:      "nmcli",
			Available: hasBin("nmcli") && len(ifaces) > 0,
			Detail:    "NetworkManager hotspot reapply + iw/nmcli scan; " + ifaceNote,
		})
		out = append(out, DriverStatus{
			Name:      "android",
			Available: hasBin("cmd") || hasBin("termux-wifi-scaninfo"),
			Detail:    "Android SoftAP (cmd wifi start-softap) / Termux Wi-Fi scanner",
		})
	}
	if runtime.GOOS == "darwin" {
		out = append(out, DriverStatus{
			Name:      "macos",
			Available: hasBin("system_profiler") || hasBin("networksetup"),
			Detail:    "macOS AirPort / CoreWLAN Wi-Fi scanner & hotspot adapter",
		})
	}
	if runtime.GOOS == "windows" {
		out = append(out, DriverStatus{
			Name:      "windows",
			Available: hasBin("netsh") && hasBin("powershell"),
			Detail:    "Windows Mobile Hotspot WinRT + netsh wlan BSSID scanner",
		})
	}
	out = append(out, DriverStatus{
		Name:      "cmd",
		Available: true,
		Detail:    "Custom external command hook (--tx-cmd / --rx-cmd for OpenWrt/ESP32/scripts)",
	})
	out = append(out, DriverStatus{
		Name:      "sim",
		Available: true,
		Detail:    "Shared virtual 802.11 air directory for local multi-terminal / hardwareless use",
	})
	return out
}

// Open constructs the requested (or auto-detected) Wi-Fi SSID driver.
func Open(cfg DriverConfig) (Driver, error) {
	if cfg.Channel <= 0 {
		cfg.Channel = 6
	}
	if cfg.Slots <= 0 {
		cfg.Slots = 1
	}
	if cfg.Slots > 16 {
		cfg.Slots = 16
	}

	drv := cfg.Driver
	if drv == "" || drv == "auto" {
		drv = autoPickDriver(cfg)
	}

	switch drv {
	case "sim":
		return NewDirDriver(cfg.SimDir, cfg.Channel, cfg.Slots)
	case "nmcli":
		return NewNMCLIDriver(cfg)
	case "hostapd":
		return NewHostapdDriver(cfg)
	case "android":
		return NewAndroidDriver(cfg)
	case "macos":
		return NewMacOSDriver(cfg)
	case "windows":
		return NewWindowsDriver(cfg)
	case "cmd":
		return NewCmdDriver(cfg)
	default:
		return nil, fmt.Errorf("wifi: unknown driver %q (use: auto, nmcli, hostapd, android, macos, windows, cmd, sim)", drv)
	}
}

func autoPickDriver(cfg DriverConfig) string {
	if cfg.TxCmd != "" || cfg.RxCmd != "" {
		return "cmd"
	}
	for _, st := range DetectDrivers() {
		if st.Name == "cmd" || st.Name == "sim" {
			continue
		}
		if st.Available {
			return st.Name
		}
	}
	return "sim"
}

var ErrNoWirelessInterface = errors.New("wifi: no wireless network interface found (specify --iface or use --driver sim)")
