package engine

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wifichat/pkg/crypto"
	"wifichat/pkg/protocol"
	"wifichat/pkg/ssid"
	"wifichat/pkg/wifi"
)

const (
	maxActiveRxTransfers = 32
	rxStaleTTL           = 2 * time.Minute
	dedupCacheLimit      = 2048
)

// PeerInfo describes a nearby anonymous client discovered via Wi-Fi SSID beacons.
type PeerInfo struct {
	PeerID    crypto.PeerID
	Nick      string
	X25519Pub [32]byte
	HasPubKey bool
	SAS       string
	Slots     uint8
	Signal    int
	LastSeen  time.Time
}

// ChatMessage represents a received text message (1-SSID Micro, Room Broadcast, or X25519 E2EE Direct).
type ChatMessage struct {
	SenderID crypto.PeerID
	Nick     string
	Text     string
	Direct   bool
	Micro    bool
	Time     time.Time
}

// ReceivedFile represents a verified binary file saved to DownloadDir.
type ReceivedFile struct {
	SenderID  crypto.PeerID
	Nick      string
	FileName  string
	SavedPath string
	Size      int
	SHA256Hex string
	Direct    bool
	Elapsed   time.Duration
}

// Progress reports live TX/RX throughput and GF(256) erasure recovery stats.
type Progress struct {
	Direction    string // "tx" or "rx"
	Label        string
	XferID       uint16
	DoneGens     int
	TotalGens    int
	DoneChunks   int
	TotalChunks  int
	RepairsUsed  int
	Bytes        int
	BytesPerSec  float64
	Complete     bool
}

type Options struct {
	Driver          wifi.Driver
	Nick            string
	Passphrase      string
	Encoding        ssid.Mode
	Prefix          string
	DecoySSID       string
	Stealth         bool
	RXOnly          bool
	DownloadDir     string
	Dwell           time.Duration
	ScanInterval    time.Duration
	HelloInterval   time.Duration
	TransferTimeout time.Duration
	BlindRedundancy float64
	MicroHoldTicks  int

	OnChat     func(ChatMessage)
	OnFile     func(ReceivedFile)
	OnPeer     func(PeerInfo)
	OnProgress func(Progress)
	OnEvent    func(string)
}

type pendingAck struct {
	ack       protocol.AckFrame
	ticksLeft int
}

// Engine coordinates anonymous SSID modulation, multi-slot fountain coding,
// passive/active Wi-Fi scanning, and E2EE decryption.
type Engine struct {
	opts   Options
	drv    wifi.Driver
	codec  *ssid.Codec
	id     *crypto.Identity
	ckMu   sync.RWMutex
	ck     *crypto.ChannelKeys
	decoy  string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu            sync.Mutex
	txQueue       []*txJob
	activeJob     *txJob
	ackQueue      map[uint16]*pendingAck
	rxTransfers   map[uint16]*rxTransfer
	peers         map[crypto.PeerID]*PeerInfo
	shortPeers    map[[2]byte]crypto.PeerID
	seenOrder     []string
	seenSet       map[string]struct{}
	ownSSIDs      map[string]time.Time
	lastSSIDs     []string
	lastHelloResp time.Time
	txFramesTotal uint64
	rxFramesTotal uint64
}

func New(opts Options) (*Engine, error) {
	if opts.Driver == nil {
		return nil, errors.New("engine: wifi Driver is required")
	}
	mode := opts.Encoding
	if mode == "" {
		mode = ssid.ModeASCII91
	}
	codec, err := ssid.NewCodec(mode, opts.Prefix)
	if err != nil {
		return nil, err
	}
	id, err := crypto.NewIdentity(opts.Nick)
	if err != nil {
		return nil, err
	}
	ck := crypto.DeriveChannelKeys(opts.Passphrase)

	if opts.Dwell <= 0 {
		opts.Dwell = opts.Driver.RecommendedDwell()
	}
	if opts.ScanInterval <= 0 {
		opts.ScanInterval = opts.Driver.RecommendedScanInterval()
	}
	if opts.HelloInterval <= 0 {
		opts.HelloInterval = 20 * time.Second
	}
	if opts.TransferTimeout <= 0 {
		opts.TransferTimeout = 3 * time.Minute
	}
	if opts.DownloadDir == "" {
		opts.DownloadDir = "./downloads"
	}
	if opts.MicroHoldTicks <= 0 {
		opts.MicroHoldTicks = 5
	}
	decoy := opts.DecoySSID
	if decoy == "" {
		decoy = ssid.RandomDecoySSID()
	}

	return &Engine{
		opts:        opts,
		drv:         opts.Driver,
		codec:       codec,
		id:          id,
		ck:          ck,
		decoy:       decoy,
		ackQueue:    make(map[uint16]*pendingAck),
		rxTransfers: make(map[uint16]*rxTransfer),
		peers:       make(map[crypto.PeerID]*PeerInfo),
		shortPeers:  make(map[[2]byte]crypto.PeerID),
		seenSet:     make(map[string]struct{}),
		ownSSIDs:    make(map[string]time.Time),
	}, nil
}

func (e *Engine) Start() error {
	if e.cancel != nil {
		return errors.New("engine already started")
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())

	if !e.opts.RXOnly && !e.opts.Stealth {
		_ = e.QueueHello()
	} else if !e.opts.RXOnly && e.opts.Stealth {
		_ = e.applySSIDs([]string{e.decoy})
	}

	e.wg.Add(1)
	go e.scanLoop()
	if !e.opts.RXOnly {
		e.wg.Add(1)
		go e.txLoop()
	}
	return nil
}

func (e *Engine) Stop() {
	if e.cancel == nil {
		return
	}
	e.cancel()
	e.wg.Wait()
	_ = e.drv.Close()
	e.cancel = nil
}

func (e *Engine) PublicIdentity() crypto.IdentityPublic { return e.id.Public() }
func (e *Engine) DriverName() string                    { return e.drv.Name() }
func (e *Engine) Codec() *ssid.Codec                    { return e.codec }
func (e *Engine) IsOpenChannel() bool {
	e.ckMu.RLock()
	defer e.ckMu.RUnlock()
	return e.ck.IsOpen()
}

func (e *Engine) SetPassphrase(pass string) {
	ck := crypto.DeriveChannelKeys(pass)
	e.ckMu.Lock()
	e.ck = ck
	e.ckMu.Unlock()
	e.emitEvent("switched channel key (open=%v)", ck.IsOpen())
	if !e.opts.RXOnly && !e.opts.Stealth {
		_ = e.QueueHello()
	}
}

// SetNick updates the local display nickname and re-announces Hello if transmitting.
func (e *Engine) SetNick(nick string) crypto.IdentityPublic {
	e.id.SetNick(nick)
	pub := e.id.Public()
	e.emitEvent("nickname set to %s", pub.Nick)
	if !e.opts.RXOnly && !e.opts.Stealth {
		_ = e.QueueHello()
	}
	return pub
}

func (e *Engine) SetStealth(stealth bool) {
	e.mu.Lock()
	e.opts.Stealth = stealth
	e.mu.Unlock()
	if stealth && !e.opts.RXOnly {
		_ = e.applySSIDs([]string{e.decoy})
	} else if !stealth && !e.opts.RXOnly {
		_ = e.QueueHello()
	}
}

// RotateIdentity generates a fresh ephemeral X25519 keypair, PeerID, alias, and rotates AP MAC.
func (e *Engine) RotateIdentity() (crypto.IdentityPublic, error) {
	if err := e.id.Rotate(); err != nil {
		return crypto.IdentityPublic{}, err
	}
	if !e.opts.RXOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = e.drv.RotateIdentity(ctx)
		cancel()
		if !e.opts.Stealth {
			_ = e.QueueHello()
		}
	}
	pub := e.id.Public()
	e.emitEvent("rotated anonymous identity -> %s (%s)", pub.Nick, pub.PeerID.Hex())
	return pub, nil
}

func (e *Engine) currentKeys() *crypto.ChannelKeys {
	e.ckMu.RLock()
	defer e.ckMu.RUnlock()
	return e.ck
}

func (e *Engine) frameBodySize() int {
	return e.codec.RawBytes() - crypto.FrameTagSize
}

func (e *Engine) chunkSize() int {
	return protocol.ChunkDataSize(e.frameBodySize())
}

func (e *Engine) randomXferID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint16(b[:])
	if v == 0 {
		v = 1
	}
	return v
}

// QueueHello broadcasts an anonymous ephemeral presence + X25519 public key announcement.
func (e *Engine) QueueHello() error {
	if e.opts.RXOnly {
		return nil
	}
	xid := e.randomXferID()
	ck := e.currentKeys()
	env, err := protocol.SealHello(protocol.SealOptions{
		XferID:  xid,
		Sender:  e.id,
		Channel: ck,
	}, uint8(e.drv.MaxSlots()))
	if err != nil {
		return err
	}
	job, err := newEnvelopeJob("hello", xid, env, e.chunkSize(), false, crypto.PeerID{}, 0.5)
	if err != nil {
		return err
	}
	e.enqueueJob(job)
	return nil
}

// SendText broadcasts an encrypted chat message (using 1-SSID MicroFrame if short,
// or compressed Cauchy GF(256) MDS frames if longer) and waits for air transmission.
func (e *Engine) SendText(ctx context.Context, text string) error {
	if e.opts.RXOnly {
		return errors.New("engine is in rx-only passive mode")
	}
	if text == "" {
		return nil
	}
	ck := e.currentKeys()
	pub := e.id.Public()

	// Fast path: 1-SSID MicroFrame when message fits in a single 32-byte SSID!
	if body, mid, ok := protocol.TryEncodeMicroBody(ck, pub.PeerID, e.id.NextSalt(), text, e.frameBodySize()); ok {
		job := newMicroJob(mid, body, e.opts.MicroHoldTicks)
		e.enqueueJob(job)
		return e.waitJob(ctx, job)
	}

	xid := e.randomXferID()
	env, err := protocol.SealText(protocol.SealOptions{
		XferID:  xid,
		Sender:  e.id,
		Channel: ck,
	}, text)
	if err != nil {
		return err
	}
	job, err := newEnvelopeJob("text", xid, env, e.chunkSize(), false, crypto.PeerID{}, e.opts.BlindRedundancy)
	if err != nil {
		return err
	}
	e.enqueueJob(job)
	return e.waitJob(ctx, job)
}

// SendDirectText sends an X25519 E2EE private text message to a discovered peer.
func (e *Engine) SendDirectText(ctx context.Context, targetQuery string, text string) error {
	if e.opts.RXOnly {
		return errors.New("engine is in rx-only passive mode")
	}
	peer, err := e.ResolvePeer(targetQuery)
	if err != nil {
		return err
	}
	xid := e.randomXferID()
	env, err := protocol.SealText(protocol.SealOptions{
		XferID:        xid,
		Sender:        e.id,
		Channel:       e.currentKeys(),
		Direct:        true,
		RecipientID:   peer.PeerID,
		RecipientXPub: peer.X25519Pub,
	}, text)
	if err != nil {
		return err
	}
	job, err := newEnvelopeJob("dm:"+peer.Nick, xid, env, e.chunkSize(), true, peer.PeerID, e.opts.BlindRedundancy)
	if err != nil {
		return err
	}
	e.enqueueJob(job)
	return e.waitJob(ctx, job)
}

// SendFile sends a binary file either as a room broadcast (targetQuery == "")
// or as an X25519 E2EE direct transfer to targetQuery.
func (e *Engine) SendFile(ctx context.Context, targetQuery string, filePath string) error {
	if e.opts.RXOnly {
		return errors.New("engine is in rx-only passive mode")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	return e.SendFileBytes(ctx, targetQuery, filepath.Base(filePath), data)
}

// SendFileBytes transmits arbitrary binary file bytes with SHA-256 verification,
// compression, AEAD encryption, and multi-generation Cauchy GF(256) MDS coding.
func (e *Engine) SendFileBytes(ctx context.Context, targetQuery string, fileName string, data []byte) error {
	if e.opts.RXOnly {
		return errors.New("engine is in rx-only passive mode")
	}
	xid := e.randomXferID()
	sopts := protocol.SealOptions{
		XferID:  xid,
		Sender:  e.id,
		Channel: e.currentKeys(),
	}
	var targetID crypto.PeerID
	direct := strings.TrimSpace(targetQuery) != ""
	if direct {
		peer, err := e.ResolvePeer(targetQuery)
		if err != nil {
			return err
		}
		sopts.Direct = true
		sopts.RecipientID = peer.PeerID
		sopts.RecipientXPub = peer.X25519Pub
		targetID = peer.PeerID
	}

	env, err := protocol.SealFile(sopts, fileName, data)
	if err != nil {
		return err
	}
	job, err := newEnvelopeJob("file:"+protocol.SanitizeFileName(fileName), xid, env, e.chunkSize(), direct, targetID, e.opts.BlindRedundancy)
	if err != nil {
		return err
	}
	e.enqueueJob(job)
	return e.waitJob(ctx, job)
}

func (e *Engine) enqueueJob(job *txJob) {
	e.mu.Lock()
	// Replace any older queued "hello" job so presence beacons never pile up.
	if job.label == "hello" {
		filtered := e.txQueue[:0]
		for _, q := range e.txQueue {
			if q.label != "hello" {
				filtered = append(filtered, q)
			} else {
				q.finish(nil)
			}
		}
		e.txQueue = append(filtered, job)
	} else {
		e.txQueue = append(e.txQueue, job)
	}
	e.mu.Unlock()
}

func (e *Engine) waitJob(ctx context.Context, job *txJob) error {
	timeout := e.opts.TransferTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-job.doneCh:
		return err
	case <-ctx.Done():
		e.cancelJob(job, ctx.Err())
		return ctx.Err()
	case <-e.ctx.Done():
		return e.ctx.Err()
	case <-timer.C:
		err := errors.New("transfer timed out waiting for peer ACK")
		e.cancelJob(job, err)
		return err
	}
}

func (e *Engine) cancelJob(job *txJob, reason error) {
	e.mu.Lock()
	job.finish(reason)
	if e.activeJob == job {
		e.activeJob = nil
	}
	e.mu.Unlock()
}

// ResolvePeer finds a nearby discovered peer with an X25519 public key by nick or hex ID prefix.
func (e *Engine) ResolvePeer(query string) (PeerInfo, error) {
	q := strings.TrimPrefix(strings.TrimSpace(query), "@")
	qLower := strings.ToLower(q)

	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.peers {
		if strings.EqualFold(p.Nick, q) || strings.HasPrefix(p.PeerID.Hex(), qLower) {
			if !p.HasPubKey {
				return PeerInfo{}, fmt.Errorf("peer %q seen via 1-SSID micro frame only; wait for their Hello key beacon before sending E2EE DM", p.Nick)
			}
			return *p, nil
		}
	}
	return PeerInfo{}, fmt.Errorf("nearby peer %q not found (use /peers to list discovered anonymous peers)", q)
}

func (e *Engine) ListPeers() []PeerInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]PeerInfo, 0, len(e.peers))
	for _, p := range e.peers {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

func (e *Engine) CurrentSSIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.lastSSIDs...)
}

func (e *Engine) FrameCounters() (tx, rx uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.txFramesTotal, e.rxFramesTotal
}

func (e *Engine) txLoop() {
	defer e.wg.Done()
	dwellT := time.NewTicker(e.opts.Dwell)
	defer dwellT.Stop()
	helloT := time.NewTicker(e.opts.HelloInterval)
	defer helloT.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-helloT.C:
			e.mu.Lock()
			idle := e.activeJob == nil && len(e.txQueue) == 0 && !e.opts.Stealth
			e.mu.Unlock()
			if idle {
				_ = e.QueueHello()
			}
		case <-dwellT.C:
			ssids, prog := e.stepTx()
			if len(ssids) > 0 {
				_ = e.applySSIDs(ssids)
			}
			if prog != nil && e.opts.OnProgress != nil {
				e.opts.OnProgress(*prog)
			}
		}
	}
}

func (e *Engine) stepTx() ([]string, *Progress) {
	maxSlots := e.drv.MaxSlots()
	if maxSlots < 1 {
		maxSlots = 1
	}
	bodySize := e.frameBodySize()
	ck := e.currentKeys()

	e.mu.Lock()
	defer e.mu.Unlock()

	var bodies [][]byte

	// Priority 1: Emit pending ACKs (uses 1 slot when multi-slot, or preempts briefly when 1-slot).
	for xid, pa := range e.ackQueue {
		if pa.ticksLeft <= 0 {
			delete(e.ackQueue, xid)
			continue
		}
		if b, err := protocol.EncodeAckBody(pa.ack, bodySize); err == nil {
			bodies = append(bodies, b)
		}
		pa.ticksLeft--
		if pa.ticksLeft <= 0 {
			delete(e.ackQueue, xid)
		}
		// At most 1 ACK per tick on 1-slot AP; up to half of slots on multi-slot AP.
		if maxSlots == 1 || len(bodies) >= maxSlots/2 {
			break
		}
	}

	// Priority 2: Fill remaining SSID slots from active/queued txJob.
	var prog *Progress
	remSlots := maxSlots - len(bodies)
	for remSlots > 0 {
		if e.activeJob == nil {
			if len(e.txQueue) == 0 {
				break
			}
			e.activeJob = e.txQueue[0]
			e.txQueue = e.txQueue[1:]
			// Interrupt background "hello" if real user data is queued behind it.
		}
		if e.activeJob.label == "hello" && len(e.txQueue) > 0 {
			e.activeJob.finish(nil)
			e.activeJob = e.txQueue[0]
			e.txQueue = e.txQueue[1:]
		}

		jb, done := e.activeJob.nextFrameBodies(bodySize, remSlots)
		bodies = append(bodies, jb...)
		remSlots = maxSlots - len(bodies)

		if e.activeJob.kind == txKindEnvelope && e.activeJob.label != "hello" {
			p := buildTxProgress(e.activeJob)
			prog = &p
		}
		if done {
			e.activeJob = nil
		} else {
			break
		}
	}

	if len(bodies) == 0 {
		if e.opts.Stealth || len(e.lastSSIDs) == 0 {
			return []string{e.decoy}, prog
		}
		// Return decoy when idle so hotspot doesn't keep stale transfer frames forever.
		return []string{e.decoy}, prog
	}

	ssids := make([]string, 0, len(bodies))
	now := time.Now()
	// Prune expired ownSSIDs entries
	for k, exp := range e.ownSSIDs {
		if now.After(exp) {
			delete(e.ownSSIDs, k)
		}
	}
	for _, b := range bodies {
		sealed, err := ck.SealFrame(b)
		if err != nil {
			continue
		}
		s, err := e.codec.Encode(sealed)
		if err != nil {
			continue
		}
		ssids = append(ssids, s)
		e.ownSSIDs[s] = now.Add(10 * time.Second)
		e.txFramesTotal++
	}
	return ssids, prog
}

func buildTxProgress(j *txJob) Progress {
	doneGens := 0
	totalChunks := 0
	doneChunks := 0
	for _, g := range j.gens {
		totalChunks += g.enc.K()
		if g.acked {
			doneGens++
			doneChunks += g.enc.K()
		} else {
			em := g.emitted
			if em > g.enc.K() {
				em = g.enc.K()
			}
			doneChunks += em
		}
	}
	elapsed := time.Since(j.startedAt).Seconds()
	bps := 0.0
	if elapsed > 0.001 {
		bps = float64(j.rawBytes) * (float64(doneChunks) / float64(maxInt(1, totalChunks))) / elapsed
	}
	return Progress{
		Direction:   "tx",
		Label:       j.label,
		XferID:      j.xferID,
		DoneGens:    doneGens,
		TotalGens:   len(j.gens),
		DoneChunks:  doneChunks,
		TotalChunks: totalChunks,
		Bytes:       j.rawBytes,
		BytesPerSec: bps,
		Complete:    j.completed,
	}
}

func (e *Engine) applySSIDs(ssids []string) error {
	e.mu.Lock()
	same := len(ssids) == len(e.lastSSIDs)
	if same {
		for i := range ssids {
			if ssids[i] != e.lastSSIDs[i] {
				same = false
				break
			}
		}
	}
	if same {
		e.mu.Unlock()
		return nil
	}
	e.lastSSIDs = append([]string(nil), ssids...)
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	if err := e.drv.SetSSIDs(ctx, ssids); err != nil {
		e.emitEvent("wifi set SSID error: %v", err)
		return err
	}
	return nil
}

func (e *Engine) scanLoop() {
	defer e.wg.Done()
	t := time.NewTicker(e.opts.ScanInterval)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			_ = e.ScanOnce(e.ctx)
		}
	}
}

// ScanOnce triggers one Wi-Fi scan and processes any valid encrypted wifichat SSIDs.
func (e *Engine) ScanOnce(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	beacons, err := e.drv.Scan(sctx)
	cancel()
	if err != nil {
		return err
	}
	for _, b := range beacons {
		e.processBeacon(b)
	}
	return nil
}

func (e *Engine) processBeacon(b wifi.Beacon) {
	e.mu.Lock()
	_, isOwn := e.ownSSIDs[b.SSID]
	e.mu.Unlock()
	if isOwn {
		return
	}

	raw, err := e.codec.Decode(b.SSID)
	if err != nil {
		return
	}
	ck := e.currentKeys()
	body, err := ck.OpenFrame(raw)
	if err != nil {
		return
	}
	pf, err := protocol.DecodeBody(ck, body)
	if err != nil {
		return
	}

	e.mu.Lock()
	e.rxFramesTotal++
	e.mu.Unlock()

	switch pf.Type {
	case protocol.FrameAck:
		e.handleAck(pf.Ack)
	case protocol.FrameMicro:
		e.handleMicro(pf.Micro, b.Signal)
	case protocol.FrameData:
		e.handleData(pf.Data, b.Signal)
	}
}

func (e *Engine) handleAck(ack *protocol.AckFrame) {
	myID := e.id.Public().PeerID
	if ack.PeerID == myID {
		return
	}
	var doneProg *Progress
	e.mu.Lock()
	if e.activeJob != nil && e.activeJob.xferID == ack.XferID {
		wasDone := e.activeJob.completed
		if e.activeJob.applyAck(ack) && !wasDone && e.activeJob.kind == txKindEnvelope && e.activeJob.label != "hello" {
			p := buildTxProgress(e.activeJob)
			doneProg = &p
		}
	}
	for _, q := range e.txQueue {
		if q.xferID == ack.XferID {
			q.applyAck(ack)
		}
	}
	e.mu.Unlock()
	if doneProg != nil && e.opts.OnProgress != nil {
		e.opts.OnProgress(*doneProg)
	}
}

func (e *Engine) handleMicro(m *protocol.MicroFrame, signal int) {
	myPub := e.id.Public()
	if m.SenderShort == myPub.PeerID.Short2() {
		// Guard against rare 16-bit self-short collision only if text isn't ours?
		// ownSSIDs already filters our own transmissions; still check dedup.
	}
	dedupKey := fmt.Sprintf("micro:%x:%x", m.SenderShort, m.Nonce)

	e.mu.Lock()
	if !e.opts.RXOnly {
		e.ackQueue[m.MicroID] = &pendingAck{
			ack: protocol.AckFrame{
				XferID:   m.MicroID,
				PeerID:   myPub.PeerID,
				Complete: true,
				IsMicro:  true,
			},
			ticksLeft: 2,
		}
	}
	if e.isSeenLocked(dedupKey) {
		e.mu.Unlock()
		return
	}
	e.markSeenLocked(dedupKey)

	var senderID crypto.PeerID
	nick := fmt.Sprintf("anon-%02x%02x", m.SenderShort[0], m.SenderShort[1])
	if pid, ok := e.shortPeers[m.SenderShort]; ok {
		senderID = pid
		if p := e.peers[pid]; p != nil {
			p.LastSeen = time.Now()
			p.Signal = signal
			nick = p.Nick
		}
	} else {
		senderID[0], senderID[1] = m.SenderShort[0], m.SenderShort[1]
	}
	e.mu.Unlock()

	if e.opts.OnChat != nil {
		e.opts.OnChat(ChatMessage{
			SenderID: senderID,
			Nick:     nick,
			Text:     m.Text,
			Micro:    true,
			Time:     time.Now(),
		})
	}
}

func (e *Engine) handleData(df *protocol.DataFrame, signal int) {
	myID := e.id.Public().PeerID
	var (
		completedBytes []byte
		startedAt      time.Time
		rxProg         *Progress
	)

	e.mu.Lock()
	e.evictStaleRxLocked()
	rx, ok := e.rxTransfers[df.XferID]
	if !ok {
		if len(e.rxTransfers) >= maxActiveRxTransfers {
			e.evictOldestRxLocked()
		}
		rx = newRxTransfer(df.XferID, e.chunkSize())
		e.rxTransfers[df.XferID] = rx
	}
	wasComplete := rx.completed
	useful, allDone, err := rx.addFrame(df)
	if err != nil {
		e.mu.Unlock()
		return
	}

	if !e.opts.RXOnly && (useful || allDone) {
		ack := rx.buildAck(myID)
		ticks := 1
		if allDone {
			ticks = 3
		}
		e.ackQueue[df.XferID] = &pendingAck{ack: ack, ticksLeft: ticks}
	}

	if useful && !allDone && rx.totalGens > 1 {
		dc, tc, rep := rx.stats()
		elapsed := time.Since(rx.startedAt).Seconds()
		bps := 0.0
		if elapsed > 0.001 {
			bps = float64(dc*rx.chunkSize) / elapsed
		}
		p := Progress{
			Direction:   "rx",
			Label:       fmt.Sprintf("xfer:%04x", df.XferID),
			XferID:      df.XferID,
			TotalGens:   rx.totalGens,
			DoneChunks:  dc,
			TotalChunks: tc,
			RepairsUsed: rep,
			Bytes:       dc * rx.chunkSize,
			BytesPerSec: bps,
		}
		rxProg = &p
	}

	if allDone && !wasComplete {
		completedBytes = rx.completedBytes
		startedAt = rx.startedAt
	}
	e.mu.Unlock()

	if rxProg != nil && e.opts.OnProgress != nil {
		e.opts.OnProgress(*rxProg)
	}
	if completedBytes != nil {
		e.deliverCompletedEnvelope(df.XferID, completedBytes, startedAt, signal)
	}
}

func (e *Engine) deliverCompletedEnvelope(xferID uint16, raw []byte, startedAt time.Time, signal int) {
	ck := e.currentKeys()
	dec, err := protocol.OpenEnvelope(xferID, e.id, ck, raw)
	if err != nil {
		if errors.Is(err, protocol.ErrNotForUs) {
			// Direct E2EE transfer between two other peers; cancel our ACK so only
			// the true recipient ACKs it!
			e.mu.Lock()
			delete(e.ackQueue, xferID)
			e.mu.Unlock()
		}
		return
	}
	if dec.SenderID == e.id.Public().PeerID {
		return
	}

	dedupKey := fmt.Sprintf("env:%x:%04x:%x", dec.SenderID, xferID, dec.Salt)
	e.mu.Lock()
	if e.isSeenLocked(dedupKey) {
		e.mu.Unlock()
		return
	}
	e.markSeenLocked(dedupKey)
	wasKnown := e.peers[dec.SenderID] != nil && e.peers[dec.SenderID].HasPubKey
	peerSnap := e.recordPeerLocked(dec, signal)
	shouldReplyHello := !wasKnown && !e.opts.RXOnly && !e.opts.Stealth && time.Since(e.lastHelloResp) > 250*time.Millisecond
	if shouldReplyHello {
		e.lastHelloResp = time.Now()
	}
	e.mu.Unlock()

	if shouldReplyHello {
		_ = e.QueueHello()
	}

	switch dec.Kind {
	case protocol.KindHello:
		if peerSnap != nil && e.opts.OnPeer != nil {
			e.opts.OnPeer(*peerSnap)
		}
	case protocol.KindText:
		if e.opts.OnChat != nil {
			e.opts.OnChat(ChatMessage{
				SenderID: dec.SenderID,
				Nick:     dec.Text.Nick,
				Text:     dec.Text.Text,
				Direct:   dec.Direct,
				Time:     time.Unix(dec.Text.Timestamp, 0),
			})
		}
	case protocol.KindFile:
		saved, err := e.saveIncomingFile(dec.File.FileName, dec.File.Data)
		if err != nil {
			e.emitEvent("failed saving incoming file %q: %v", dec.File.FileName, err)
			return
		}
		elapsed := time.Since(startedAt)
		if e.opts.OnProgress != nil {
			secs := elapsed.Seconds()
			bps := 0.0
			if secs > 0.0001 {
				bps = float64(len(dec.File.Data)) / secs
			}
			e.opts.OnProgress(Progress{
				Direction:   "rx",
				Label:       "file:" + dec.File.FileName,
				XferID:      xferID,
				Bytes:       len(dec.File.Data),
				BytesPerSec: bps,
				Complete:    true,
			})
		}
		if e.opts.OnFile != nil {
			e.opts.OnFile(ReceivedFile{
				SenderID:  dec.SenderID,
				Nick:      dec.File.Nick,
				FileName:  dec.File.FileName,
				SavedPath: saved,
				Size:      len(dec.File.Data),
				SHA256Hex: hex.EncodeToString(dec.File.SHA256[:]),
				Direct:    dec.Direct,
				Elapsed:   elapsed,
			})
		}
	}
}

func (e *Engine) recordPeerLocked(dec *protocol.DecodedEnvelope, signal int) *PeerInfo {
	p := e.peers[dec.SenderID]
	isNew := false
	if p == nil {
		p = &PeerInfo{PeerID: dec.SenderID, Slots: 1}
		e.peers[dec.SenderID] = p
		isNew = true
	}
	p.LastSeen = time.Now()
	p.Signal = signal
	e.shortPeers[dec.SenderID.Short2()] = dec.SenderID

	switch dec.Kind {
	case protocol.KindHello:
		p.Nick = dec.Hello.Nick
		p.Slots = dec.Hello.Slots
		p.X25519Pub = dec.Hello.X25519Pub
		p.HasPubKey = true
		p.SAS = crypto.SAS(e.id.Public().X25519Pub, dec.Hello.X25519Pub)
	case protocol.KindText:
		if dec.Text.Nick != "" {
			p.Nick = dec.Text.Nick
		}
	case protocol.KindFile:
		if dec.File.Nick != "" {
			p.Nick = dec.File.Nick
		}
	}
	if dec.Direct && dec.SenderPub != ([32]byte{}) {
		p.X25519Pub = dec.SenderPub
		p.HasPubKey = true
		p.SAS = crypto.SAS(e.id.Public().X25519Pub, dec.SenderPub)
	}
	if p.Nick == "" {
		p.Nick = "anon-" + dec.SenderID.Short()
	}
	if isNew || dec.Kind == protocol.KindHello {
		cp := *p
		return &cp
	}
	return nil
}

func (e *Engine) saveIncomingFile(cleanName string, data []byte) (string, error) {
	if err := os.MkdirAll(e.opts.DownloadDir, 0o700); err != nil {
		return "", err
	}
	base := protocol.SanitizeFileName(cleanName)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for i := 0; i < 10000; i++ {
		cand := base
		if i > 0 {
			cand = fmt.Sprintf("%s_%d%s", stem, i, ext)
		}
		full := filepath.Join(e.opts.DownloadDir, cand)
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return "", werr
		}
		if cerr != nil {
			return "", cerr
		}
		return full, nil
	}
	return "", errors.New("too many colliding filenames in download directory")
}

func (e *Engine) isSeenLocked(key string) bool {
	_, ok := e.seenSet[key]
	return ok
}

func (e *Engine) markSeenLocked(key string) {
	if _, ok := e.seenSet[key]; ok {
		return
	}
	if len(e.seenOrder) >= dedupCacheLimit {
		evict := e.seenOrder[0]
		e.seenOrder = e.seenOrder[1:]
		delete(e.seenSet, evict)
	}
	e.seenSet[key] = struct{}{}
	e.seenOrder = append(e.seenOrder, key)
}

func (e *Engine) evictStaleRxLocked() {
	now := time.Now()
	for id, rx := range e.rxTransfers {
		if now.Sub(rx.updatedAt) > rxStaleTTL {
			delete(e.rxTransfers, id)
		}
	}
}

func (e *Engine) evictOldestRxLocked() {
	var oldestID uint16
	var oldestTime time.Time
	first := true
	for id, rx := range e.rxTransfers {
		if first || rx.updatedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = rx.updatedAt
			first = false
		}
	}
	if !first {
		delete(e.rxTransfers, oldestID)
	}
}

func (e *Engine) emitEvent(format string, args ...any) {
	if e.opts.OnEvent != nil {
		e.opts.OnEvent(fmt.Sprintf(format, args...))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
