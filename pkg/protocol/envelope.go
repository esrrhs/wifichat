package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"wifichat/pkg/crypto"
)

type EnvelopeKind uint8

const (
	KindHello EnvelopeKind = 1
	KindText  EnvelopeKind = 2
	KindFile  EnvelopeKind = 3

	envelopeVersion uint8 = 1
	flagDirect      uint8 = 1 << 3
	flagCompressed  uint8 = 1 << 4

	MaxEnvelopePlaintext = 8 * 1024 * 1024 // 8 MiB max uncompressed file/text payload
	MaxClockSkew         = 10 * time.Minute
)

var (
	ErrBadEnvelope     = errors.New("protocol: malformed envelope")
	ErrNotForUs        = errors.New("protocol: direct E2EE envelope addressed to another peer")
	ErrSenderSpoof     = errors.New("protocol: sender PeerID does not match X25519 public key")
	ErrReplayTimestamp = errors.New("protocol: envelope timestamp outside allowed window")
	ErrFileIntegrity   = errors.New("protocol: file SHA-256 integrity mismatch")
)

type HelloPayload struct {
	Timestamp int64
	Slots     uint8
	X25519Pub [32]byte
	Nick      string
}

type TextPayload struct {
	Timestamp int64
	Nick      string
	Text      string
}

type FilePayload struct {
	Timestamp int64
	Nick      string
	FileName  string
	SHA256    [32]byte
	Data      []byte
}

type DecodedEnvelope struct {
	Kind        EnvelopeKind
	Direct      bool
	Compressed  bool
	SenderID    crypto.PeerID
	RecipientID crypto.PeerID
	SenderPub   [32]byte // populated on KindHello or Direct E2EE messages
	Salt        [4]byte
	Hello       *HelloPayload
	Text        *TextPayload
	File        *FilePayload
}

// SealOptions configures how an Envelope is encrypted (Room broadcast vs Direct X25519 E2EE).
type SealOptions struct {
	XferID        uint16
	Sender        *crypto.Identity
	Channel       *crypto.ChannelKeys
	Direct        bool
	RecipientID   crypto.PeerID
	RecipientXPub [32]byte
}

func SealHello(opts SealOptions, slots uint8) ([]byte, error) {
	pub := opts.Sender.Public()
	if slots == 0 {
		slots = 1
	}
	inner := encodeHello(HelloPayload{
		Timestamp: time.Now().Unix(),
		Slots:     slots,
		X25519Pub: pub.X25519Pub,
		Nick:      pub.Nick,
	})
	opts.Direct = false
	return sealRawEnvelope(opts, KindHello, inner)
}

func SealText(opts SealOptions, text string) ([]byte, error) {
	pub := opts.Sender.Public()
	inner := encodeText(TextPayload{
		Timestamp: time.Now().Unix(),
		Nick:      pub.Nick,
		Text:      text,
	})
	return sealRawEnvelope(opts, KindText, inner)
}

func SealFile(opts SealOptions, fileName string, data []byte) ([]byte, error) {
	if len(data) > MaxEnvelopePlaintext-256 {
		return nil, fmt.Errorf("file size %d exceeds limit (%d bytes)", len(data), MaxEnvelopePlaintext-256)
	}
	cleanName := SanitizeFileName(fileName)
	pub := opts.Sender.Public()
	inner := encodeFile(FilePayload{
		Timestamp: time.Now().Unix(),
		Nick:      pub.Nick,
		FileName:  cleanName,
		SHA256:    sha256.Sum256(data),
		Data:      data,
	})
	return sealRawEnvelope(opts, KindFile, inner)
}

func sealRawEnvelope(opts SealOptions, kind EnvelopeKind, inner []byte) ([]byte, error) {
	pub := opts.Sender.Public()
	salt := opts.Sender.NextSalt()

	payload, didCompress := compressDeflate(inner)
	flags := (envelopeVersion << 5) | (uint8(kind) & 0x07)
	if opts.Direct {
		flags |= flagDirect
	}
	if didCompress {
		flags |= flagCompressed
	}

	hdrLen := 1 + 4 + 4
	if opts.Direct {
		hdrLen += 4 + 32
	}
	hdr := make([]byte, hdrLen)
	hdr[0] = flags
	copy(hdr[1:5], pub.PeerID[:])
	copy(hdr[5:9], salt[:])
	if opts.Direct {
		copy(hdr[9:13], opts.RecipientID[:])
		copy(hdr[13:45], pub.X25519Pub[:])
	}

	var aeadKey [32]byte
	if opts.Direct {
		shared, err := opts.Sender.ECDH(opts.RecipientXPub)
		if err != nil {
			return nil, fmt.Errorf("x25519 ecdh: %w", err)
		}
		aeadKey = opts.Channel.DeriveDirectKey(shared)
	} else {
		aeadKey = opts.Channel.RoomKey()
	}

	ad := buildEnvelopeAD(opts.XferID, hdr)
	ct, err := crypto.SealAEAD(aeadKey, ad, payload, ad)
	if err != nil {
		return nil, err
	}

	bodyLen := len(hdr) + len(ct)
	var out []byte
	out = binary.AppendUvarint(out, uint64(bodyLen))
	out = append(out, hdr...)
	out = append(out, ct...)
	return out, nil
}

// OpenEnvelope parses, authenticates, and decrypts a reassembled multi-chunk Envelope.
func OpenEnvelope(xferID uint16, local *crypto.Identity, ck *crypto.ChannelKeys, raw []byte) (*DecodedEnvelope, error) {
	bodyLen, n := binary.Uvarint(raw)
	if n <= 0 || bodyLen == 0 || uint64(len(raw)-n) < bodyLen {
		return nil, ErrBadEnvelope
	}
	env := raw[n : n+int(bodyLen)]
	if len(env) < 1+4+4+16 {
		return nil, ErrBadEnvelope
	}

	flags := env[0]
	ver := flags >> 5
	if ver != envelopeVersion {
		return nil, ErrBadEnvelope
	}
	kind := EnvelopeKind(flags & 0x07)
	direct := (flags & flagDirect) != 0
	compressed := (flags & flagCompressed) != 0

	hdrLen := 1 + 4 + 4
	if direct {
		hdrLen += 4 + 32
	}
	if len(env) < hdrLen+16 {
		return nil, ErrBadEnvelope
	}
	hdr := env[:hdrLen]
	ct := env[hdrLen:]

	var senderID, recipientID crypto.PeerID
	var senderPub [32]byte
	var salt [4]byte
	copy(senderID[:], hdr[1:5])
	copy(salt[:], hdr[5:9])

	var aeadKey [32]byte
	if direct {
		copy(recipientID[:], hdr[9:13])
		copy(senderPub[:], hdr[13:45])
		if local == nil || local.Public().PeerID != recipientID {
			return nil, ErrNotForUs
		}
		if crypto.DerivePeerID(senderPub) != senderID {
			return nil, ErrSenderSpoof
		}
		shared, err := local.ECDH(senderPub)
		if err != nil {
			return nil, err
		}
		aeadKey = ck.DeriveDirectKey(shared)
	} else {
		aeadKey = ck.RoomKey()
	}

	ad := buildEnvelopeAD(xferID, hdr)
	pt, err := crypto.OpenAEAD(aeadKey, ad, ct, ad)
	if err != nil {
		return nil, err
	}
	if compressed {
		pt, err = decompressDeflate(pt, MaxEnvelopePlaintext)
		if err != nil {
			return nil, err
		}
	}

	dec := &DecodedEnvelope{
		Kind:        kind,
		Direct:      direct,
		Compressed:  compressed,
		SenderID:    senderID,
		RecipientID: recipientID,
		SenderPub:   senderPub,
		Salt:        salt,
	}

	switch kind {
	case KindHello:
		h, err := decodeHello(pt)
		if err != nil {
			return nil, err
		}
		if crypto.DerivePeerID(h.X25519Pub) != senderID {
			return nil, ErrSenderSpoof
		}
		if err := checkTimestamp(h.Timestamp); err != nil {
			return nil, err
		}
		dec.SenderPub = h.X25519Pub
		dec.Hello = &h
	case KindText:
		tm, err := decodeText(pt)
		if err != nil {
			return nil, err
		}
		if err := checkTimestamp(tm.Timestamp); err != nil {
			return nil, err
		}
		dec.Text = &tm
	case KindFile:
		fm, err := decodeFile(pt)
		if err != nil {
			return nil, err
		}
		if err := checkTimestamp(fm.Timestamp); err != nil {
			return nil, err
		}
		dec.File = &fm
	default:
		return nil, ErrBadEnvelope
	}
	return dec, nil
}

func buildEnvelopeAD(xferID uint16, hdr []byte) []byte {
	ad := make([]byte, len("wifichat-env-v1")+2+len(hdr))
	n := copy(ad, "wifichat-env-v1")
	binary.BigEndian.PutUint16(ad[n:n+2], xferID)
	copy(ad[n+2:], hdr)
	return ad
}

func checkTimestamp(ts int64) error {
	skew := time.Since(time.Unix(ts, 0))
	if skew > MaxClockSkew || skew < -MaxClockSkew {
		return ErrReplayTimestamp
	}
	return nil
}

func encodeHello(h HelloPayload) []byte {
	nick := truncateUTF8(h.Nick, crypto.MaxNickBytes)
	b := make([]byte, 8+1+32+1+len(nick))
	binary.BigEndian.PutUint64(b[0:8], uint64(h.Timestamp))
	b[8] = h.Slots
	copy(b[9:41], h.X25519Pub[:])
	b[41] = uint8(len(nick))
	copy(b[42:], nick)
	return b
}

func decodeHello(b []byte) (HelloPayload, error) {
	var h HelloPayload
	if len(b) < 42 {
		return h, ErrBadEnvelope
	}
	h.Timestamp = int64(binary.BigEndian.Uint64(b[0:8]))
	h.Slots = b[8]
	if h.Slots == 0 {
		h.Slots = 1
	}
	copy(h.X25519Pub[:], b[9:41])
	nlen := int(b[41])
	if 42+nlen != len(b) || nlen > crypto.MaxNickBytes || !utf8.Valid(b[42:]) {
		return h, ErrBadEnvelope
	}
	h.Nick = string(b[42:])
	return h, nil
}

func encodeText(t TextPayload) []byte {
	nick := truncateUTF8(t.Nick, crypto.MaxNickBytes)
	b := make([]byte, 8+1+len(nick)+len(t.Text))
	binary.BigEndian.PutUint64(b[0:8], uint64(t.Timestamp))
	b[8] = uint8(len(nick))
	copy(b[9:9+len(nick)], nick)
	copy(b[9+len(nick):], t.Text)
	return b
}

func decodeText(b []byte) (TextPayload, error) {
	var t TextPayload
	if len(b) < 9 {
		return t, ErrBadEnvelope
	}
	t.Timestamp = int64(binary.BigEndian.Uint64(b[0:8]))
	nlen := int(b[8])
	if 9+nlen > len(b) || nlen > crypto.MaxNickBytes {
		return t, ErrBadEnvelope
	}
	if !utf8.Valid(b[9:9+nlen]) || !utf8.Valid(b[9+nlen:]) {
		return t, ErrBadEnvelope
	}
	t.Nick = string(b[9 : 9+nlen])
	t.Text = string(b[9+nlen:])
	return t, nil
}

func encodeFile(f FilePayload) []byte {
	nick := truncateUTF8(f.Nick, crypto.MaxNickBytes)
	name := truncateUTF8(SanitizeFileName(f.FileName), 128)
	b := make([]byte, 8+1+len(nick)+1+len(name)+32+len(f.Data))
	off := 0
	binary.BigEndian.PutUint64(b[off:off+8], uint64(f.Timestamp))
	off += 8
	b[off] = uint8(len(nick))
	off++
	copy(b[off:off+len(nick)], nick)
	off += len(nick)
	b[off] = uint8(len(name))
	off++
	copy(b[off:off+len(name)], name)
	off += len(name)
	copy(b[off:off+32], f.SHA256[:])
	off += 32
	copy(b[off:], f.Data)
	return b
}

func decodeFile(b []byte) (FilePayload, error) {
	var f FilePayload
	if len(b) < 8+1+1+32 {
		return f, ErrBadEnvelope
	}
	off := 0
	f.Timestamp = int64(binary.BigEndian.Uint64(b[off : off+8]))
	off += 8
	nlen := int(b[off])
	off++
	if off+nlen+1 > len(b) || nlen > crypto.MaxNickBytes || !utf8.Valid(b[off:off+nlen]) {
		return f, ErrBadEnvelope
	}
	f.Nick = string(b[off : off+nlen])
	off += nlen

	flen := int(b[off])
	off++
	if flen == 0 || flen > 128 || off+flen+32 > len(b) || !utf8.Valid(b[off:off+flen]) {
		return f, ErrBadEnvelope
	}
	f.FileName = SanitizeFileName(string(b[off : off+flen]))
	off += flen

	copy(f.SHA256[:], b[off:off+32])
	off += 32
	f.Data = append([]byte(nil), b[off:]...)

	if sha256.Sum256(f.Data) != f.SHA256 {
		return f, ErrFileIntegrity
	}
	return f, nil
}

// SanitizeFileName strips any directory traversal or control characters.
func SanitizeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(name, ".")
	if name == "" || name == "_" {
		name = "received.bin"
	}
	return truncateUTF8(name, 120)
}

func truncateUTF8(s string, maxBytes int) string {
	for len(s) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
