// engine.go - Core networking engine with P2P + relay dual transport
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

type EngineState int

const (
	StateDisconnected EngineState = iota
	StateConnecting
	StateConnected
	StateError
)

func (s EngineState) String() string {
	switch s {
	case StateDisconnected:
		return "未连接"
	case StateConnecting:
		return "连接中..."
	case StateConnected:
		return "已连接"
	case StateError:
		return "错误"
	default:
		return "未知"
	}
}

type EngineConfig struct {
	NodeID     string `json:"node_id"`
	VirtualIP  string `json:"virtual_ip"`
	Mode       string `json:"mode"`
	SignalURL  string `json:"signal_url"`
	ListenAddr string `json:"listen_addr"`
	ProxyAddr  string `json:"proxy_addr"`
	PrivateKey string `json:"private_key"`
}

func DefaultConfig() *EngineConfig {
	return &EngineConfig{
		Mode:       "auto",
		ListenAddr: "0.0.0.0:0",
		ProxyAddr:  "127.0.0.1:1080",
	}
}

func ConfigPath() string {
	u, _ := user.Current()
	return filepath.Join(u.HomeDir, ".netlink", "config.json")
}

func LoadConfig() (*EngineConfig, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), nil
	}
	cfg := DefaultConfig()
	json.Unmarshal(data, cfg)
	return cfg, nil
}

func (c *EngineConfig) Save() error {
	path := ConfigPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, data, 0644)
}

// PeerConnection tracks a peer's connection state
type PeerConnection struct {
	Info      NodeInfo
	Direct    bool      // true = P2P direct, false = relay only
	Addr      net.Addr  // resolved peer address for P2P
	LastSeen  time.Time
	BytesSent uint64
	BytesRecv uint64
}

type Engine struct {
	Config     *EngineConfig
	State      EngineState
	Router     *Router
	Transport  Transport
	KeyPair    *KeyPair
	Cipher     *Cipher
	Signal     *SignalClient
	Proxy      *SOCKS5Proxy
	PublicAddr *PublicAddress
	LocalAddr  string
	VirtualIP  net.IP
	Peers      []NodeInfo
	PeerConns  map[string]*PeerConnection // VIP -> connection
	BytesSent  uint64
	BytesRecv  uint64
	Latency    time.Duration

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	onUpdate func()
}

func NewEngine(cfg *EngineConfig) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		Config:    cfg,
		Router:    NewRouter(),
		PeerConns: make(map[string]*PeerConnection),
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (e *Engine) SetOnUpdate(fn func()) {
	e.onUpdate = fn
}

func (e *Engine) notify() {
	if e.onUpdate != nil {
		e.onUpdate()
	}
}

func (e *Engine) Connect() error {
	e.mu.Lock()
	e.State = StateConnecting
	e.mu.Unlock()
	e.notify()

	// Load or generate key pair
	var err error
	if e.Config.PrivateKey != "" {
		e.KeyPair, err = LoadKeyPair(e.Config.PrivateKey)
	} else {
		e.KeyPair, err = GenerateKeyPair()
		if err == nil {
			e.Config.PrivateKey = fmt.Sprintf("%x", e.KeyPair.PrivateKey)
			e.Config.Save()
		}
	}
	if err != nil {
		e.setError(fmt.Errorf("密钥错误: %v", err))
		return err
	}

	// Start transport
	mode := e.Config.Mode
	var t Transport

	if mode == "tcp" {
		t = NewTCP(e.Config.ListenAddr)
	} else {
		t = NewUDP(e.Config.ListenAddr)
	}

	if err := t.Start(e.ctx); err != nil {
		if mode == "auto" {
			t = NewTCP(e.Config.ListenAddr)
			if err2 := t.Start(e.ctx); err2 != nil {
				e.setError(fmt.Errorf("传输层启动失败: %v", err2))
				return err2
			}
		} else {
			e.setError(err)
			return err
		}
	}
	e.Transport = t

	if addr := t.LocalAddr(); addr != nil {
		e.LocalAddr = addr.String()
	}

	// STUN discovery
	pub, stunErr := DiscoverSTUN(e.LocalAddr, 5*time.Second)
	if stunErr == nil {
		e.mu.Lock()
		e.PublicAddr = pub
		e.mu.Unlock()
	}

	// Register with signal server
	if e.Config.SignalURL != "" {
		e.Signal = NewSignalClient(e.Config.SignalURL, e.Config.NodeID)

		addr := e.LocalAddr
		if e.PublicAddr != nil {
			addr = e.PublicAddr.String()
		}

		assignedVIP, regErr := e.Signal.Register(addr, t.Mode())
		if regErr != nil {
			fmt.Printf("Warning: signal register failed: %v\n", regErr)
			if e.Config.VirtualIP == "" {
				ip, err := e.Router.AllocateVIP()
				if err != nil {
					e.setError(err)
					return err
				}
				e.Config.VirtualIP = ip.String()
				e.Config.Save()
			}
		} else {
			e.Config.VirtualIP = assignedVIP
			e.Config.Save()
		}

		go e.heartbeatLoop()
		go e.peerDiscoveryLoop()
		go e.relayReceiveLoop()
	} else {
		if e.Config.VirtualIP == "" {
			ip, err := e.Router.AllocateVIP()
			if err != nil {
				e.setError(err)
				return err
			}
			e.Config.VirtualIP = ip.String()
			e.Config.Save()
		}
	}

	e.VirtualIP = net.ParseIP(e.Config.VirtualIP)

	// Start SOCKS5 proxy
	e.Proxy = NewSOCKS5(e.Config.ProxyAddr, func(vip net.IP, port uint16) (net.Conn, error) {
		return e.dialVirtualIP(vip, port)
	})
	e.Proxy.Start()

	// Direct UDP packet handler
	go e.directPacketLoop()

	e.mu.Lock()
	e.State = StateConnected
	e.mu.Unlock()
	e.notify()
	return nil
}

// dialVirtualIP connects to a virtual IP - tries P2P first, falls back to relay
func (e *Engine) dialVirtualIP(vip net.IP, port uint16) (net.Conn, error) {
	vipStr := vip.To4().String()

	e.mu.RLock()
	pc, exists := e.PeerConns[vipStr]
	e.mu.RUnlock()

	if exists && pc.Direct && pc.Addr != nil {
		// P2P direct connection available
		conn, err := net.DialTimeout("tcp", pc.Addr.String(), 5*time.Second)
		if err == nil {
			return conn, nil
		}
	}

	// Fallback: use relay (virtual pipe)
	serverConn, clientConn := net.Pipe()

	go e.relayForwardLoop(vipStr, serverConn)

	return clientConn, nil
}

// relayForwardLoop reads from local connection and sends via relay
func (e *Engine) relayForwardLoop(targetVIP string, conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 16384)

	for {
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		if e.Signal == nil {
			return
		}

		// Find peer's node_id from VIP
		e.mu.RLock()
		pc, exists := e.PeerConns[targetVIP]
		e.mu.RUnlock()

		if !exists {
			return
		}

		encoded := base64.StdEncoding.EncodeToString(buf[:n])
		e.Signal.Relay(pc.Info.NodeID, encoded)
	}
}

// relayReceiveLoop polls signal server for relay messages
func (e *Engine) relayReceiveLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.Signal == nil {
				continue
			}

			messages, err := e.Signal.RelayPoll()
			if err != nil {
				continue
			}

			for _, msg := range messages {
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					continue
				}

				e.mu.Lock()
				e.BytesRecv += uint64(len(data))
				e.mu.Unlock()

				// TODO: deliver data to the appropriate local connection
				// For now, just track the stats
				_ = data
			}
		}
	}
}

// directPacketLoop handles packets received via direct UDP/TCP transport
func (e *Engine) directPacketLoop() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case pkt := <-e.Transport.Recv():
			if pkt.Data != nil {
				e.mu.Lock()
				e.BytesRecv += uint64(len(pkt.Data))
				e.mu.Unlock()

				// Try to parse and handle the packet
				e.handleIncomingPacket(pkt)
			}
		}
	}
}

// handleIncomingPacket processes a received packet
func (e *Engine) handleIncomingPacket(pkt IncomingPacket) {
	p, err := DecodePacket(pkt.Data)
	if err != nil {
		return
	}

	switch p.Header.Type {
	case MsgPing:
		// Reply with pong
		pong := &Packet{
			Header: Header{
				Type:   MsgPong,
				Seq:    p.Header.Seq,
				SrcVIP: e.VirtualIP,
				DstVIP: p.Header.SrcVIP,
			},
		}
		e.Transport.Send(pkt.Addr, pong.Encode())

	case MsgPong:
		// Update latency
		e.mu.Lock()
		e.Latency = time.Since(time.Unix(0, int64(p.Header.Seq)))
		e.mu.Unlock()

	case MsgHandshake:
		// Peer wants P2P connection
		e.mu.Lock()
		vipStr := p.Header.SrcVIP.To4().String()
		pc, exists := e.PeerConns[vipStr]
		if !exists {
			pc = &PeerConnection{}
			e.PeerConns[vipStr] = pc
		}
		pc.Direct = true
		pc.Addr = pkt.Addr
		pc.LastSeen = time.Now()
		e.mu.Unlock()

		// Send handshake ack
		ack := &Packet{
			Header: Header{
				Type:   MsgHandshakeAck,
				Seq:    p.Header.Seq,
				SrcVIP: e.VirtualIP,
				DstVIP: p.Header.SrcVIP,
			},
		}
		e.Transport.Send(pkt.Addr, ack.Encode())

	case MsgHandshakeAck:
		// Peer confirmed P2P
		e.mu.Lock()
		vipStr := p.Header.SrcVIP.To4().String()
		if pc, exists := e.PeerConns[vipStr]; exists {
			pc.Direct = true
			pc.LastSeen = time.Now()
		}
		e.mu.Unlock()
	}
}

// tryP2P attempts P2P hole punching to a peer
func (e *Engine) tryP2P(peer NodeInfo) {
	vipStr := peer.VirtualIP

	// Resolve peer address
	var addr net.Addr
	if peer.PublicAddr != "" {
		addr, _ = net.ResolveUDPAddr("udp", peer.PublicAddr)
	}
	if addr == nil && peer.RealAddr != "" {
		addr, _ = net.ResolveUDPAddr("udp", peer.RealAddr)
	}

	// Store peer connection (relay-only initially)
	e.mu.Lock()
	e.PeerConns[vipStr] = &PeerConnection{
		Info:     peer,
		Direct:   false,
		Addr:     addr,
		LastSeen: time.Now(),
	}
	e.mu.Unlock()

	if addr == nil || e.Transport == nil {
		return // Can't try P2P without address
	}

	// Send handshake packets to try hole punching
	handshake := &Packet{
		Header: Header{
			Type:   MsgHandshake,
			Seq:    uint64(time.Now().UnixNano()),
			SrcVIP: e.VirtualIP,
			DstVIP: net.ParseIP(vipStr),
		},
	}
	data := handshake.Encode()

	// Send multiple times to increase chance of NAT traversal
	for i := 0; i < 3; i++ {
		e.Transport.Send(addr, data)
		time.Sleep(200 * time.Millisecond)
	}
}

func (e *Engine) Disconnect() {
	e.cancel()
	if e.Signal != nil {
		e.Signal.Deregister()
	}
	if e.Proxy != nil {
		e.Proxy.Stop()
	}
	if e.Transport != nil {
		e.Transport.Close()
	}
	e.mu.Lock()
	e.State = StateDisconnected
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) setError(err error) {
	e.mu.Lock()
	e.State = StateError
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.Signal != nil {
				e.Signal.Heartbeat()
			}
		}
	}
}

func (e *Engine) peerDiscoveryLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.Signal == nil {
				continue
			}
			peers, err := e.Signal.List()
			if err != nil {
				continue
			}

			// Filter out self
			var filtered []NodeInfo
			for _, p := range peers {
				if p.NodeID != e.Config.NodeID && p.VirtualIP != e.Config.VirtualIP {
					filtered = append(filtered, p)

					// Try P2P to new peers
					e.mu.RLock()
					_, exists := e.PeerConns[p.VirtualIP]
					e.mu.RUnlock()

					if !exists {
						go e.tryP2P(p)
					}
				}
			}

			e.mu.Lock()
			e.Peers = filtered
			e.mu.Unlock()
			e.notify()
		}
	}
}

func (e *Engine) GetStatus() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	status := map[string]string{
		"状态":     e.State.String(),
		"节点ID":   e.Config.NodeID,
		"虚拟IP":   e.Config.VirtualIP,
		"传输模式": e.Transport_mode(),
		"本地地址": e.LocalAddr,
	}
	if e.PublicAddr != nil {
		status["公网地址"] = e.PublicAddr.String()
	}
	if e.Proxy != nil && e.Proxy.Addr() != nil {
		status["SOCKS5"] = e.Proxy.Addr().String()
	}

	// Count direct vs relay peers
	direct := 0
	relay := 0
	for _, pc := range e.PeerConns {
		if pc.Direct {
			direct++
		} else {
			relay++
		}
	}
	status["在线节点"] = fmt.Sprintf("%d (直连:%d 中继:%d)", len(e.Peers), direct, relay)
	status["流量"] = fmt.Sprintf("↑%d KB ↓%d KB", e.BytesSent/1024, e.BytesRecv/1024)

	if e.Latency > 0 {
		status["延迟"] = fmt.Sprintf("%.1f ms", float64(e.Latency.Microseconds())/1000.0)
	}

	return status
}

func (e *Engine) Transport_mode() string {
	if e.Transport != nil {
		return e.Transport.Mode()
	}
	return "none"
}

func Hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "node"
	}
	return h
}
