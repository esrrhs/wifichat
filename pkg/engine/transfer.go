package engine

import (
	"fmt"
	"math"
	"time"

	"wifichat/pkg/crypto"
	"wifichat/pkg/fec"
	"wifichat/pkg/protocol"
)

type txKind int

const (
	txKindEnvelope txKind = iota
	txKindMicro
)

type txJob struct {
	kind        txKind
	label       string // "hello", "text", "file:<name>"
	xferID      uint16
	direct      bool
	targetPeer  crypto.PeerID
	requireAck  bool
	rawBytes    int
	startedAt   time.Time
	doneCh      chan error
	completed   bool
	ticksActive int
	minAirTicks int

	// For txKindMicro (1-SSID fast path)
	microBody     []byte
	microHoldLeft int

	// For txKindEnvelope (multi-generation Cauchy GF(256) fountain)
	gens           []*txGen
	baseGen        int
	windowSize     int
	rrCursor       int
	blindRedund    float64
	proactiveRedun float64
}

type txGen struct {
	index        uint16
	final        bool
	enc          *fec.Encoder
	nextSeq      uint8
	emitted      int
	targetEmit   int // forinitial pass / blind mode
	repairCredit int // priority repair chunks requested by receiver ACK
	acked        bool
}

func newMicroJob(microID uint16, body []byte, holdTicks int) *txJob {
	if holdTicks < 4 {
		holdTicks = 4
	}
	return &txJob{
		kind:          txKindMicro,
		label:         "micro-text",
		xferID:        microID,
		rawBytes:      len(body),
		startedAt:     time.Now(),
		doneCh:        make(chan error, 1),
		microBody:     body,
		microHoldLeft: holdTicks,
	}
}

func newEnvelopeJob(
	label string,
	xferID uint16,
	envelope []byte,
	chunkSize int,
	direct bool,
	target crypto.PeerID,
	blindRedundancy float64,
) (*txJob, error) {
	genCapBytes := fec.MaxK * chunkSize
	numGens := (len(envelope) + genCapBytes - 1) / genCapBytes
	if numGens == 0 {
		numGens = 1
	}
	if numGens > protocol.MaxGenerationCount {
		return nil, fmt.Errorf("engine: transfer requires %d generations (max %d)", numGens, protocol.MaxGenerationCount)
	}

	if blindRedundancy <= 0 {
		blindRedundancy = 0.55
	}
	proactive := 0.20

	gens := make([]*txGen, numGens)
	for g := 0; g < numGens; g++ {
		start := g * genCapBytes
		end := start + genCapBytes
		if end > len(envelope) {
			end = len(envelope)
		}
		enc, err := fec.NewEncoder(envelope[start:end], chunkSize)
		if err != nil {
			return nil, err
		}
		k := enc.K()
		var targetEmit int
		if direct {
			// In interactive Direct mode, send K systematic + small proactive parity cushion
			// then immediately pipeline next generation while awaiting bitmap ACK.
			cushion := int(math.Ceil(float64(k) * proactive))
			if cushion < 1 && k > 1 {
				cushion = 1
			}
			targetEmit = k + cushion
		} else {
			// In Broadcast mode, emit enough MDS parity chunks so even completely passive
			// (--rx-only) listeners under wireless scan loss reconstruct 100% in one pass.
			extra := int(math.Ceil(float64(k) * blindRedundancy))
			if extra < 4 {
				extra = 4
			}
			targetEmit = k + extra
		}
		gens[g] = &txGen{
			index:      uint16(g),
			final:      g == numGens-1,
			enc:        enc,
			targetEmit: targetEmit,
		}
	}

	win := 4
	if numGens < win {
		win = numGens
	}

	minTicks := 1
	if !direct {
		// Ensure multi-slot APs keep short broadcasts (e.g. Hello or short chat)
		// on the air for at least 4 dwell cycles so async scanners catch them.
		minTicks = 4
	}

	return &txJob{
		kind:           txKindEnvelope,
		label:          label,
		xferID:         xferID,
		direct:         direct,
		targetPeer:     target,
		requireAck:     direct,
		rawBytes:       len(envelope),
		startedAt:      time.Now(),
		doneCh:         make(chan error, 1),
		minAirTicks:    minTicks,
		gens:           gens,
		windowSize:     win,
		blindRedund:    blindRedundancy,
		proactiveRedun: proactive,
	}, nil
}

func (j *txJob) finish(err error) {
	if j.completed {
		return
	}
	j.completed = true
	select {
	case j.doneCh <- err:
	default:
	}
	close(j.doneCh)
}

// nextFrameBodies produces up to maxFrames frame bodies for the current dwell tick.
func (j *txJob) nextFrameBodies(bodySize int, maxFrames int) (bodies [][]byte, done bool) {
	if j.completed {
		return nil, true
	}
	if j.kind == txKindMicro {
		if j.microHoldLeft <= 0 {
			j.finish(nil)
			return nil, true
		}
		j.microHoldLeft--
		return [][]byte{j.microBody}, false
	}

	j.ticksActive++

	// Advance baseGen past any already-acked generations.
	j.advanceBase()
	if j.baseGen >= len(j.gens) {
		j.finish(nil)
		return nil, true
	}

	winEnd := j.baseGen + j.windowSize
	if winEnd > len(j.gens) {
		winEnd = len(j.gens)
	}

	for len(bodies) < maxFrames {
		var chosen *txGen

		// 1. Highest priority: receiver-requested repair credits in window.
		for g := j.baseGen; g < winEnd; g++ {
			gen := j.gens[g]
			if !gen.acked && gen.repairCredit > 0 {
				chosen = gen
				gen.repairCredit--
				break
			}
		}

		// 2. Initial systematic + cushion pass across the sliding generation window.
		if chosen == nil {
			for g := j.baseGen; g < winEnd; g++ {
				gen := j.gens[g]
				if !gen.acked && gen.emitted < gen.targetEmit {
					chosen = gen
					break
				}
			}
		}

		// 3. In Broadcast mode (!requireAck), once all generations in window hit
		// targetEmit AND we have stayed on air for at least minAirTicks, retire them.
		if chosen == nil && !j.requireAck && j.ticksActive > j.minAirTicks {
			for g := j.baseGen; g < winEnd; g++ {
				if j.gens[g].emitted >= j.gens[g].targetEmit && j.gens[g].repairCredit == 0 {
					j.gens[g].acked = true
				}
			}
			j.advanceBase()
			if j.baseGen >= len(j.gens) {
				if len(bodies) == 0 {
					j.finish(nil)
					return nil, true
				}
				return bodies, false
			}
			winEnd = j.baseGen + j.windowSize
			if winEnd > len(j.gens) {
				winEnd = len(j.gens)
			}
			continue
		}

		// 4. Fountain continuation: emit fresh MDS parity chunks round-robin across
		// unacked generations in window so every SSID slot carries linearly independent data!
		if chosen == nil {
			span := winEnd - j.baseGen
			for step := 0; step < span; step++ {
				idx := j.baseGen + ((j.rrCursor + step) % span)
				if !j.gens[idx].acked {
					chosen = j.gens[idx]
					j.rrCursor = (idx - j.baseGen + 1) % span
					break
				}
			}
		}
		if chosen == nil {
			break
		}

		seq := chosen.nextSeq
		chosen.nextSeq++
		chosen.emitted++
		chunk := chosen.enc.Encode(seq)
		b, err := protocol.EncodeDataBody(protocol.DataFrame{
			XferID:   j.xferID,
			Gen:      chosen.index,
			FinalGen: chosen.final,
			K:        uint8(chosen.enc.K()),
			Seq:      seq,
			Chunk:    chunk,
		}, bodySize)
		if err != nil {
			continue
		}
		bodies = append(bodies, b)
	}
	return bodies, false
}

func (j *txJob) advanceBase() {
	for j.baseGen < len(j.gens) && j.gens[j.baseGen].acked {
		j.baseGen++
	}
}

func (j *txJob) applyAck(ack *protocol.AckFrame) bool {
	if j.completed || ack.XferID != j.xferID || j.kind == txKindMicro || ack.IsMicro {
		return false
	}
	// In 1-to-many Broadcast mode (!j.direct), one fast peer's Complete ACK must
	// NEVER abort transmission early for other nearby peers; we only honor partial
	// repair requests (BaseNeed > 0) to assist slower receivers!
	if !j.direct {
		if !ack.Complete && int(ack.BaseGen) < len(j.gens) && ack.BaseNeed > 0 {
			gen := j.gens[ack.BaseGen]
			if !gen.acked {
				want := int(ack.BaseNeed) + 1
				if gen.repairCredit < want {
					gen.repairCredit = want
				}
			}
		}
		return false
	}

	if !j.targetPeer.IsZero() && ack.PeerID != j.targetPeer {
		return false
	}

	if ack.Complete {
		for _, g := range j.gens {
			g.acked = true
		}
		j.baseGen = len(j.gens)
		j.finish(nil)
		return true
	}

	limit := int(ack.BaseGen)
	if limit > len(j.gens) {
		limit = len(j.gens)
	}
	for g := 0; g < limit; g++ {
		j.gens[g].acked = true
		j.gens[g].repairCredit = 0
	}
	for bit := 0; bit < 32; bit++ {
		if (ack.GenBitmap & (uint32(1) << bit)) != 0 {
			g := int(ack.BaseGen) + bit
			if g < len(j.gens) {
				j.gens[g].acked = true
				j.gens[g].repairCredit = 0
			}
		}
	}
	if int(ack.BaseGen) < len(j.gens) && !j.gens[ack.BaseGen].acked && ack.BaseNeed > 0 {
		want := int(ack.BaseNeed) + 1
		if j.gens[ack.BaseGen].repairCredit < want {
			j.gens[ack.BaseGen].repairCredit = want
		}
	}
	j.advanceBase()
	if j.baseGen >= len(j.gens) {
		j.finish(nil)
		return true
	}
	return false
}

// rxTransfer tracks an incoming multi-generation Cauchy MDS transfer.
type rxTransfer struct {
	xferID       uint16
	chunkSize    int
	startedAt    time.Time
	updatedAt    time.Time
	totalGens    int // 0 until a FinalGen frame is seen
	gens         map[uint16]*fec.Decoder
	completed    bool
	completedBytes []byte
}

func newRxTransfer(xferID uint16, chunkSize int) *rxTransfer {
	now := time.Now()
	return &rxTransfer{
		xferID:    xferID,
		chunkSize: chunkSize,
		startedAt: now,
		updatedAt: now,
		gens:      make(map[uint16]*fec.Decoder),
	}
}

func (rx *rxTransfer) addFrame(df *protocol.DataFrame) (newlyUseful bool, allComplete bool, err error) {
	rx.updatedAt = time.Now()
	if rx.completed {
		return false, true, nil
	}
	if df.FinalGen {
		rx.totalGens = int(df.Gen) + 1
	}
	dec, ok := rx.gens[df.Gen]
	if !ok {
		if len(rx.gens) >= 4096 {
			return false, false, fmt.Errorf("too many generations")
		}
		dec, err = fec.NewDecoder(int(df.K), rx.chunkSize)
		if err != nil {
			return false, false, err
		}
		rx.gens[df.Gen] = dec
	} else if dec.K() != int(df.K) {
		return false, false, fmt.Errorf("inconsistent K for generation %d", df.Gen)
	}

	_, useful, err := dec.Add(df.Seq, df.Chunk)
	if err != nil {
		return false, false, err
	}
	if rx.totalGens > 0 && len(rx.gens) >= rx.totalGens {
		doneAll := true
		totalLen := 0
		for g := 0; g < rx.totalGens; g++ {
			gd := rx.gens[uint16(g)]
			if gd == nil || !gd.Complete() {
				doneAll = false
				break
			}
			totalLen += gd.K() * rx.chunkSize
		}
		if doneAll {
			buf := make([]byte, 0, totalLen)
			for g := 0; g < rx.totalGens; g++ {
				b, _ := rx.gens[uint16(g)].Bytes()
				buf = append(buf, b...)
			}
			rx.completed = true
			rx.completedBytes = buf
			return useful, true, nil
		}
	}
	return useful, false, nil
}

func (rx *rxTransfer) buildAck(myPeer crypto.PeerID) protocol.AckFrame {
	if rx.completed {
		return protocol.AckFrame{
			XferID:   rx.xferID,
			PeerID:   myPeer,
			BaseGen:  uint16(rx.totalGens),
			Complete: true,
		}
	}
	var base uint16
	for {
		d := rx.gens[base]
		if d != nil && d.Complete() {
			base++
			continue
		}
		break
	}
	var bitmap uint32
	for bit := 0; bit < 32; bit++ {
		d := rx.gens[base+uint16(bit)]
		if d != nil && d.Complete() {
			bitmap |= uint32(1) << bit
		}
	}
	var need uint8
	if d := rx.gens[base]; d != nil {
		need = uint8(d.Needed())
	}
	return protocol.AckFrame{
		XferID:    rx.xferID,
		PeerID:    myPeer,
		BaseGen:   base,
		GenBitmap: bitmap,
		BaseNeed:  need,
	}
}

func (rx *rxTransfer) stats() (doneChunks, knownTotalChunks, repairs int) {
	for _, d := range rx.gens {
		doneChunks += d.Rank()
		knownTotalChunks += d.K()
		repairs += d.RepairsUsed()
	}
	return
}
