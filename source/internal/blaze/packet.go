package blaze

import (
	"encoding/binary"
	"fmt"
	"io"
)

const HeaderSize = 12

const (
	TypeRequest  uint16 = 0x0000
	TypeResponse uint16 = 0x1000
	TypeNotify   uint16 = 0x2000
	TypeError    uint16 = 0x3000
)

type Packet struct {
	Component uint16
	Command   uint16
	Error     uint16
	QType     uint16
	ID        uint16
	Payload   []byte
}

func ReadPacket(r io.Reader) (*Packet, error) {
	h := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, err
	}
	low := uint32(binary.BigEndian.Uint16(h[0:2]))
	component := binary.BigEndian.Uint16(h[2:4])
	command := binary.BigEndian.Uint16(h[4:6])
	ecode := binary.BigEndian.Uint16(h[6:8])
	qtype := binary.BigEndian.Uint16(h[8:10])
	id := binary.BigEndian.Uint16(h[10:12])
	length := low
	if qtype&0x0010 != 0 {
		x := make([]byte, 2)
		if _, err := io.ReadFull(r, x); err != nil {
			return nil, err
		}
		length |= uint32(binary.BigEndian.Uint16(x)) << 16
	}
	if length > 16*1024*1024 {
		return nil, fmt.Errorf("packet too large: %d", length)
	}
	p := make([]byte, length)
	if _, err := io.ReadFull(r, p); err != nil {
		return nil, err
	}
	return &Packet{Component: component, Command: command, Error: ecode, QType: qtype, ID: id, Payload: p}, nil
}

func (p *Packet) Bytes() []byte {
	n := len(p.Payload)
	ext := n > 0xffff
	q := p.QType
	hs := HeaderSize
	if ext {
		q |= 0x0010
		hs += 2
	}
	out := make([]byte, hs+n)
	binary.BigEndian.PutUint16(out[0:2], uint16(n&0xffff))
	binary.BigEndian.PutUint16(out[2:4], p.Component)
	binary.BigEndian.PutUint16(out[4:6], p.Command)
	binary.BigEndian.PutUint16(out[6:8], p.Error)
	binary.BigEndian.PutUint16(out[8:10], q)
	binary.BigEndian.PutUint16(out[10:12], p.ID)
	off := 12
	if ext {
		binary.BigEndian.PutUint16(out[12:14], uint16(uint32(n)>>16))
		off = 14
	}
	copy(out[off:], p.Payload)
	return out
}
