package wifichat

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

)

type memHost struct {
	mu    sync.Mutex
	ssid  string
	dir   string
	scans int
}

func (h *memHost) SetSSID(ssid string) error {
	h.mu.Lock()
	h.ssid = ssid
	h.mu.Unlock()
	return nil
}

func (h *memHost) ScanJSON() (string, error) {
	h.mu.Lock()
	h.scans++
	ssid := h.ssid
	h.mu.Unlock()
	if ssid == "" {
		return "[]", nil
	}
	b, _ := json.Marshal([]map[string]any{{
		"ssid": ssid, "bssid": "02:00:00:00:00:01", "signal": -40, "channel": 6,
	}})
	return string(b), nil
}

func (h *memHost) RotateMAC() error { return nil }

type memListener struct {
	mu    sync.Mutex
	chats int
	events []string
}

func (l *memListener) OnChat(nick, peerID, text string, direct, micro bool, unixMs int64) {
	l.mu.Lock()
	l.chats++
	l.mu.Unlock()
}
func (l *memListener) OnFile(nick, peerID, fileName, savedPath, sha256 string, size int, direct bool) {
}
func (l *memListener) OnPeer(nick, peerID, sas string, signal int) {}
func (l *memListener) OnProgress(direction, label string, doneGens, totalGens, bytes int, bps float64, complete bool) {
}
func (l *memListener) OnEvent(msg string) {
	l.mu.Lock()
	l.events = append(l.events, msg)
	l.mu.Unlock()
}

type airPair struct {
	mu    sync.Mutex
	ssids map[string]string
}

func TestSessionSimRoundTrip(t *testing.T) {
	dir := t.TempDir()
	air := filepath.Join(dir, "air")

	shared := &airPair{ssids: map[string]string{}}

	makeHost := func(id string) *pairHost {
		return &pairHost{id: id, pair: shared, air: air}
	}

	l1 := &memListener{}
	l2 := &memListener{}
	s1, err := NewSession(makeHost("a"), &SessionConfig{Nick: "alice", Key: "room", DownloadDir: filepath.Join(dir, "dl1")}, l1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSession(makeHost("b"), &SessionConfig{Nick: "bob", Key: "room", DownloadDir: filepath.Join(dir, "dl2")}, l2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Start(); err != nil {
		t.Fatal(err)
	}
	defer s1.Stop()
	if err := s2.Start(); err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		_ = s1.SendText("hi-phone")
		l2.mu.Lock()
		n := l2.chats
		l2.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("message was not received by peer session")
}

type pairHost struct {
	id   string
	pair *airPair
	air  string
}

func (h *pairHost) SetSSID(ssid string) error {
	h.pair.mu.Lock()
	h.pair.ssids[h.id] = ssid
	h.pair.mu.Unlock()
	return nil
}

func (h *pairHost) ScanJSON() (string, error) {
	h.pair.mu.Lock()
	defer h.pair.mu.Unlock()
	type row struct {
		SSID    string `json:"ssid"`
		BSSID   string `json:"bssid"`
		Signal  int    `json:"signal"`
		Channel int    `json:"channel"`
	}
	var beacons []row
	for id, ssid := range h.pair.ssids {
		if id == h.id || ssid == "" {
			continue
		}
		beacons = append(beacons, row{SSID: ssid, BSSID: "02:00:00:00:00:0" + id, Signal: -35, Channel: 6})
	}
	b, _ := json.Marshal(beacons)
	return string(b), nil
}

func (h *pairHost) RotateMAC() error { return nil }
