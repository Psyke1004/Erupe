package mhfpacket

import (
	"errors"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfSetKiju represents the MSG_MHF_SET_KIJU
type MsgMhfSetKiju struct {
	AckHandle uint32
	Unk1      uint16
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfSetKiju) Opcode() network.PacketID {
	return network.MSG_MHF_SET_KIJU
}

// Parse parses the packet from binary
func (m *MsgMhfSetKiju) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	// The client sends a one-byte zero-based UI slot followed by one byte of
	// padding. Reading both bytes as a big-endian uint16 turns slot 2 (02 00)
	// into 512 and causes the server to reject a valid selection.
	m.Unk1 = uint16(bf.ReadUint8())
	bf.ReadUint8() // padding
	return nil
}

// Build builds a binary packet from the current data.
func (m *MsgMhfSetKiju) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
