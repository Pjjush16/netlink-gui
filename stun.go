// stun.go - STUN client for NAT traversal
package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const stunMagicCookie = 0x2112A442

type PublicAddress struct {
	IP   net.IP
	Port uint16
}

func (a PublicAddress) String() string {
	return fmt.Sprintf("%s:%d", a.IP, a.Port)
}

func DiscoverSTUN(localAddr string, timeout time.Duration) (*PublicAddress, error) {
	servers := []string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
		"stun.cloudflare.com:3478",
	}
	var lastErr error
	for _, server := range servers {
		addr, err := stunRequest(server, timeout)
		if err == nil {
			return addr, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all STUN servers failed: %v", lastErr)
}

func stunRequest(server string, timeout time.Duration) (*PublicAddress, error) {
	saddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", &net.UDPAddr{Port: 0}, saddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	var txnID [12]byte
	rand.Read(txnID[:])

	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(msg[2:4], 0)
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	copy(msg[8:20], txnID[:])

	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	data := buf[:n]
	if len(data) < 20 {
		return nil, fmt.Errorf("response too short")
	}

	msgLen := int(binary.BigEndian.Uint16(data[2:4]))
	offset := 20
	end := 20 + msgLen
	if end > len(data) {
		end = len(data)
	}

	for offset < end {
		if offset+4 > end {
			break
		}
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		if offset+attrLen > end {
			break
		}
		attr := data[offset : offset+attrLen]
		offset += attrLen
		offset = (offset + 3) & ^3

		switch attrType {
		case 0x0020: // XOR-MAPPED-ADDRESS
			if len(attr) >= 8 && attr[1] == 0x01 {
				xorPort := binary.BigEndian.Uint16(attr[2:4])
				port := xorPort ^ uint16(stunMagicCookie>>16)
				xorIP := binary.BigEndian.Uint32(attr[4:8])
				ipVal := xorIP ^ uint32(stunMagicCookie)
				ip := make(net.IP, 4)
				binary.BigEndian.PutUint32(ip, ipVal)
				return &PublicAddress{IP: ip, Port: port}, nil
			}
		case 0x0001: // MAPPED-ADDRESS
			if len(attr) >= 8 && attr[1] == 0x01 {
				port := binary.BigEndian.Uint16(attr[2:4])
				ip := net.IPv4(attr[4], attr[5], attr[6], attr[7])
				return &PublicAddress{IP: ip, Port: port}, nil
			}
		}
	}
	return nil, fmt.Errorf("no mapped address in response")
}
