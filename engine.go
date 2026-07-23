// engine.go - Core networking engine
package main

import (
	"context"
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
	NodeID      string `json:"node_id"`
	VirtualIP   string `json:"virtual_ip"`
	Mode        string `json:"mode"`
	SignalURL   string `json:"signal_url"`
	ListenAddr  string `json:"listen_addr"`
	ProxyAddr   string `json:"proxy_addr"`
	PrivateKey  string `json:"private_key"`
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

type Engine struct {
	Config    *EngineConfig
	State     EngineState
	Router    *Router
	Transport Transport
	KeyPair   *KeyPair
	Cipher    *Cipher
	Signal    *SignalClient
	Proxy     *SOCKS5Proxy
	PublicAddr *PublicAddress
	LocalAddr  string
	VirtualIP  net.IP
	Peers     []NodeInfo
	BytesSent uint64
	BytesRecv uint64
	Latency   time.Duration

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	onUpdate func()
}

func NewEngine(cfg *EngineConfig) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		Config: cfg,
		Router: NewRouter(),
		ctx:    ctx,
		cancel: cancel,
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

	// Allocate virtual IP
	if e.Config.VirtualIP == "" {
		ip, err := e.Router.AllocateVIP()
		if err != nil {
			e.setError(err)
			return err
		}
		e.Config.VirtualIP = ip.String()
		e.Config.Save()
	}
	e.VirtualIP = net.ParseIP(e.Config.VirtualIP)

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
	go func() {
		pub, err := DiscoverSTUN(e.LocalAddr, 5*time.Second)
		if err == nil {
			e.mu.Lock()
			e.PublicAddr = pub
			e.mu.Unlock()
			e.notify()
		}
	}()

	// Start SOCKS5 proxy
	e.Proxy = NewSOCKS5(e.Config.ProxyAddr, func(vip net.IP, port uint16) (net.Conn, error) {
		route, ok := e.Router.Lookup(vip)
		if !ok {
			return nil, fmt.Errorf("no route to %s", vip)
		}
		return net.DialTimeout("tcp", route.PeerAddr.String(), 10*time.Second)
	})
	e.Proxy.Start()

	// Signal client
	if e.Config.SignalURL != "" {
		e.Signal = NewSignalClient(e.Config.SignalURL, e.Config.NodeID)
		addr := e.LocalAddr
		if e.PublicAddr != nil {
			addr = e.PublicAddr.String()
		}
		e.Signal.Register(e.Config.VirtualIP, addr, t.Mode())

		go e.heartbeatLoop()
		go e.peerDiscoveryLoop()
	}

	// Packet handler
	go e.packetLoop()

	e.mu.Lock()
	e.State = StateConnected
	e.mu.Unlock()
	e.notify()
	return nil
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
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.Signal != nil {
				peers, err := e.Signal.List()
				if err == nil {
					e.mu.Lock()
					e.Peers = peers
					e.mu.Unlock()
					e.notify()
				}
			}
		}
	}
}

func (e *Engine) packetLoop() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case pkt := <-e.Transport.Recv():
			if pkt.Data != nil {
				e.mu.Lock()
				e.BytesRecv += uint64(len(pkt.Data))
				e.mu.Unlock()
			}
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
	status["在线节点"] = fmt.Sprintf("%d", e.Router.Count())
	status["流量"] = fmt.Sprintf("↑%d KB ↓%d KB", e.BytesSent/1024, e.BytesRecv/1024)
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
