package crypto

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	FrameTagSize = 4
	MaxNickBytes = 16
)

var (
	ErrFrameAuth = errors.New("crypto: SSID frame MAC verification failed")
	ErrAEADAuth  = errors.New("crypto: envelope AEAD authentication failed")
	ErrMicroAuth = errors.New("crypto: micro frame authentication failed")
)

// PeerID is a 4-byte anonymous ephemeral identifier derived from an X25519 public key.
type PeerID [4]byte

func DerivePeerID(x25519Pub [32]byte) PeerID {
	h, _ := blake2s.New256(nil)
	h.Write([]byte("wifichat-peerid-v1"))
	h.Write(x25519Pub[:])
	sum := h.Sum(nil)
	var id PeerID
	copy(id[:], sum[:4])
	return id
}

func (p PeerID) Hex() string     { return hex.EncodeToString(p[:]) }
func (p PeerID) Short() string   { return hex.EncodeToString(p[:2]) }
func (p PeerID) IsZero() bool    { return p == PeerID{} }
func (p PeerID) Short2() [2]byte { return [2]byte{p[0], p[1]} }

// IdentityPublic holds the shareable anonymous ephemeral parameters of a client.
type IdentityPublic struct {
	PeerID    PeerID
	X25519Pub [32]byte
	Nick      string
}

// Identity is an in-memory-only ephemeral client identity. No keys are ever
// persisted to disk, providing anonymity and forward secrecy across restarts
// and /rotate invocations.
type Identity struct {
	mu       sync.RWMutex
	priv     [32]byte
	pub      [32]byte
	peerID   PeerID
	nick     string
	autoNick bool
	counter  atomic.Uint32
}

func NewIdentity(customNick string) (*Identity, error) {
	id := &Identity{}
	customNick = sanitizeNick(customNick)
	if customNick == "" {
		id.autoNick = true
	} else {
		id.nick = customNick
	}
	if err := id.Rotate(); err != nil {
		return nil, err
	}
	return id, nil
}

func sanitizeNick(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return -1
		}
		return r
	}, s)
	for len(s) > MaxNickBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// Rotate generates a fresh X25519 keypair and anonymous PeerID in memory.
func (id *Identity) Rotate() error {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return fmt.Errorf("generate ephemeral key: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pubSlice, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return err
	}
	var pub [32]byte
	copy(pub[:], pubSlice)
	pid := DerivePeerID(pub)

	id.mu.Lock()
	id.priv = priv
	id.pub = pub
	id.peerID = pid
	if id.autoNick {
		id.nick = "anon-" + pid.Short()
	}
	id.mu.Unlock()
	return nil
}

func (id *Identity) SetNick(nick string) {
	clean := sanitizeNick(nick)
	id.mu.Lock()
	defer id.mu.Unlock()
	if clean == "" {
		id.autoNick = true
		id.nick = "anon-" + id.peerID.Short()
	} else {
		id.autoNick = false
		id.nick = clean
	}
}

func (id *Identity) Public() IdentityPublic {
	id.mu.RLock()
	defer id.mu.RUnlock()
	return IdentityPublic{
		PeerID:    id.peerID,
		X25519Pub: id.pub,
		Nick:      id.nick,
	}
}

// NextSalt returns a 4-byte salt combining a 16-bit monotonic counter and 16-bit CSPRNG.
func (id *Identity) NextSalt() [4]byte {
	seq := uint16(id.counter.Add(1))
	var out [4]byte
	binary.BigEndian.PutUint16(out[0:2], seq)
	_, _ = rand.Read(out[2:4])
	return out
}

// ECDH performs X25519 key agreement with a peer's ephemeral public key.
func (id *Identity) ECDH(peerPub [32]byte) ([32]byte, error) {
	id.mu.RLock()
	priv := id.priv
	id.mu.RUnlock()

	var shared [32]byte
	out, err := curve25519.X25519(priv[:], peerPub[:])
	if err != nil {
		return shared, err
	}
	copy(shared[:], out)
	return shared, nil
}

// SAS computes a symmetric 6-hex-character Short Authentication String (e.g. "7F2-C19")
// for out-of-band visual MITM verification between two nearby peers.
func SAS(pubA, pubB [32]byte) string {
	first, second := pubA, pubB
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	h, _ := blake2s.New256(nil)
	h.Write([]byte("wifichat-sas-v1"))
	h.Write(first[:])
	h.Write(second[:])
	sum := h.Sum(nil)
	s := strings.ToUpper(hex.EncodeToString(sum[:3]))
	return s[:3] + "-" + s[3:]
}

// ChannelKeys holds domain-separated symmetric keys for an anonymous room/channel.
type ChannelKeys struct {
	isOpen       bool
	frameMACKey  [32]byte
	frameMaskKey [32]byte
	roomAEADKey  [32]byte
	microMACKey  [32]byte
	microEncKey  [32]byte
	channelSalt  [16]byte
}

// DeriveChannelKeys derives all symmetric subkeys from a passphrase via Argon2id + HKDF.
// If passphrase is empty, the default open nearby anonymous channel key is used.
func DeriveChannelKeys(passphrase string) *ChannelKeys {
	var master [32]byte
	isOpen := passphrase == ""
	if isOpen {
		master = sha256.Sum256([]byte("wifichat-open-nearby-channel-v1"))
	} else {
		derived := argon2.IDKey(
			[]byte(passphrase),
			[]byte("wifichat-argon2id-salt-v1"),
			1,
			32*1024, // 32 MiB memory-hard cost
			2,
			32,
		)
		copy(master[:], derived)
	}

	ck := &ChannelKeys{isOpen: isOpen}
	expandKey := func(info string, dst []byte) {
		r := hkdf.New(sha256.New, master[:], []byte("wifichat-hkdf-v1"), []byte(info))
		if _, err := io.ReadFull(r, dst); err != nil {
			panic("hkdf expand: " + err.Error())
		}
	}
	expandKey("frame-mac", ck.frameMACKey[:])
	expandKey("frame-mask", ck.frameMaskKey[:])
	expandKey("room-aead", ck.roomAEADKey[:])
	expandKey("micro-mac", ck.microMACKey[:])
	expandKey("micro-enc", ck.microEncKey[:])
	expandKey("channel-salt", ck.channelSalt[:])
	return ck
}

func (ck *ChannelKeys) IsOpen() bool          { return ck.isOpen }
func (ck *ChannelKeys) RoomKey() [32]byte     { return ck.roomAEADKey }
func (ck *ChannelKeys) MicroMACKey() [32]byte { return ck.microMACKey }
func (ck *ChannelKeys) MicroEncKey() [32]byte { return ck.microEncKey }

// DeriveDirectKey derives the ChaCha20-Poly1305 key for X25519 E2EE direct transfers.
func (ck *ChannelKeys) DeriveDirectKey(sharedSecret [32]byte) [32]byte {
	r := hkdf.New(sha256.New, sharedSecret[:], ck.channelSalt[:], []byte("wifichat-dm-aead-v1"))
	var k [32]byte
	if _, err := io.ReadFull(r, k[:]); err != nil {
		panic("hkdf direct: " + err.Error())
	}
	return k
}

// SealFrame applies Synthetic-IV (MAC-then-mask) protection to an SSID frame body.
// Output length is FrameTagSize (4) + len(body). Every bit of header & body is
// masked so passive Wi-Fi observers see uniform random bytes with no magic prefix.
func (ck *ChannelKeys) SealFrame(body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > 32 {
		return nil, errors.New("crypto: invalid frame body size")
	}
	mac, _ := blake2s.New256(ck.frameMACKey[:])
	mac.Write(body)
	tag := mac.Sum(nil)[:FrameTagSize]

	prf, _ := blake2s.New256(ck.frameMaskKey[:])
	prf.Write(tag)
	mask := prf.Sum(nil)

	out := make([]byte, FrameTagSize+len(body))
	copy(out[:FrameTagSize], tag)
	for i := range body {
		out[FrameTagSize+i] = body[i] ^ mask[i]
	}
	return out, nil
}

// OpenFrame verifies the 32-bit keyed BLAKE2s MAC and unmasks the SSID frame body.
func (ck *ChannelKeys) OpenFrame(raw []byte) ([]byte, error) {
	if len(raw) <= FrameTagSize || len(raw)-FrameTagSize > 32 {
		return nil, ErrFrameAuth
	}
	tag := raw[:FrameTagSize]
	ct := raw[FrameTagSize:]

	prf, _ := blake2s.New256(ck.frameMaskKey[:])
	prf.Write(tag)
	mask := prf.Sum(nil)

	body := make([]byte, len(ct))
	for i := range ct {
		body[i] = ct[i] ^ mask[i]
	}

	mac, _ := blake2s.New256(ck.frameMACKey[:])
	mac.Write(body)
	want := mac.Sum(nil)[:FrameTagSize]
	if subtle.ConstantTimeCompare(tag, want) != 1 {
		return nil, ErrFrameAuth
	}
	return body, nil
}

func deriveNonce12(nonceSeed []byte) [12]byte {
	h, _ := blake2s.New256(nil)
	h.Write([]byte("wifichat-aead-nonce-v1"))
	h.Write(nonceSeed)
	sum := h.Sum(nil)
	var n [12]byte
	copy(n[:], sum[:12])
	return n
}

// SealAEAD encrypts plaintext with ChaCha20-Poly1305 (128-bit authentication tag).
func SealAEAD(key [32]byte, nonceSeed, plaintext, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, err
	}
	n := deriveNonce12(nonceSeed)
	return aead.Seal(nil, n[:], plaintext, ad), nil
}

// OpenAEAD verifies and decrypts ChaCha20-Poly1305 ciphertext.
func OpenAEAD(key [32]byte, nonceSeed, ciphertext, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, err
	}
	n := deriveNonce12(nonceSeed)
	pt, err := aead.Open(nil, n[:], ciphertext, ad)
	if err != nil {
		return nil, ErrAEADAuth
	}
	return pt, nil
}

// SealMicroPayload encrypts and authenticates a 1-SSID micro message payload.
func (ck *ChannelKeys) SealMicroPayload(hdr7 []byte, plaintext []byte) (innerMAC [4]byte, ct []byte, err error) {
	if len(hdr7) != 7 || len(plaintext) > 32 {
		return innerMAC, nil, errors.New("crypto: invalid micro parameters")
	}
	enc, _ := blake2s.New256(ck.microEncKey[:])
	enc.Write([]byte("micro-stream-v1"))
	enc.Write(hdr7[1:7]) // senderShort2 || nonce4
	stream := enc.Sum(nil)

	ct = make([]byte, len(plaintext))
	for i := range plaintext {
		ct[i] = plaintext[i] ^ stream[i]
	}

	mac, _ := blake2s.New256(ck.microMACKey[:])
	mac.Write([]byte("micro-mac-v1"))
	mac.Write(hdr7)
	mac.Write(ct)
	copy(innerMAC[:], mac.Sum(nil)[:4])
	return innerMAC, ct, nil
}

// OpenMicroPayload verifies the inner 32-bit MAC and decrypts a 1-SSID micro message.
func (ck *ChannelKeys) OpenMicroPayload(hdr7 []byte, innerMAC [4]byte, ct []byte) ([]byte, error) {
	if len(hdr7) != 7 || len(ct) > 32 {
		return nil, ErrMicroAuth
	}
	mac, _ := blake2s.New256(ck.microMACKey[:])
	mac.Write([]byte("micro-mac-v1"))
	mac.Write(hdr7)
	mac.Write(ct)
	want := mac.Sum(nil)[:4]
	if subtle.ConstantTimeCompare(innerMAC[:], want) != 1 {
		return nil, ErrMicroAuth
	}

	enc, _ := blake2s.New256(ck.microEncKey[:])
	enc.Write([]byte("micro-stream-v1"))
	enc.Write(hdr7[1:7])
	stream := enc.Sum(nil)

	pt := make([]byte, len(ct))
	for i := range ct {
		pt[i] = ct[i] ^ stream[i]
	}
	return pt, nil
}
