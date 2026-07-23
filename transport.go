// transport.go - UDP and TCP transport layer
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type IncomingPacket struct {
	Addr net.Addr
	Data []byte
}

type Transport interface {
	Start(ctx context.Context) error
	Send(addr net.Addr, data []byte) error
	Recv() <-chan IncomingPacket
	Close() error
	LocalAddr() net.Addr
	Mode() string
}

// UDP Transport
type UDPTransport struct {
	conn     *net.UDPConn
	recvCh   chan IncomingPacket
	mu       sync.Mutex
	addr     string
}

func NewUDP(addr string) *UDPTransport {
	return &UDPTransport{recvCh: make(chan IncomingPacket, 256), addr: addr}
}

func (u *UDPTransport) Start(ctx context.Context) error {
	a, err := net.ResolveUDPAddr("udp", u.addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", a)
	if err != nil {
		return err
	}
	u.conn = conn
	go func() {
		buf := make([]byte, 65536)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case u.recvCh <- IncomingPacket{Addr: addr, Data: data}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (u *UDPTransport) Send(addr net.Addr, data []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn == nil {
		return fmt.Errorf("not started")
	}
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		udpAddr, _ = net.ResolveUDPAddr("udp", addr.String())
	}
	_, err := u.conn.WriteToUDP(data, udpAddr)
	return err
}

func (u *UDPTransport) Recv() <-chan IncomingPacket { return u.recvCh }
func (u *UDPTransport) Close() error {
	if u.conn != nil {
		return u.conn.Close()
	}
	return nil
}
func (u *UDPTransport) LocalAddr() net.Addr {
	if u.conn != nil {
		return u.conn.LocalAddr()
	}
	return nil
}
func (u *UDPTransport) Mode() string { return "udp" }

// TCP Transport
type TCPTransport struct {
	listener net.Listener
	conns    map[string]net.Conn
	connsMu  sync.RWMutex
	recvCh   chan IncomingPacket
	mu       sync.Mutex
	addr     string
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewTCP(addr string) *TCPTransport {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPTransport{
		conns:  make(map[string]net.Conn),
		recvCh: make(chan IncomingPacket, 256),
		addr:   addr,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (t *TCPTransport) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return err
	}
	t.listener = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-t.ctx.Done():
					return
				default:
					continue
				}
			}
			key := conn.RemoteAddr().String()
			t.connsMu.Lock()
			t.conns[key] = conn
			t.connsMu.Unlock()
			go t.readConn(key, conn)
		}
	}()
	return nil
}

func (t *TCPTransport) readConn(key string, conn net.Conn) {
	defer func() {
		conn.Close()
		t.connsMu.Lock()
		delete(t.conns, key)
		t.connsMu.Unlock()
	}()
	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(conn, lenBuf)
		if err != nil {
			return
		}
		msgLen := binary.BigEndian.Uint32(lenBuf)
		if msgLen > 65536 {
			return
		}
		msg := make([]byte, msgLen)
		_, err = io.ReadFull(conn, msg)
		if err != nil {
			return
		}
		addr, _ := net.ResolveTCPAddr("tcp", key)
		select {
		case t.recvCh <- IncomingPacket{Addr: addr, Data: msg}:
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *TCPTransport) Send(addr net.Addr, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := addr.String()
	t.connsMu.RLock()
	conn, ok := t.conns[key]
	t.connsMu.RUnlock()
	if !ok {
		var err error
		conn, err = net.DialTimeout("tcp", key, 5*time.Second)
		if err != nil {
			return err
		}
		t.connsMu.Lock()
		t.conns[key] = conn
		t.connsMu.Unlock()
		go t.readConn(key, conn)
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)
	_, err := conn.Write(frame)
	if err != nil {
		t.connsMu.Lock()
		delete(t.conns, key)
		t.connsMu.Unlock()
		conn.Close()
	}
	return err
}

func (t *TCPTransport) Recv() <-chan IncomingPacket { return t.recvCh }
func (t *TCPTransport) Close() error {
	t.cancel()
	if t.listener != nil {
		t.listener.Close()
	}
	t.connsMu.Lock()
	for _, c := range t.conns {
		c.Close()
	}
	t.connsMu.Unlock()
	return nil
}
func (t *TCPTransport) LocalAddr() net.Addr {
	if t.listener != nil {
		return t.listener.Addr()
	}
	return nil
}
func (t *TCPTransport) Mode() string { return "tcp" }
