package protocol

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"wifichat/pkg/crypto"
	"wifichat/pkg/fec"
)

type FrameType uint8

const (
	FrameData  FrameType = 0
	FrameAck   FrameType = 1
	FrameMicro FrameType = 3

	DataHeaderSize = 6
	AckBodyMinSize = 14
	MicroOverhead  = 11

	AckFlagComplete uint8 = 0x01
	AckFlagMicro    uint8 = 0x02

	MaxGenerationCount = 32768
)

var (
	ErrBadFrame    = errors.New("protocol: malformed frame body")
	ErrTooLong     = errors.New("protocol: message does not fit in micro frame")
	ErrNotUTF8     = errors.New("protocol: micro message is not valid UTF-8")
)

// DataFrame carries one systematic or Cauchy GF(256) MDS chunk of a transfer generation.
type DataFrame struct {
	XferID   uint16
	Gen      uint16 // 0..32767
	FinalGen bool
	K        uint8 // 1..64
	Seq      uint8 // 0..255
	Chunk    []byte
}

// AckFrame provides fast selective generation completion & rank feedback.
type AckFrame struct {
	XferID    uint16
	PeerID    crypto.PeerID
	BaseGen   uint16
	GenBitmap uint32
	BaseNeed  uint8
	Complete  bool
	IsMicro   bool
}

// MicroFrame carries a complete short broadcast chat message inside a SINGLE 32-byte SSID.
type MicroFrame struct {
	SenderShort [2]byte
	Nonce       [4]byte
	MicroID     uint16
	Text        string
}

func ChunkDataSize(frameBodySize int) int {
	return frameBodySize - DataHeaderSize
}

func EncodeDataBody(f DataFrame, bodySize int) ([]byte, error) {
	chunkCap := ChunkDataSize(bodySize)
	if chunkCap < 4 || f.K == 0 || int(f.K) > fec.MaxK || f.Gen >= MaxGenerationCount {
		return nil, ErrBadFrame
	}
	if len(f.Chunk) != chunkCap {
		return nil, fmt.Errorf("%w: chunk len=%d want %d", ErrBadFrame, len(f.Chunk), chunkCap)
	}
	out := make([]byte, bodySize)
	out[0] = (uint8(FrameData) << 6) | ((f.K - 1) & 0x3F)
	binary.BigEndian.PutUint16(out[1:3], f.XferID)
	genWord := f.Gen & 0x7FFF
	if f.FinalGen {
		genWord |= 0x8000
	}
	binary.BigEndian.PutUint16(out[3:5], genWord)
	out[5] = f.Seq
	copy(out[6:], f.Chunk)
	return out, nil
}

func EncodeAckBody(a AckFrame, bodySize int) ([]byte, error) {
	if bodySize < AckBodyMinSize {
		return nil, ErrBadFrame
	}
	out := make([]byte, bodySize)
	var flags uint8
	if a.Complete {
		flags |= AckFlagComplete
	}
	if a.IsMicro {
		flags |= AckFlagMicro
	}
	out[0] = (uint8(FrameAck) << 6) | (flags & 0x3F)
	binary.BigEndian.PutUint16(out[1:3], a.XferID)
	copy(out[3:7], a.PeerID[:])
	binary.BigEndian.PutUint16(out[7:9], a.BaseGen)
	binary.BigEndian.PutUint32(out[9:13], a.GenBitmap)
	out[13] = a.BaseNeed
	if bodySize > AckBodyMinSize {
		// Randomize spare bytes so identical ACKs still remain deterministic
		// per call? Keep deterministic per ACK state so dwelling on the same
		// ACK doesn't churn SSID needlessly, seeded from XferID+BaseGen+BaseNeed.
		for i := AckBodyMinSize; i < bodySize; i++ {
			out[i] = byte(int(a.XferID>>uint(i%8)) ^ int(a.BaseGen) ^ int(a.BaseNeed) ^ (i * 37))
		}
	}
	return out, nil
}

// TryEncodeMicroBody attempts to fit a broadcast text message into a single SSID body.
func TryEncodeMicroBody(ck *crypto.ChannelKeys, sender crypto.PeerID, salt [4]byte, text string, bodySize int) (body []byte, microID uint16, ok bool) {
	maxPayload := bodySize - MicroOverhead
	if maxPayload <= 0 || text == "" {
		return nil, 0, false
	}
	raw := []byte(text)
	compressed := false
	payload := raw
	if len(payload) > maxPayload {
		if comp, did := compressDeflate(raw); did && len(comp) <= maxPayload {
			payload = comp
			compressed = true
		} else {
			return nil, 0, false
		}
	}
	if len(payload) > 31 {
		return nil, 0, false
	}

	var hdr7 [7]byte
	var compBit uint8
	if compressed {
		compBit = 0x20
	}
	hdr7[0] = (uint8(FrameMicro) << 6) | compBit | uint8(len(payload)&0x1F)
	hdr7[1] = sender[0]
	hdr7[2] = sender[1]
	copy(hdr7[3:7], salt[:])

	innerMAC, ct, err := ck.SealMicroPayload(hdr7[:], payload)
	if err != nil {
		return nil, 0, false
	}

	out := make([]byte, bodySize)
	copy(out[0:7], hdr7[:])
	copy(out[7:11], innerMAC[:])
	copy(out[11:11+len(ct)], ct)
	if 11+len(ct) < bodySize {
		_, _ = rand.Read(out[11+len(ct):])
	}
	mID := binary.BigEndian.Uint16(salt[0:2]) ^ binary.BigEndian.Uint16(salt[2:4])
	return out, mID, true
}

// ParsedFrame holds the decoded variant of an incoming SSID frame body.
type ParsedFrame struct {
	Type  FrameType
	Data  *DataFrame
	Ack   *AckFrame
	Micro *MicroFrame
}

func DecodeBody(ck *crypto.ChannelKeys, body []byte) (ParsedFrame, error) {
	if len(body) < DataHeaderSize+4 {
		return ParsedFrame{}, ErrBadFrame
	}
	ft := FrameType(body[0] >> 6)
	switch ft {
	case FrameData:
		k := (body[0] & 0x3F) + 1
		if int(k) > fec.MaxK {
			return ParsedFrame{}, ErrBadFrame
		}
		xferID := binary.BigEndian.Uint16(body[1:3])
		genWord := binary.BigEndian.Uint16(body[3:5])
		finalGen := (genWord & 0x8000) != 0
		gen := genWord & 0x7FFF
		seq := body[5]
		chunk := append([]byte(nil), body[6:]...)
		return ParsedFrame{
			Type: FrameData,
			Data: &DataFrame{
				XferID:   xferID,
				Gen:      gen,
				FinalGen: finalGen,
				K:        k,
				Seq:      seq,
				Chunk:    chunk,
			},
		}, nil

	case FrameAck:
		if len(body) < AckBodyMinSize {
			return ParsedFrame{}, ErrBadFrame
		}
		flags := body[0] & 0x3F
		var pid crypto.PeerID
		copy(pid[:], body[3:7])
		return ParsedFrame{
			Type: FrameAck,
			Ack: &AckFrame{
				XferID:    binary.BigEndian.Uint16(body[1:3]),
				PeerID:    pid,
				BaseGen:   binary.BigEndian.Uint16(body[7:9]),
				GenBitmap: binary.BigEndian.Uint32(body[9:13]),
				BaseNeed:  body[13],
				Complete:  (flags & AckFlagComplete) != 0,
				IsMicro:   (flags & AckFlagMicro) != 0,
			},
		}, nil

	case FrameMicro:
		if len(body) < MicroOverhead {
			return ParsedFrame{}, ErrBadFrame
		}
		compressed := (body[0] & 0x20) != 0
		plen := int(body[0] & 0x1F)
		if 11+plen > len(body) {
			return ParsedFrame{}, ErrBadFrame
		}
		var mac [4]byte
		copy(mac[:], body[7:11])
		pt, err := ck.OpenMicroPayload(body[0:7], mac, body[11:11+plen])
		if err != nil {
			return ParsedFrame{}, err
		}
		if compressed {
			pt, err = decompressDeflate(pt, 256)
			if err != nil {
				return ParsedFrame{}, err
			}
		}
		if !utf8.Valid(pt) {
			return ParsedFrame{}, ErrNotUTF8
		}
		var short2 [2]byte
		short2[0], short2[1] = body[1], body[2]
		var nonce4 [4]byte
		copy(nonce4[:], body[3:7])
		mID := binary.BigEndian.Uint16(nonce4[0:2]) ^ binary.BigEndian.Uint16(nonce4[2:4])
		return ParsedFrame{
			Type: FrameMicro,
			Micro: &MicroFrame{
				SenderShort: short2,
				Nonce:       nonce4,
				MicroID:     mID,
				Text:        string(pt),
			},
		}, nil

	default:
		return ParsedFrame{}, ErrBadFrame
	}
}

func compressDeflate(in []byte) ([]byte, bool) {
	if len(in) < 12 {
		return in, false
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return in, false
	}
	if _, err := w.Write(in); err != nil {
		_ = w.Close()
		return in, false
	}
	if err := w.Close(); err != nil {
		return in, false
	}
	if buf.Len() < len(in) {
		return buf.Bytes(), true
	}
	return in, false
}

func decompressDeflate(in []byte, maxBytes int) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(in))
	defer r.Close()
	lr := io.LimitReader(r, int64(maxBytes)+1)
	out, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(out) > maxBytes {
		return nil, errors.New("protocol: decompressed payload exceeds limit")
	}
	return out, nil
}
