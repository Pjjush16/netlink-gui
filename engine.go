// engine.go - Core networking engine with P2P + relay dual transport
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

type PeerConnection struct {
	Info      NodeInfo
	Direct    bool
	Addr      net.Addr
	LastSeen  time.Time
	BytesSent uint64
	BytesRecv uint64

	// For relay: active connections waiting for data
	relayConns   []net.Conn
	relayConnsMu sync.Mutex
}

func (pc *PeerConnection) AddRelayConn(conn net.Conn) {
	pc.relayConnsMu.Lock()
	pc.relayConns = append(pc.relayConns, conn)
	pc.relayConnsMu.Unlock()
}

func (pc *PeerConnection) DeliverRelayData(data []byte) {
	pc.relayConnsMu.Lock()
	defer pc.relayConnsMu.Unlock()

	for i := len(pc.relayConns) - 1; i >= 0; i-- {
		conn := pc.relayConns[i]
		_, err := conn.Write(data)
		if err != nil {
			// Remove dead connection
			pc.relayConns = append(pc.relayConns[:i], pc.relayConns[i+1:]...)
			conn.Close()
		}
	}
}

type Engine struct {
	Config     *EngineConfig
	State      EngineState
	Router     *Router
	Transport  Transport
	KeyPair    *KeyPair
	Signal     *SignalClient
	Proxy      *SOCKS5Proxy
	PublicAddr *PublicAddress
	LocalAddr  string
	VirtualIP  net.IP
	Peers      []NodeInfo
	PeerConns  map[string]*PeerConnection
	BytesSent  uint64
	BytesRecv  uint64
	Latency    time.Duration

	// UDP listener for P2P (separate from main transport if main is TCP)
	p2pListener *net.UDPConn
	p2pAddr     string

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	onUpdate func()
	logFunc  func(string)
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

func (e *Engine) SetLogFunc(fn func(string)) {
	e.logFunc = fn
}

func (e *Engine) notify() {
	if e.onUpdate != nil {
		e.onUpdate()
	}
}

func (e *Engine) log(msg string) {
	if e.logFunc != nil {
		e.logFunc(msg)
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

	// ===== ALWAYS start a UDP listener for P2P =====
	// Even if signal server uses HTTP, P2P data goes over UDP
	p2pAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		e.setError(err)
		return err
	}
	e.p2pListener, err = net.ListenUDP("udp", p2pAddr)
	if err != nil {
		e.log(fmt.Sprintf("P2P UDP listener failed: %v", err))
	} else {
		e.p2pAddr = e.p2pListener.LocalAddr().String()
		e.log(fmt.Sprintf("P2P UDP listener: %s", e.p2pAddr))
		go e.p2pReceiveLoop()
	}

	// Start main transport (TCP for signal, or UDP)
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
		e.log(fmt.Sprintf("STUN public addr: %s", pub))
	} else {
		e.log(fmt.Sprintf("STUN failed: %v", stunErr))
	}

	// Also try STUN on P2P listener
	if e.p2pAddr != "" {
		p2pPub, err := DiscoverSTUN(e.p2pAddr, 5*time.Second)
		if err == nil {
			e.log(fmt.Sprintf("P2P STUN public addr: %s", p2pPub))
		}
	}

	// Register with signal server
	if e.Config.SignalURL != "" {
		e.Signal = NewSignalClient(e.Config.SignalURL, e.Config.NodeID)

		// Register with our P2P UDP address so peers can reach us directly
		addr := e.LocalAddr
		if e.PublicAddr != nil {
			addr = e.PublicAddr.String()
		}

		assignedVIP, regErr := e.Signal.Register(addr, t.Mode())
		if regErr != nil {
			e.log(fmt.Sprintf("Signal register failed: %v", regErr))
			if e.Config.VirtualIP == "" {
				ip, allocErr := e.Router.AllocateVIP()
				if allocErr != nil {
					e.setError(allocErr)
					return allocErr
				}
				e.Config.VirtualIP = ip.String()
				e.Config.Save()
			}
		} else {
			e.Config.VirtualIP = assignedVIP
			e.Config.Save()
			e.log(fmt.Sprintf("Registered with VIP: %s", assignedVIP))
		}

		go e.heartbeatLoop()
		go e.peerDiscoveryLoop()
		go e.relayReceiveLoop()
	} else {
		if e.Config.VirtualIP == "" {
			ip, allocErr := e.Router.AllocateVIP()
			if allocErr != nil {
				e.setError(allocErr)
				return allocErr
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
	e.log(fmt.Sprintf("SOCKS5 proxy: %s", e.Proxy.Addr()))

	// Main transport packet handler
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

	// Try P2P direct first
	if exists && pc.Direct && pc.Addr != nil {
		e.log(fmt.Sprintf("P2P direct -> %s via %s", vipStr, pc.Addr))
		conn, err := net.DialTimeout("udp", pc.Addr.String(), 5*time.Second)
		if err == nil {
			return conn, nil
		}
		e.log(fmt.Sprintf("P2P direct failed: %v, falling back to relay", err))
	}

	// Relay fallback
	e.log(fmt.Sprintf("Relay -> %s", vipStr))

	serverConn, clientConn := net.Pipe()

	// Register this connection so relay data can be delivered
	e.mu.Lock()
	if !exists {
		pc = &PeerConnection{
			Info: NodeInfo{VirtualIP: vipStr},
		}
		e.PeerConns[vipStr] = pc
	}
	e.mu.Unlock()

	pc.AddRelayConn(serverConn)

	// Start reading from clientConn and sending via relay
	go func() {
		defer clientConn.Close()
		buf := make([]byte, 16384)
		for {
			select {
			case <-e.ctx.Done():
				return
			default:
			}
			clientConn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			if e.Signal != nil && pc.Info.NodeID != "" {
				encoded := base64.StdEncoding.EncodeToString(buf[:n])
				e.Signal.Relay(pc.Info.NodeID, encoded)
				e.mu.Lock()
				e.BytesSent += uint64(n)
				e.mu.Unlock()
			}
		}
	}()

	return clientConn, nil
}

// ===== P2P UDP Listener =====

func (e *Engine) p2pReceiveLoop() {
	buf := make([]byte, 65536)
	for {
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		e.p2pListener.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, remoteAddr, err := e.p2pListener.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		e.mu.Lock()
		e.BytesRecv += uint64(n)
		e.mu.Unlock()

		// Parse packet
		pkt, err := DecodePacket(data)
		if err != nil {
			continue
		}

		e.handleP2PPacket(pkt, remoteAddr)
	}
}

func (e *Engine) handleP2PPacket(pkt *Packet, remoteAddr *net.UDPAddr) {
	switch pkt.Header.Type {
	case MsgHandshake:
		// Peer wants P2P connection - respond with ack
		vipStr := pkt.Header.SrcVIP.To4().String()
		e.log(fmt.Sprintf("P2P handshake from %s at %s", vipStr, remoteAddr))

		e.mu.Lock()
		pc, exists := e.PeerConns[vipStr]
		if !exists {
			pc = &PeerConnection{}
			e.PeerConns[vipStr] = pc
		}
		pc.Direct = true
		pc.Addr = remoteAddr
		pc.LastSeen = time.Now()
		e.mu.Unlock()

		// Send ack back
		ack := &Packet{
			Header: Header{
				Type:   MsgHandshakeAck,
				Seq:    pkt.Header.Seq,
				SrcVIP: e.VirtualIP,
				DstVIP: pkt.Header.SrcVIP,
			},
		}
		e.p2pListener.WriteToUDP(ack.Encode(), remoteAddr)
		e.notify()

	case MsgHandshakeAck:
		vipStr := pkt.Header.SrcVIP.To4().String()
		e.log(fmt.Sprintf("P2P ack from %s", vipStr))

		e.mu.Lock()
		if pc, exists := e.PeerConns[vipStr]; exists {
			pc.Direct = true
			pc.LastSeen = time.Now()
		}
		e.mu.Unlock()
		e.notify()

	case MsgPing:
		pong := &Packet{
			Header: Header{
				Type:   MsgPong,
				Seq:    pkt.Header.Seq,
				SrcVIP: e.VirtualIP,
				DstVIP: pkt.Header.SrcVIP,
			},
		}
		e.p2pListener.WriteToUDP(pong.Encode(), remoteAddr)

	case MsgPong:
		e.mu.Lock()
		e.Latency = time.Since(time.Unix(0, int64(pkt.Header.Seq)))
		e.mu.Unlock()
	}
}

// sendP2PHandshake sends handshake packets to a peer for hole punching
func (e *Engine) sendP2PHandshake(peer NodeInfo) {
	if e.p2pListener == nil {
		return
	}

	var addr *net.UDPAddr
	// Try public_addr first (STUN-discovered)
	if peer.PublicAddr != "" {
		addr, _ = net.ResolveUDPAddr("udp", peer.PublicAddr)
	}
	// Try real_addr (from HTTP headers)
	if addr == nil && peer.RealAddr != "" {
		addr, _ = net.ResolveUDPAddr("udp", peer.RealAddr)
	}
	if addr == nil {
		e.log(fmt.Sprintf("No address for peer %s", peer.NodeID))
		return
	}

	e.log(fmt.Sprintf("P2P punch -> %s (%s)", peer.NodeID, addr))

	handshake := &Packet{
		Header: Header{
			Type:   MsgHandshake,
			Seq:    uint64(time.Now().UnixNano()),
			SrcVIP: e.VirtualIP,
			DstVIP: net.ParseIP(peer.VirtualIP),
		},
	}
	data := handshake.Encode()

	// Send 5 times with short intervals for better NAT traversal
	for i := 0; i < 5; i++ {
		_, err := e.p2pListener.WriteToUDP(data, addr)
		if err != nil {
			e.log(fmt.Sprintf("P2P send error: %v", err))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ===== Relay =====

func (e *Engine) relayReceiveLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
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

				// Find the peer connection from the sender's node_id
				var targetPC *PeerConnection
				for _, pc := range e.PeerConns {
					if pc.Info.NodeID == msg.From {
						targetPC = pc
						break
					}
				}
				e.mu.Unlock()

				if targetPC != nil {
					targetPC.DeliverRelayData(data)
				}
			}
		}
	}
}

// ===== Direct transport packet handler =====

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
				e.handleIncomingPacket(pkt)
			}
		}
	}
}

func (e *Engine) handleIncomingPacket(pkt IncomingPacket) {
	p, err := DecodePacket(pkt.Data)
	if err != nil {
		return
	}
	e.handleP2PPacket(p, nil)
}

// ===== Loops =====

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
	if e.p2pListener != nil {
		e.p2pListener.Close()
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
	ticker := time.NewTicker(3 * time.Second)
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

			var filtered []NodeInfo
			for _, p := range peers {
				if p.NodeID != e.Config.NodeID && p.VirtualIP != e.Config.VirtualIP {
					filtered = append(filtered, p)

					// Try P2P to new peers
					e.mu.RLock()
					pc, exists := e.PeerConns[p.VirtualIP]
					e.mu.RUnlock()

					if !exists || !pc.Direct {
						go e.sendP2PHandshake(p)
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
	if e.p2pAddr != "" {
		status["P2P地址"] = e.p2pAddr
	}
	if e.PublicAddr != nil {
		status["公网地址"] = e.PublicAddr.String()
	}
	if e.Proxy != nil && e.Proxy.Addr() != nil {
		status["SOCKS5"] = e.Proxy.Addr().String()
	}

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

// suppress unused import
var _ = io.EOF
