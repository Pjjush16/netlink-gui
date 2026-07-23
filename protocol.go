// protocol.go - NetLink wire protocol
package main

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	MsgHandshake     byte = 0x01
	MsgHandshakeAck  byte = 0x02
	MsgPing          byte = 0x10
	MsgPong          byte = 0x11
	MsgRouteAnnounce byte = 0x20
	MsgData          byte = 0x30
	MsgDisconnect    byte = 0xFF
)

const HeaderSize = 21

type Header struct {
	Type   byte
	Length uint32
	Seq    uint64
	SrcVIP net.IP
	DstVIP net.IP
}

type Packet struct {
	Header  Header
	Payload []byte
}

func (p *Packet) Encode() []byte {
	p.Header.Length = uint32(len(p.Payload))
	buf := make([]byte, HeaderSize+len(p.Payload))
	buf[0] = p.Header.Type
	binary.BigEndian.PutUint32(buf[1:5], p.Header.Length)
	binary.BigEndian.PutUint64(buf[5:13], p.Header.Seq)
	s := p.Header.SrcVIP.To4()
	d := p.Header.DstVIP.To4()
	if s != nil {
		copy(buf[13:17], s)
	}
	if d != nil {
		copy(buf[17:21], d)
	}
	copy(buf[HeaderSize:], p.Payload)
	return buf
}

func DecodePacket(data []byte) (*Packet, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("packet too short: %d", len(data))
	}
	h := Header{
		Type:   data[0],
		Length: binary.BigEndian.Uint32(data[1:5]),
		Seq:    binary.BigEndian.Uint64(data[5:13]),
		SrcVIP: net.IPv4(data[13], data[14], data[15], data[16]),
		DstVIP: net.IPv4(data[17], data[18], data[19], data[20]),
	}
	total := HeaderSize + int(h.Length)
	if len(data) < total {
		return nil, fmt.Errorf("incomplete packet")
	}
	return &Packet{Header: h, Payload: data[HeaderSize:total]}, nil
}
