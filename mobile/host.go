package wifichat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"wifichat/pkg/wifi"
)

// Host is implemented on the Android/iOS side. It performs SoftAP SSID
// mutation and nearby Wi-Fi scanning using platform APIs (Shizuku/root/shell
// or WifiManager).
type Host interface {
	// SetSSID publishes the given SoftAP SSID. An empty string stops SoftAP
	// or restores a decoy name when the host chooses to keep the radio up.
	SetSSID(ssid string) error
	// ScanJSON returns a JSON array of beacons:
	// [{"ssid":"...","bssid":"...","signal":-50,"channel":6}, ...]
	ScanJSON() (string, error)
	// RotateMAC randomizes the SoftAP BSSID when the platform allows it.
	RotateMAC() error
}

// EventListener receives engine callbacks on the UI / host thread.
type EventListener interface {
	OnChat(nick, peerID, text string, direct, micro bool, unixMs int64)
	OnFile(nick, peerID, fileName, savedPath, sha256 string, size int, direct bool)
	OnPeer(nick, peerID, sas string, signal int)
	OnProgress(direction, label string, doneGens, totalGens, bytes int, bps float64, complete bool)
	OnEvent(msg string)
}

type hostDriver struct {
	host   Host
	rxOnly bool
}

func (d *hostDriver) Name() string {
	return "android-app"
}

func (d *hostDriver) MaxSlots() int { return 1 }

func (d *hostDriver) RecommendedDwell() time.Duration { return 900 * time.Millisecond }

func (d *hostDriver) RecommendedScanInterval() time.Duration {
	return 600 * time.Millisecond
}

func (d *hostDriver) SetSSIDs(ctx context.Context, ssids []string) error {
	if d.rxOnly {
		return nil
	}
	ssid := ""
	if len(ssids) > 0 {
		ssid = ssids[0]
	}
	if err := d.host.SetSSID(ssid); err != nil {
		return fmt.Errorf("host SetSSID: %w", err)
	}
	return nil
}

func (d *hostDriver) Scan(ctx context.Context) ([]wifi.Beacon, error) {
	raw, err := d.host.ScanJSON()
	if err != nil {
		return nil, err
	}
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var items []struct {
		SSID    string `json:"ssid"`
		BSSID   string `json:"bssid"`
		Signal  int    `json:"signal"`
		Channel int    `json:"channel"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("host ScanJSON: %w", err)
	}
	out := make([]wifi.Beacon, 0, len(items))
	for _, it := range items {
		if it.SSID == "" {
			continue
		}
		out = append(out, wifi.Beacon{
			SSID:    it.SSID,
			BSSID:   it.BSSID,
			Signal:  it.Signal,
			Channel: it.Channel,
		})
	}
	return out, nil
}

func (d *hostDriver) RotateIdentity(ctx context.Context) error {
	if d.rxOnly {
		return nil
	}
	return d.host.RotateMAC()
}

func (d *hostDriver) Close() error {
	if d.rxOnly {
		return nil
	}
	_ = d.host.SetSSID("")
	return nil
}
