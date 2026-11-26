package wifi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// AndroidDriver supports Android / Termux environments using Android 11+
// `cmd wifi start-softap` (via ADB/root/Shizuku shell) and `cmd wifi list-scan-results`
// or `termux-wifi-scaninfo`.
type AndroidDriver struct {
	channel int
	rxOnly  bool
	pass    string
}

func NewAndroidDriver(cfg DriverConfig) (*AndroidDriver, error) {
	return &AndroidDriver{
		channel: cfg.Channel,
		rxOnly:  cfg.RXOnly,
		pass:    "wc" + hex.EncodeToString([]byte(randomLocalMAC()))[:10],
	}, nil
}

func (d *AndroidDriver) Name() string                         { return "android-softap" }
func (d *AndroidDriver) MaxSlots() int                        { return 1 }
func (d *AndroidDriver) RecommendedDwell() time.Duration      { return 900 * time.Millisecond }
func (d *AndroidDriver) RecommendedScanInterval() time.Duration { return 600 * time.Millisecond }

func (d *AndroidDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || len(ssids) == 0 || ssids[0] == "" {
		return nil
	}
	if err := validateSSIDList(ssids[:1]); err != nil {
		return err
	}
	_ = exec.CommandContext(ctx, "cmd", "wifi", "stop-softap").Run()
	out, err := exec.CommandContext(ctx, "cmd", "wifi", "start-softap", ssids[0], "wpa2", d.pass, "-b", "2").CombinedOutput()
	if err != nil {
		return fmt.Errorf("android start-softap: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *AndroidDriver) Scan(ctx context.Context) ([]Beacon, error) {
	if _, err := exec.LookPath("termux-wifi-scaninfo"); err == nil {
		if out, err := exec.CommandContext(ctx, "termux-wifi-scaninfo").Output(); err == nil {
			if bs, err := parseTermuxScanJSON(out); err == nil {
				return bs, nil
			}
		}
	}
	_ = exec.CommandContext(ctx, "cmd", "wifi", "start-scan").Run()
	out, err := exec.CommandContext(ctx, "cmd", "wifi", "list-scan-results").Output()
	if err != nil {
		return nil, err
	}
	return parseAndroidCmdScan(string(out)), nil
}

func parseTermuxScanJSON(data []byte) ([]Beacon, error) {
	var items []struct {
		SSID  string `json:"ssid"`
		BSSID string `json:"bssid"`
		RSSI  int    `json:"rssi"`
		Freq  int    `json:"frequency_mhz"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	out := make([]Beacon, 0, len(items))
	for _, it := range items {
		if it.SSID == "" {
			continue
		}
		out = append(out, Beacon{
			SSID:    it.SSID,
			BSSID:   it.BSSID,
			Signal:  it.RSSI,
			Channel: FreqMHzToChannel(it.Freq),
		})
	}
	return out, nil
}

func parseAndroidCmdScan(text string) []Beacon {
	var out []Beacon
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		// Format: BSSID Frequency RSSI Age SSID ...
		if len(fields) < 5 || !strings.Contains(fields[0], ":") {
			continue
		}
		freq, _ := strconv.Atoi(fields[1])
		rssi, _ := strconv.Atoi(fields[2])
		out = append(out, Beacon{
			SSID:    fields[4],
			BSSID:   fields[0],
			Signal:  rssi,
			Channel: FreqMHzToChannel(freq),
		})
	}
	return out
}

func (d *AndroidDriver) RotateIdentity(_ context.Context) error { return nil }
func (d *AndroidDriver) Close() error {
	if !d.rxOnly {
		_ = exec.Command("cmd", "wifi", "stop-softap").Run()
	}
	return nil
}

// MacOSDriver scans nearby Wi-Fi networks on macOS via CoreWLAN/airport/system_profiler.
type MacOSDriver struct {
	rxOnly bool
	txCmd  string
}

func NewMacOSDriver(cfg DriverConfig) (*MacOSDriver, error) {
	return &MacOSDriver{rxOnly: cfg.RXOnly, txCmd: cfg.TxCmd}, nil
}

func (d *MacOSDriver) Name() string                         { return "macos" }
func (d *MacOSDriver) MaxSlots() int                        { return 1 }
func (d *MacOSDriver) RecommendedDwell() time.Duration      { return 1000 * time.Millisecond }
func (d *MacOSDriver) RecommendedScanInterval() time.Duration { return 800 * time.Millisecond }

func (d *MacOSDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || len(ssids) == 0 || ssids[0] == "" {
		return nil
	}
	if err := validateSSIDList(ssids[:1]); err != nil {
		return err
	}
	if d.txCmd != "" {
		return runCustomTx(ctx, d.txCmd, ssids)
	}
	return fmt.Errorf("macos restricts programmatic SoftAP SSID mutation without entitlement; use --rx-only to receive or provide --tx-cmd helper")
}

func (d *MacOSDriver) Scan(ctx context.Context) ([]Beacon, error) {
	airportPath := "/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport"
	if _, err := os.Stat(airportPath); err == nil {
		if out, err := exec.CommandContext(ctx, airportPath, "-s").Output(); err == nil {
			return parseAirportScan(string(out)), nil
		}
	}
	out, err := exec.CommandContext(ctx, "system_profiler", "SPAirPortDataType", "-json").Output()
	if err != nil {
		return nil, err
	}
	return parseMacSystemProfilerJSON(out), nil
}

func parseAirportScan(text string) []Beacon {
	var out []Beacon
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Find BSSID column xx:xx:xx:xx:xx:xx
		bssidIdx := -1
		for idx, f := range fields {
			if len(f) == 17 && strings.Count(f, ":") == 5 {
				bssidIdx = idx
				break
			}
		}
		if bssidIdx > 0 {
			ssidStr := strings.TrimSpace(strings.Join(fields[:bssidIdx], " "))
			sig := 0
			if bssidIdx+1 < len(fields) {
				sig, _ = strconv.Atoi(fields[bssidIdx+1])
			}
			out = append(out, Beacon{SSID: ssidStr, BSSID: fields[bssidIdx], Signal: sig})
		}
	}
	return out
}

func parseMacSystemProfilerJSON(data []byte) []Beacon {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	var out []Beacon
	var walk func(v any)
	walk = func(v any) {
		switch m := v.(type) {
		case map[string]any:
			if arr, ok := m["spairport_airport_other_local_wireless_networks"].([]any); ok {
				for _, item := range arr {
					if nm, ok := item.(map[string]any); ok {
						if name, ok := nm["_name"].(string); ok && name != "" {
							out = append(out, Beacon{SSID: name})
						}
					}
				}
			}
			for _, sub := range m {
				walk(sub)
			}
		case []any:
			for _, sub := range m {
				walk(sub)
			}
		}
	}
	walk(raw)
	return out
}

func (d *MacOSDriver) RotateIdentity(_ context.Context) error { return nil }
func (d *MacOSDriver) Close() error                           { return nil }

// WindowsDriver manages Windows Mobile Hotspot SSID via PowerShell WinRT and scans
// nearby networks via `netsh wlan show networks mode=bssid`.
type WindowsDriver struct {
	rxOnly bool
	pass   string
}

func NewWindowsDriver(cfg DriverConfig) (*WindowsDriver, error) {
	return &WindowsDriver{
		rxOnly: cfg.RXOnly,
		pass:   "Wc" + hex.EncodeToString([]byte(randomLocalMAC()))[:10],
	}, nil
}

func (d *WindowsDriver) Name() string                         { return "windows-hotspot" }
func (d *WindowsDriver) MaxSlots() int                        { return 1 }
func (d *WindowsDriver) RecommendedDwell() time.Duration      { return 1000 * time.Millisecond }
func (d *WindowsDriver) RecommendedScanInterval() time.Duration { return 700 * time.Millisecond }

func (d *WindowsDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || len(ssids) == 0 || ssids[0] == "" {
		return nil
	}
	if err := validateSSIDList(ssids[:1]); err != nil {
		return err
	}
	script := `
$profile = [Windows.Networking.Connectivity.NetworkInformation,Windows.Networking.Connectivity,ContentType=WindowsRuntime]::GetInternetConnectionProfile()
$tether = [Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]::CreateFromConnectionProfile($profile)
$cfg = $tether.GetCurrentAccessPointConfiguration()
$cfg.Ssid = $env:WIFICHAT_SSID
$cfg.Passphrase = $env:WIFICHAT_PASS
$op = $tether.ConfigureAccessPointAsync($cfg)
while ($op.Status -eq 0) { Start-Sleep -Milliseconds 20 }
if ($tether.TetheringOperationalState -ne 1) {
    $s = $tether.StartTetheringAsync()
    while ($s.Status -eq 0) { Start-Sleep -Milliseconds 20 }
}
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "WIFICHAT_SSID="+ssids[0], "WIFICHAT_PASS="+d.pass)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("windows configure hotspot: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *WindowsDriver) Scan(ctx context.Context) ([]Beacon, error) {
	out, err := exec.CommandContext(ctx, "netsh", "wlan", "show", "networks", "mode=bssid").Output()
	if err != nil {
		return nil, err
	}
	return parseNetshScanOutput(string(out)), nil
}

func parseNetshScanOutput(text string) []Beacon {
	var out []Beacon
	var curSSID string
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "SSID ") && !strings.HasPrefix(line, "BSSID ") {
			if idx := strings.IndexByte(line, ':'); idx >= 0 {
				curSSID = strings.TrimSpace(line[idx+1:])
				if curSSID != "" {
					out = append(out, Beacon{SSID: curSSID})
				}
			}
		}
	}
	return out
}

func (d *WindowsDriver) RotateIdentity(_ context.Context) error { return nil }
func (d *WindowsDriver) Close() error                           { return nil }

// CmdDriver allows plugging in arbitrary external scripts/hardware (OpenWrt uci,
// ESP32 multi-beacon serial adapter, Android root script, etc.).
type CmdDriver struct {
	txCmd  string
	rxCmd  string
	slots  int
	rxOnly bool
}

func NewCmdDriver(cfg DriverConfig) (*CmdDriver, error) {
	if cfg.RxCmd == "" && (cfg.TxCmd == "" || cfg.RXOnly) {
		return nil, fmt.Errorf("cmd driver requires --rx-cmd and/or --tx-cmd")
	}
	slots := cfg.Slots
	if slots < 1 {
		slots = 1
	}
	return &CmdDriver{
		txCmd:  cfg.TxCmd,
		rxCmd:  cfg.RxCmd,
		slots:  slots,
		rxOnly: cfg.RXOnly,
	}, nil
}

func (d *CmdDriver) Name() string                         { return "custom-cmd" }
func (d *CmdDriver) MaxSlots() int                        { return d.slots }
func (d *CmdDriver) RecommendedDwell() time.Duration      { return 400 * time.Millisecond }
func (d *CmdDriver) RecommendedScanInterval() time.Duration { return 300 * time.Millisecond }

func (d *CmdDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly || d.txCmd == "" || len(ssids) == 0 {
		return nil
	}
	if err := validateSSIDList(ssids); err != nil {
		return err
	}
	return runCustomTx(ctx, d.txCmd, ssids)
}

func runCustomTx(ctx context.Context, tmpl string, ssids []string) error {
	hexList := make([]string, len(ssids))
	for i, s := range ssids {
		hexList[i] = hex.EncodeToString([]byte(s))
	}
	safeQuote := "'" + strings.ReplaceAll(ssids[0], "'", `'\''`) + "'"
	commandStr := strings.ReplaceAll(tmpl, "{ssid}", safeQuote)

	cmd := exec.CommandContext(ctx, "sh", "-c", commandStr)
	cmd.Env = append(os.Environ(),
		"WIFICHAT_SSID="+ssids[0],
		"WIFICHAT_SSID_HEX="+hexList[0],
		"WIFICHAT_SSIDS_HEX="+strings.Join(hexList, ","),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tx-cmd failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *CmdDriver) Scan(ctx context.Context) ([]Beacon, error) {
	if d.rxCmd == "" {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", d.rxCmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var bs []Beacon
	for _, line := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" && len(line) <= 32 {
			bs = append(bs, Beacon{SSID: line})
		}
	}
	return bs, nil
}

func (d *CmdDriver) RotateIdentity(_ context.Context) error { return nil }
func (d *CmdDriver) Close() error                           { return nil }
