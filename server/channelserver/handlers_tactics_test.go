package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

func TestHandleMsgMhfGetUdTacticsPoint(t *testing.T) {
	server := createMockServer()
	server.divaRepo.(*mockDivaRepo).interceptionPoints = map[string]int{
		"58079": 1100,
		"58080": 200,
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(p.data)
		_ = bf.ReadUint16()
		_ = bf.ReadUint32()
		if !bf.ReadBool() || bf.ReadUint8() != 0 {
			t.Fatal("expected successful buffered ack")
		}
		if size := bf.ReadUint16(); size != 6 {
			t.Fatalf("payload size = %d, want 6", size)
		}
		payload := bf.ReadBytes(6)
		if payload[0] != 0 || binary.BigEndian.Uint32(payload[1:5]) != 1300 || payload[5] != 0 {
			t.Fatalf("unexpected tactics point payload: %x", payload)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAddUdTacticsPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAddUdTacticsPoint{
		AckHandle:     12345,
		QuestID:       udTacticsQuestMin,
		TacticsPoints: 120,
	}

	handleMsgMhfAddUdTacticsPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(p.data)
		_ = bf.ReadUint16()
		_ = bf.ReadUint32()
		if !bf.ReadBool() || bf.ReadUint8() != 0 {
			t.Fatal("expected successful buffered ack")
		}
		if size := bf.ReadUint16(); size != 2 {
			t.Fatalf("payload size = %d, want 2", size)
		}
		if payload := bf.ReadBytes(2); payload[0] != 0 || payload[1] != 0 {
			t.Fatalf("unexpected add tactics payload: %x", payload)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRewardList(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRewardList{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRewardList(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		data := p.data[dataOffset:]
		// status + two (u16 count + one 11-byte fallback prize) lists +
		// one 13-byte Reclaimed Areas & Point reward.
		if len(data) != 1+2+11+2+11+2+13 {
			t.Fatalf("tactics reward payload size=%d, want 42", len(data))
		}
		if data[0] != 0 {
			t.Errorf("tactics reward status=%d, want success", data[0])
		}
		offset := 1
		for list := 0; list < 2; list++ {
			if got := binary.BigEndian.Uint16(data[offset : offset+2]); got != 1 {
				t.Fatalf("tactics reward list %d count=%d, want 1", list, got)
			}
			offset += 2
			entry := data[offset : offset+11]
			if binary.BigEndian.Uint32(entry[0:4]) != 1 || entry[4] != divaRewardItemType ||
				binary.BigEndian.Uint16(entry[5:7]) != divaRewardItemID ||
				binary.BigEndian.Uint16(entry[7:9]) != divaRewardQuantity || entry[9] != 1 {
				t.Errorf("tactics reward list %d entry=% X, unexpected field order", list, entry)
			}
			offset += 11
		}
		if got := binary.BigEndian.Uint16(data[offset : offset+2]); got != 1 {
			t.Fatalf("third tactics reward list count=%d, want 1", got)
		}
		offset += 2
		entry := data[offset : offset+13]
		if entry[0] != divaRewardItemType ||
			binary.BigEndian.Uint16(entry[1:3]) != divaRewardItemID ||
			binary.BigEndian.Uint16(entry[3:5]) != divaRewardQuantity ||
			binary.BigEndian.Uint32(entry[5:9]) != 1 ||
			binary.BigEndian.Uint32(entry[9:13]) != 1 {
			t.Errorf("third tactics reward entry=% X, unexpected field order", entry)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsFollower(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsFollower{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsFollower(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsBonusQuest(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsBonusQuest{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsBonusQuest(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsFirstQuestBonus(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsFirstQuestBonus(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRemainingPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRemainingPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRanking(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		characterGuildID: 10,
		interceptionGuildRankings: []DivaRankingEntry{
			{ID: 10, Name: "TestGuild", Score: 1840},
			{ID: 11, Name: "OtherGuild", Score: 920},
		},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRanking{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRanking(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		data := p.data[dataOffset:]
		if len(data) != 41+2*40 {
			t.Fatalf("tactics ranking payload size=%d, want 121", len(data))
		}
		if got := binary.BigEndian.Uint32(data[0:4]); got != 1 {
			t.Errorf("own guild rank=%d, want 1", got)
		}
		if got := binary.BigEndian.Uint32(data[4:8]); got != 1840 {
			t.Errorf("own guild score=%d, want 1840", got)
		}
		if got := string(bytes.TrimRight(data[8:40], "\x00")); got != "TestGuild" {
			t.Errorf("own guild name=%q, want TestGuild", got)
		}
		if data[40] != 2 {
			t.Fatalf("ranking count=%d, want 2", data[40])
		}
		if got := binary.BigEndian.Uint32(data[41:45]); got != 1 {
			t.Errorf("first row rank=%d, want 1", got)
		}
		if got := binary.BigEndian.Uint32(data[45:49]); got != 1840 {
			t.Errorf("first row score=%d, want 1840", got)
		}
		if got := string(bytes.TrimRight(data[49:81], "\x00")); got != "TestGuild" {
			t.Errorf("first row guild name=%q, want TestGuild", got)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfSetUdTacticsFollower(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("handleMsgMhfSetUdTacticsFollower panicked: %v", r)
		}
	}()

	handleMsgMhfSetUdTacticsFollower(session, nil)
}

// Tests consolidated from handlers_coverage3_test.go

func TestSimpleAckHandlers_TacticsGo(t *testing.T) {
	server := createMockServer()

	tests := []struct {
		name string
		fn   func(s *Session)
	}{
		{"handleMsgMhfAddUdTacticsPoint", func(s *Session) {
			handleMsgMhfAddUdTacticsPoint(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 1})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := createMockSession(1, server)
			tt.fn(session)
			select {
			case p := <-session.sendPackets:
				if len(p.data) == 0 {
					t.Errorf("%s: response should have data", tt.name)
				}
			default:
				t.Errorf("%s: no response queued", tt.name)
			}
		})
	}
}

func TestNonTrivialHandlers_TacticsGo(t *testing.T) {
	server := createMockServer()

	tests := []struct {
		name string
		fn   func(s *Session)
	}{
		{"handleMsgMhfGetUdTacticsPoint", func(s *Session) {
			handleMsgMhfGetUdTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRewardList", func(s *Session) {
			handleMsgMhfGetUdTacticsRewardList(s, &mhfpacket.MsgMhfGetUdTacticsRewardList{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsFollower", func(s *Session) {
			handleMsgMhfGetUdTacticsFollower(s, &mhfpacket.MsgMhfGetUdTacticsFollower{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsBonusQuest", func(s *Session) {
			handleMsgMhfGetUdTacticsBonusQuest(s, &mhfpacket.MsgMhfGetUdTacticsBonusQuest{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsFirstQuestBonus", func(s *Session) {
			handleMsgMhfGetUdTacticsFirstQuestBonus(s, &mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRemainingPoint", func(s *Session) {
			handleMsgMhfGetUdTacticsRemainingPoint(s, &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRanking", func(s *Session) {
			handleMsgMhfGetUdTacticsRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsLog", func(s *Session) {
			handleMsgMhfGetUdTacticsLog(s, &mhfpacket.MsgMhfGetUdTacticsLog{AckHandle: 1})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := createMockSession(1, server)
			tt.fn(session)
			select {
			case p := <-session.sendPackets:
				if len(p.data) == 0 {
					t.Errorf("%s: response should have data", tt.name)
				}
			default:
				t.Errorf("%s: no response queued", tt.name)
			}
		})
	}
}

func TestEmptyHandlers_MiscFiles_Tactics(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	tests := []struct {
		name string
		fn   func()
	}{
		{"handleMsgMhfSetUdTacticsFollower", func() { handleMsgMhfSetUdTacticsFollower(session, nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", tt.name, r)
				}
			}()
			tt.fn()
		})
	}
}
