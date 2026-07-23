// proxy.go - SOCKS5 proxy server
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

type ForwardFunc func(net.IP, uint16) (net.Conn, error)

type SOCKS5Proxy struct {
	listener net.Listener
	forward  ForwardFunc
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	addr     string
}

func NewSOCKS5(addr string, forward ForwardFunc) *SOCKS5Proxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &SOCKS5Proxy{forward: forward, ctx: ctx, cancel: cancel, addr: addr}
}

func (s *SOCKS5Proxy) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
					continue
				}
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handle(conn)
			}()
		}
	}()
	return nil
}

func (s *SOCKS5Proxy) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

func (s *SOCKS5Proxy) Stop() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
}

func (s *SOCKS5Proxy) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 262)
	n, err := conn.Read(buf)
	if err != nil || n < 2 || buf[0] != 0x05 {
		return
	}
	conn.Write([]byte{0x05, 0x00})

	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[1] != 0x01 {
		return
	}

	var targetIP net.IP
	var targetPort uint16

	switch buf[3] {
	case 0x01:
		targetIP = net.IPv4(buf[4], buf[5], buf[6], buf[7])
		targetPort = binary.BigEndian.Uint16(buf[8:10])
	case 0x03:
		domLen := int(buf[4])
		targetPort = binary.BigEndian.Uint16(buf[5+domLen : 7+domLen])
		ips, err := net.LookupIP(string(buf[5 : 5+domLen]))
		if err != nil || len(ips) == 0 {
			conn.Write([]byte{0x05, 0x04, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		targetIP = ips[0]
	default:
		conn.Write([]byte{0x05, 0x08, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}

	isVIP := targetIP.To4() != nil && targetIP.To4()[0] == 10 && targetIP.To4()[1] == 0 && targetIP.To4()[2] == 0

	var remote net.Conn
	if isVIP {
		remote, err = s.forward(targetIP, targetPort)
	} else {
		remote, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetIP, targetPort), 10*time.Second)
	}
	if err != nil {
		conn.Write([]byte{0x05, 0x05, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()

	conn.Write([]byte{0x05, 0x00, 0, 1, 0, 0, 0, 0, 0, 0})

	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, remote); done <- struct{}{} }()
	<-done
}
