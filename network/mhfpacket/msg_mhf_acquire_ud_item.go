package mhfpacket

import (
	"errors"

	"erupe-ce/common/byteframe"
	"erupe-ce/network"
	"erupe-ce/network/clientctx"
)

// MsgMhfAcquireUdItem represents the MSG_MHF_ACQUIRE_UD_ITEM
type MsgMhfAcquireUdItem struct {
	AckHandle uint32
	Unk0      uint8
	// from gal
	// daily = 0
	// personal = 1
	// personal rank = 2
	// guild rank = 3
	// gcp = 4
	// from cat
	// treasure achievement = 5
	// personal achievement = 6
	// guild achievement = 7
	RewardType  uint8
	ItemIDCount uint8
	ItemIDs     []uint32
}

// Opcode returns the ID associated with this packet type.
func (m *MsgMhfAcquireUdItem) Opcode() network.PacketID {
	return network.MSG_MHF_ACQUIRE_UD_ITEM
}

// Parse parses the packet from binary
func (m *MsgMhfAcquireUdItem) Parse(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	m.AckHandle = bf.ReadUint32()
	m.Unk0 = bf.ReadUint8()
	m.RewardType = bf.ReadUint8()
	m.ItemIDCount = bf.ReadUint8()
	m.ItemIDs = make([]uint32, 0, m.ItemIDCount)
	// Unk0 == 0 is selective claim and carries an explicit ID array. The
	// client's "claim all" form (Unk0 != 0) carries only the count.
	if m.Unk0 == 0 {
		for i := uint8(0); i < m.ItemIDCount; i++ {
			m.ItemIDs = append(m.ItemIDs, bf.ReadUint32())
		}
	}
	return nil
}

// Build builds a binary packet from the current data.
func (m *MsgMhfAcquireUdItem) Build(bf *byteframe.ByteFrame, ctx *clientctx.ClientContext) error {
	return errors.New("NOT IMPLEMENTED")
}
