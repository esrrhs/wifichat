package wifichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"wifichat/pkg/engine"
	"wifichat/pkg/ssid"
)

// SessionConfig holds the start-up parameters for a mobile chat session.
// All fields are optional; empty values use engine defaults.
type SessionConfig struct {
	Nick        string
	Key         string
	Prefix      string
	Decoy       string
	Stealth     bool
	RXOnly      bool
	DownloadDir string
}

// Session is the gomobile-facing handle around the WiFiChat engine.
type Session struct {
	mu       sync.Mutex
	host     Host
	listener EventListener
	eng      *engine.Engine
	cfg      SessionConfig
	started  bool
}

// NewSession wires a platform Host and UI EventListener into a WiFiChat engine.
func NewSession(host Host, cfg *SessionConfig, listener EventListener) (*Session, error) {
	if host == nil {
		return nil, errors.New("wifichat: Host is required")
	}
	if cfg == nil {
		cfg = &SessionConfig{}
	}
	s := &Session{
		host:     host,
		listener: listener,
		cfg:      *cfg,
	}
	if err := s.buildEngine(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) buildEngine() error {
	drv := &hostDriver{host: s.host, rxOnly: s.cfg.RXOnly}
	download := s.cfg.DownloadDir
	if download == "" {
		download = "/data/local/tmp/wifichat-downloads"
	}
	opts := engine.Options{
		Driver:      drv,
		Nick:        s.cfg.Nick,
		Passphrase:  s.cfg.Key,
		Encoding:    ssid.ModeASCII91,
		Prefix:      s.cfg.Prefix,
		DecoySSID:   s.cfg.Decoy,
		Stealth:     s.cfg.Stealth,
		RXOnly:      s.cfg.RXOnly,
		DownloadDir: download,
		OnChat: func(m engine.ChatMessage) {
			if s.listener == nil {
				return
			}
			s.listener.OnChat(m.Nick, m.SenderID.Hex(), m.Text, m.Direct, m.Micro, m.Time.UnixMilli())
		},
		OnFile: func(rf engine.ReceivedFile) {
			if s.listener == nil {
				return
			}
			s.listener.OnFile(rf.Nick, rf.SenderID.Hex(), rf.FileName, rf.SavedPath, rf.SHA256Hex, rf.Size, rf.Direct)
		},
		OnPeer: func(p engine.PeerInfo) {
			if s.listener == nil {
				return
			}
			s.listener.OnPeer(p.Nick, p.PeerID.Hex(), p.SAS, p.Signal)
		},
		OnProgress: func(p engine.Progress) {
			if s.listener == nil {
				return
			}
			s.listener.OnProgress(p.Direction, p.Label, p.DoneGens, p.TotalGens, p.Bytes, p.BytesPerSec, p.Complete)
		},
		OnEvent: func(msg string) {
			if s.listener == nil {
				return
			}
			s.listener.OnEvent(msg)
		},
	}
	eng, err := engine.New(opts)
	if err != nil {
		return err
	}
	s.eng = eng
	return nil
}

// Start begins scan/TX loops.
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("session already started")
	}
	if err := s.eng.Start(); err != nil {
		return err
	}
	s.started = true
	return nil
}

// Stop halts the engine and releases SoftAP resources.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	s.eng.Stop()
	s.started = false
}

// IdentityJSON returns {"nick":"...","peerId":"..."}.
func (s *Session) IdentityJSON() string {
	pub := s.eng.PublicIdentity()
	b, _ := json.Marshal(map[string]string{
		"nick":   pub.Nick,
		"peerId": pub.PeerID.Hex(),
	})
	return string(b)
}

// StatusJSON returns a compact status snapshot for the UI.
func (s *Session) StatusJSON() string {
	tx, rx := s.eng.FrameCounters()
	pub := s.eng.PublicIdentity()
	payload := map[string]any{
		"nick":       pub.Nick,
		"peerId":     pub.PeerID.Hex(),
		"driver":     s.eng.DriverName(),
		"rxOnly":     s.cfg.RXOnly,
		"stealth":    s.cfg.Stealth,
		"open":       s.eng.IsOpenChannel(),
		"peers":      len(s.eng.ListPeers()),
		"txFrames":   tx,
		"rxFrames":   rx,
		"ssids":      s.eng.CurrentSSIDs(),
		"codec":      string(s.eng.Codec().Mode()),
		"rawPerSsid": s.eng.Codec().RawBytes(),
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// ListPeersJSON returns discovered nearby peers.
func (s *Session) ListPeersJSON() string {
	peers := s.eng.ListPeers()
	type row struct {
		Nick   string `json:"nick"`
		PeerID string `json:"peerId"`
		SAS    string `json:"sas"`
		Signal int    `json:"signal"`
		Slots  uint8  `json:"slots"`
		AgeMs  int64  `json:"ageMs"`
	}
	out := make([]row, 0, len(peers))
	now := time.Now()
	for _, p := range peers {
		out = append(out, row{
			Nick:   p.Nick,
			PeerID: p.PeerID.Hex(),
			SAS:    p.SAS,
			Signal: p.Signal,
			Slots:  p.Slots,
			AgeMs:  now.Sub(p.LastSeen).Milliseconds(),
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// SendText broadcasts a room chat message (blocks until air TX finishes).
func (s *Session) SendText(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return s.eng.SendText(ctx, text)
}

// SendDirect sends an X25519 E2EE direct message to a peer nick/id prefix.
func (s *Session) SendDirect(peer, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := s.waitPeer(ctx, peer); err != nil {
		return err
	}
	return s.eng.SendDirectText(ctx, peer, text)
}

// SendFile broadcasts a binary file from an absolute path.
func (s *Session) SendFile(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return s.eng.SendFile(ctx, "", path)
}

// SendDirectFile sends a binary file to a specific peer.
func (s *Session) SendDirectFile(peer, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := s.waitPeer(ctx, peer); err != nil {
		return err
	}
	return s.eng.SendFile(ctx, peer, path)
}

// SetKey switches the room passphrase (Argon2id).
func (s *Session) SetKey(key string) {
	s.cfg.Key = key
	s.eng.SetPassphrase(key)
}

// SetStealth toggles idle decoy / quiet Hello mode.
func (s *Session) SetStealth(on bool) {
	s.cfg.Stealth = on
	s.eng.SetStealth(on)
}

// Rotate regenerates ephemeral identity; returns IdentityJSON.
func (s *Session) Rotate() (string, error) {
	if _, err := s.eng.RotateIdentity(); err != nil {
		return "", err
	}
	return s.IdentityJSON(), nil
}

func (s *Session) waitPeer(ctx context.Context, peer string) error {
	if peer == "" {
		return nil
	}
	_ = s.eng.QueueHello()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.eng.ResolvePeer(peer); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("peer %q not discovered on air yet", peer)
}
