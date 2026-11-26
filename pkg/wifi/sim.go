package wifi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// SimMedium is an in-memory 802.11 air medium for deterministic unit, fuzz,
// packet-loss, multi-SSID, and throughput tests. Clients never exchange sockets
// or identities directly: they only set <=32-byte SSIDs and scan visible beacons.
type SimMedium struct {
	mu          sync.RWMutex
	nodes       map[uint64]*simNodeState
	ambientSSID []string
	lossProb    float64
	scanDelay   time.Duration
	nextID      atomic.Uint64
	rngMu       sync.Mutex
	rng         *mrand.Rand
}

type simNodeState struct {
	bssid   string
	channel int
	ssids   []string
}

type SimConfig struct {
	LossProb    float64
	ScanDelay   time.Duration
	AmbientSSID []string
	Seed        uint64
}

func NewSimMedium(cfg SimConfig) *SimMedium {
	seed := cfg.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	ambient := cfg.AmbientSSID
	if ambient == nil {
		ambient = []string{"CMCC-5G-402", "Starbucks_WiFi", "TP-LINK_9F81", "iPhone"}
	}
	return &SimMedium{
		nodes:       make(map[uint64]*simNodeState),
		ambientSSID: append([]string(nil), ambient...),
		lossProb:    cfg.LossProb,
		scanDelay:   cfg.ScanDelay,
		rng:         mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
	}
}

func (m *SimMedium) shouldDrop() bool {
	if m.lossProb <= 0 {
		return false
	}
	m.rngMu.Lock()
	v := m.rng.Float64()
	m.rngMu.Unlock()
	return v < m.lossProb
}

func (m *SimMedium) NewDriver(slots int, channel int) Driver {
	if slots < 1 {
		slots = 1
	}
	if channel <= 0 {
		channel = 6
	}
	id := m.nextID.Add(1)
	m.mu.Lock()
	m.nodes[id] = &simNodeState{
		bssid:   randomLocalMAC(),
		channel: channel,
	}
	m.mu.Unlock()
	return &simDriver{
		medium:  m,
		nodeID:  id,
		slots:   slots,
		channel: channel,
	}
}

type simDriver struct {
	medium  *SimMedium
	nodeID  uint64
	slots   int
	channel int
}

func (d *simDriver) Name() string                         { return fmt.Sprintf("sim-mem(slots=%d)", d.slots) }
func (d *simDriver) MaxSlots() int                        { return d.slots }
func (d *simDriver) RecommendedDwell() time.Duration      { return 10 * time.Millisecond }
func (d *simDriver) RecommendedScanInterval() time.Duration { return 8 * time.Millisecond }

func (d *simDriver) SetSSIDs(_ context.Context, ssids []string) error {
	if err := validateSSIDList(ssids); err != nil {
		return err
	}
	if len(ssids) > d.slots {
		ssids = ssids[:d.slots]
	}
	cp := append([]string(nil), ssids...)
	d.medium.mu.Lock()
	if st, ok := d.medium.nodes[d.nodeID]; ok {
		st.ssids = cp
	}
	d.medium.mu.Unlock()
	return nil
}

func (d *simDriver) Scan(ctx context.Context) ([]Beacon, error) {
	if d.medium.scanDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d.medium.scanDelay):
		}
	}
	d.medium.mu.RLock()
	var beacons []Beacon
	for _, amb := range d.medium.ambientSSID {
		beacons = append(beacons, Beacon{SSID: amb, BSSID: "00:11:22:33:44:55", Signal: -65, Channel: d.channel})
	}
	for id, st := range d.medium.nodes {
		if id == d.nodeID || st.channel != d.channel {
			continue
		}
		for _, s := range st.ssids {
			if s == "" || d.medium.shouldDrop() {
				continue
			}
			beacons = append(beacons, Beacon{
				SSID:    s,
				BSSID:   st.bssid,
				Signal:  -45,
				Channel: st.channel,
			})
		}
	}
	d.medium.mu.RUnlock()
	return beacons, nil
}

func (d *simDriver) RotateIdentity(_ context.Context) error {
	d.medium.mu.Lock()
	if st, ok := d.medium.nodes[d.nodeID]; ok {
		st.bssid = randomLocalMAC()
	}
	d.medium.mu.Unlock()
	return nil
}

func (d *simDriver) Close() error {
	d.medium.mu.Lock()
	delete(d.medium.nodes, d.nodeID)
	d.medium.mu.Unlock()
	return nil
}

// BSSID returns the node's current simulated MAC address (used in anonymity tests).
func (d *simDriver) BSSID() string {
	d.medium.mu.RLock()
	defer d.medium.mu.RUnlock()
	if st := d.medium.nodes[d.nodeID]; st != nil {
		return st.bssid
	}
	return ""
}

// DirDriver implements multi-process virtual 802.11 air over a shared directory
// (default /tmp/wifichat-air), allowing multiple CLI instances on a server or
// CI runner without Wi-Fi hardware to communicate strictly via <=32-byte SSIDs.
type DirDriver struct {
	mu       sync.Mutex
	dir      string
	filePath string
	bssid    string
	channel  int
	slots    int
	ssidsHex []string
	closed   bool
	stopKeep chan struct{}
}

type dirBeaconRecord struct {
	BSSID       string   `json:"bssid"`
	Channel     int      `json:"channel"`
	UpdatedNano int64    `json:"updated_nano"`
	SSIDsHex    []string `json:"ssids_hex"`
}

const dirBeaconTTL = 4 * time.Second

func NewDirDriver(dir string, channel, slots int) (*DirDriver, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "wifichat-air")
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, err
	}
	if channel <= 0 {
		channel = 6
	}
	if slots < 1 {
		slots = 1
	}
	bssid := randomLocalMAC()
	tag := hex.EncodeToString([]byte(bssid))[:12]
	fp := filepath.Join(dir, fmt.Sprintf("ap-%d-%s.json", os.Getpid(), tag))

	d := &DirDriver{
		dir:      dir,
		filePath: fp,
		bssid:    bssid,
		channel:  channel,
		slots:    slots,
		stopKeep: make(chan struct{}),
	}
	go d.keepAliveLoop()
	return d, nil
}

func (d *DirDriver) Name() string                         { return fmt.Sprintf("sim(%s,slots=%d)", d.dir, d.slots) }
func (d *DirDriver) MaxSlots() int                        { return d.slots }
func (d *DirDriver) RecommendedDwell() time.Duration      { return 60 * time.Millisecond }
func (d *DirDriver) RecommendedScanInterval() time.Duration { return 35 * time.Millisecond }

func (d *DirDriver) keepAliveLoop() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.stopKeep:
			return
		case <-t.C:
			d.mu.Lock()
			if !d.closed && len(d.ssidsHex) > 0 {
				_ = d.writeRecordLocked()
			}
			d.mu.Unlock()
		}
	}
}

func (d *DirDriver) writeRecordLocked() error {
	rec := dirBeaconRecord{
		BSSID:       d.bssid,
		Channel:     d.channel,
		UpdatedNano: time.Now().UnixNano(),
		SSIDsHex:    d.ssidsHex,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := d.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, d.filePath)
}

func (d *DirDriver) SetSSIDs(_ context.Context, ssids []string) error {
	if err := validateSSIDList(ssids); err != nil {
		return err
	}
	if len(ssids) > d.slots {
		ssids = ssids[:d.slots]
	}
	hx := make([]string, 0, len(ssids))
	for _, s := range ssids {
		if s != "" {
			hx = append(hx, hex.EncodeToString([]byte(s)))
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.ssidsHex = hx
	if len(hx) == 0 {
		_ = os.Remove(d.filePath)
		return nil
	}
	return d.writeRecordLocked()
}

func (d *DirDriver) Scan(_ context.Context) ([]Beacon, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	myFile := filepath.Base(d.filePath)
	myChan := d.channel
	d.mu.Unlock()

	now := time.Now().UnixNano()
	var out []Beacon
	for _, e := range entries {
		name := e.Name()
		if name == myFile || filepath.Ext(name) != ".json" {
			continue
		}
		full := filepath.Join(d.dir, name)
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var rec dirBeaconRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if now-rec.UpdatedNano > int64(dirBeaconTTL) {
			_ = os.Remove(full)
			continue
		}
		if rec.Channel != myChan {
			continue
		}
		for _, h := range rec.SSIDsHex {
			raw, err := hex.DecodeString(h)
			if err != nil || len(raw) == 0 || len(raw) > 32 {
				continue
			}
			out = append(out, Beacon{
				SSID:    string(raw),
				BSSID:   rec.BSSID,
				Signal:  -48,
				Channel: rec.Channel,
			})
		}
	}
	return out, nil
}

func (d *DirDriver) RotateIdentity(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = os.Remove(d.filePath)
	d.bssid = randomLocalMAC()
	tag := hex.EncodeToString([]byte(d.bssid))[:12]
	d.filePath = filepath.Join(d.dir, fmt.Sprintf("ap-%d-%s.json", os.Getpid(), tag))
	if len(d.ssidsHex) > 0 && !d.closed {
		return d.writeRecordLocked()
	}
	return nil
}

func (d *DirDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	fp := d.filePath
	d.mu.Unlock()
	close(d.stopKeep)
	_ = os.Remove(fp)
	return nil
}
