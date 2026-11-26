package wifi

import (
	"context"
	"strings"
	"testing"
)

func TestParseIwAndNmcliAndNetshScans(t *testing.T) {
	iwOut := `BSS 02:aa:bb:cc:dd:ee(on wlan0)
	freq: 2437
	signal: -42.00 dBm
	SSID: !#$%&()*+,-./0123456789:;<=>?@AB
BSS 02:11:22:33:44:55(on wlan0)
	freq: 2437.0
	signal: -55.00 dBm
	SSID: \x00\xffRAW_BINARY_SSID_TEST_123456789
`
	bs := parseIwScanOutput(iwOut)
	if len(bs) != 2 {
		t.Fatalf("expected 2 iw beacons, got %d", len(bs))
	}
	if bs[0].Channel != 6 || bs[0].Signal != -42 || bs[0].SSID != "!#$%&()*+,-./0123456789:;<=>?@AB" {
		t.Fatalf("unexpected iw beacon[0]: %+v", bs[0])
	}
	if bs[1].SSID[0] != 0x00 || bs[1].SSID[1] != 0xFF || len(bs[1].SSID) != 32 {
		t.Fatalf("unexpected binary iw beacon[1]: len=%d %x", len(bs[1].SSID), []byte(bs[1].SSID))
	}

	nmOut := "A\\:B\\\\C:02\\:11\\:22\\:33\\:44\\:55:88:6\n"
	nbs := parseNmcliScanOutput(nmOut)
	if len(nbs) != 1 || nbs[0].SSID != `A:B\C` || nbs[0].BSSID != "02:11:22:33:44:55" || nbs[0].Channel != 6 {
		t.Fatalf("unexpected nmcli parse: %+v", nbs)
	}

	netshOut := "SSID 1 : TP-LINK_1234\r\n    Network type : Infrastructure\r\nSSID 2 : Custom32CharSSID0123456789ABCDEF\r\n"
	wbs := parseNetshScanOutput(netshOut)
	if len(wbs) != 2 || wbs[1].SSID != "Custom32CharSSID0123456789ABCDEF" {
		t.Fatalf("unexpected netsh parse: %+v", wbs)
	}
}

func TestSimMediumAndDirDriver(t *testing.T) {
	ctx := context.Background()
	med := NewSimMedium(SimConfig{})
	a := med.NewDriver(4, 6)
	b := med.NewDriver(1, 6)
	defer a.Close()
	defer b.Close()

	if err := a.SetSSIDs(ctx, []string{strings.Repeat("X", 33)}); err == nil {
		t.Fatal("SSID > 32 bytes must be rejected")
	}
	if err := a.SetSSIDs(ctx, []string{"SLOT0_SSID", "SLOT1_SSID", "SLOT2_SSID"}); err != nil {
		t.Fatal(err)
	}

	seen, err := b.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, x := range seen {
		found[x.SSID] = true
	}
	if !found["SLOT0_SSID"] || !found["SLOT1_SSID"] || !found["SLOT2_SSID"] || !found["Starbucks_WiFi"] {
		t.Fatalf("missing expected beacons: %v", found)
	}

	// MAC rotation check
	sd := a.(*simDriver)
	oldMAC := sd.BSSID()
	_ = a.RotateIdentity(ctx)
	if sd.BSSID() == oldMAC {
		t.Fatal("expected rotated simulated BSSID")
	}

	// Shared-directory virtual air driver (including raw binary SSIDs with 0x00)
	dir := t.TempDir()
	d1, err := NewDirDriver(dir, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer d1.Close()
	d2, err := NewDirDriver(dir, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	rawSSID := string([]byte{0x00, 0xFF, 'w', 'i', 'f', 'i'})
	if err := d1.SetSSIDs(ctx, []string{rawSSID, "SECOND_SLOT"}); err != nil {
		t.Fatal(err)
	}
	got, err := d2.Scan(ctx)
	if err != nil || len(got) != 2 || got[0].SSID != rawSSID || got[1].SSID != "SECOND_SLOT" {
		t.Fatalf("DirDriver scan mismatch: %+v err=%v", got, err)
	}
	_ = d1.RotateIdentity(ctx)
	got2, err := d2.Scan(ctx)
	if err != nil || len(got2) != 2 {
		t.Fatalf("DirDriver scan after RotateIdentity failed: %+v err=%v", got2, err)
	}
}

func TestChannelFreqParsersCmdDriverAndOpen(t *testing.T) {
	// Channel <-> MHz conversions (2.4 GHz & 5 GHz)
	for ch := 1; ch <= 14; ch++ {
		mhz := ChannelToFreqMHz(ch)
		if got := FreqMHzToChannel(mhz); got != ch {
			t.Fatalf("ch %d -> %d MHz -> ch %d", ch, mhz, got)
		}
	}
	if FreqMHzToChannel(ChannelToFreqMHz(149)) != 149 {
		t.Fatal("5GHz channel 149 roundtrip failed")
	}

	// Android Termux JSON + cmd wifi list-scan-results parsers
	termuxJSON := `[{"ssid":"WC_TEST_32_0123456789ABCDEFGHIJK","bssid":"02:aa:bb:cc:dd:ee","rssi":-49,"frequency_mhz":2437}]`
	tbs, err := parseTermuxScanJSON([]byte(termuxJSON))
	if err != nil || len(tbs) != 1 || tbs[0].Channel != 6 || tbs[0].Signal != -49 {
		t.Fatalf("parseTermuxScanJSON failed: %+v err=%v", tbs, err)
	}
	androidCmd := "BSSID Frequency RSSI Age SSID Flags\n02:11:22:33:44:55 2462 -51 0.120 ANDROID_SSID_0123456789 [WPA2]\n"
	abs := parseAndroidCmdScan(androidCmd)
	if len(abs) != 1 || abs[0].Channel != 11 || abs[0].SSID != "ANDROID_SSID_0123456789" {
		t.Fatalf("parseAndroidCmdScan failed: %+v", abs)
	}

	// macOS airport & system_profiler JSON parsers
	airportTxt := "                            SSID BSSID             RSSI CHANNEL HT CC SECURITY\n                    MyMacHotspot 02:aa:bb:cc:dd:01 -44  6       Y  US NONE\n"
	mbs := parseAirportScan(airportTxt)
	if len(mbs) != 1 || mbs[0].SSID != "MyMacHotspot" || mbs[0].Signal != -44 {
		t.Fatalf("parseAirportScan failed: %+v", mbs)
	}
	spJSON := `{"SPAirPortDataType":[{"spairport_airport_interfaces":[{"spairport_airport_other_local_wireless_networks":[{"_name":"SP_WIFI_01"}]}]}]}`
	sbs := parseMacSystemProfilerJSON([]byte(spJSON))
	if len(sbs) != 1 || sbs[0].SSID != "SP_WIFI_01" {
		t.Fatalf("parseMacSystemProfilerJSON failed: %+v", sbs)
	}

	// Custom CmdDriver roundtrip (including tricky characters like ! and $)
	ssidFile := t.TempDir() + "/ssid.txt"
	cd, err := Open(DriverConfig{
		Driver: "cmd",
		TxCmd:  "printf '%s\\n' {ssid} > " + ssidFile,
		RxCmd:  "cat " + ssidFile,
		Slots:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cd.Close()

	tricky := "!#$%&()*+,-./0123456789:;<=>?@AB"
	if err := cd.SetSSIDs(context.Background(), []string{tricky}); err != nil {
		t.Fatal(err)
	}
	cbs, err := cd.Scan(context.Background())
	if err != nil || len(cbs) != 1 || cbs[0].SSID != tricky {
		t.Fatalf("CmdDriver roundtrip mismatch: %+v err=%v", cbs, err)
	}

	// DetectDrivers & unknown driver error
	if len(DetectDrivers()) == 0 {
		t.Fatal("expected DetectDrivers to return entries")
	}
	if _, err := Open(DriverConfig{Driver: "nonexistent"}); err == nil {
		t.Fatal("expected unknown driver error")
	}
}
