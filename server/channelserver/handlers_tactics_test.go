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
	divaRepo := server.divaRepo.(*mockDivaRepo)
	divaRepo.events = []DivaEvent{{ID: 8, StartTime: 1}}
	divaRepo.interceptionPoints = map[string]int{
		"58079": 1100,
		"58080": 200,
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsPoint(session, pkt)
	if divaRepo.interceptionReadEventID != 8 {
		t.Fatalf("interception read event ID=%d, want 8", divaRepo.interceptionReadEventID)
	}

	select {
	case p := <-session.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(p.data)
		_ = bf.ReadUint16()
		_ = bf.ReadUint32()
		if !bf.ReadBool() || bf.ReadUint8() != 0 {
			t.Fatal("expected successful buffered ack")
		}
		if size := bf.ReadUint16(); size != 10 {
			t.Fatalf("payload size = %d, want 10", size)
		}
		payload := bf.ReadBytes(10)
		if payload[0] != 0 || binary.BigEndian.Uint32(payload[1:5]) != 1300 || payload[5] != 2 ||
			binary.BigEndian.Uint16(payload[6:8]) != 58079 || binary.BigEndian.Uint16(payload[8:10]) != 58080 {
			t.Fatalf("unexpected tactics point payload: %x", payload)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAddUdTacticsPoint(t *testing.T) {
	server := createMockServer()
	divaRepo := server.divaRepo.(*mockDivaRepo)
	divaRepo.events = []DivaEvent{{ID: 8, StartTime: 1}}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAddUdTacticsPoint{
		AckHandle:     12345,
		QuestID:       divaInterceptionQuestIDs[0],
		TacticsPoints: 120,
	}

	handleMsgMhfAddUdTacticsPoint(session, pkt)
	if divaRepo.interceptionAddEventID != 8 {
		t.Fatalf("interception add event ID=%d, want 8", divaRepo.interceptionAddEventID)
	}

	select {
	case p := <-session.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(p.data)
		_ = bf.ReadUint16()
		_ = bf.ReadUint32()
		if !bf.ReadBool() || bf.ReadUint8() != 0 {
			t.Fatal("expected successful buffered ack")
		}
		if size := bf.ReadUint16(); size != 4 {
			t.Fatalf("payload size = %d, want 4", size)
		}
		if payload := bf.ReadBytes(4); payload[0] != 0 || payload[1] != 1 ||
			binary.BigEndian.Uint16(payload[2:4]) != divaInterceptionQuestIDs[0] {
			t.Fatalf("unexpected add tactics payload: %x", payload)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestDivaTacticsCompletedQuestIDs(t *testing.T) {
	points := map[string]int{
		"58083":   1,
		"58043":   200,
		"58050":   0,
		"58073":   900,
		"invalid": 900,
		"65535":   100,
	}
	want := []uint16{58043, 58083}
	if got := divaTacticsCompletedQuestIDs(points); !bytes.Equal(uint16SliceBytes(got), uint16SliceBytes(want)) {
		t.Fatalf("completed tactics quest IDs=%v, want %v", got, want)
	}
}

func TestDivaTacticsPointTotal(t *testing.T) {
	if got := divaTacticsPointTotal(map[string]int{"58050": 1100, "58051": 200, "58052": -1}); got != 1300 {
		t.Fatalf("tactics point total=%d, want 1300", got)
	}
	maxInt := int(^uint(0) >> 1)
	if got := divaTacticsPointTotal(map[string]int{"1": maxInt, "2": maxInt, "3": maxInt}); got != ^uint32(0) {
		t.Fatalf("overflowing tactics point total=%d, want %d", got, ^uint32(0))
	}
}

func uint16SliceBytes(values []uint16) []byte {
	data := make([]byte, len(values)*2)
	for i, value := range values {
		binary.BigEndian.PutUint16(data[i*2:], value)
	}
	return data
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
	server.erupeConfig.DebugOptions.DivaOverride = -1
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsBonusQuest{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsBonusQuest(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		if data := p.data[dataOffset:]; !bytes.Equal(data, []byte{0}) {
			t.Fatalf("bonus quest payload=% X, want a valid empty list", data)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestWriteDivaTacticsBonusQuestList(t *testing.T) {
	bf := byteframe.NewByteFrame()
	writeDivaTacticsBonusQuestList(bf, []divaTacticsBonusQuestEntry{{
		questID:    58081,
		startEpoch: 0x01020304,
		endEpoch:   0x05060708,
		points:     1000,
	}})
	want := []byte{
		0x01,
		0xE2, 0xE1,
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x03, 0xE8,
	}
	if got := bf.Data(); !bytes.Equal(got, want) {
		t.Fatalf("bonus quest bytes=% X, want % X", got, want)
	}
}

func TestWriteDivaTacticsBonusQuestListCapsClientSlots(t *testing.T) {
	entries := make([]divaTacticsBonusQuestEntry, divaTacticsBonusQuestLimit+8)
	bf := byteframe.NewByteFrame()
	writeDivaTacticsBonusQuestList(bf, entries)
	if got := bf.Data()[0]; got != divaTacticsBonusQuestLimit {
		t.Fatalf("bonus quest count=%d, want %d", got, divaTacticsBonusQuestLimit)
	}
	if got, want := len(bf.Data()), 1+divaTacticsBonusQuestLimit*12; got != want {
		t.Fatalf("bonus quest payload size=%d, want %d", got, want)
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
	repo := &mockDivaRepo{
		events:                    []DivaEvent{{ID: 8, StartTime: 1}},
		characterGuildID:          10,
		interceptionGuildMapScore: 1840,
	}
	server.divaRepo = repo
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRemainingPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		if got := binary.BigEndian.Uint32(p.data[dataOffset:]); got != 160 {
			t.Errorf("remaining interception points=%d, want 160", got)
		}
		if repo.interceptionMapEventID != 8 || repo.interceptionMapGuildID != 10 {
			t.Errorf("map score query=(event %d, guild %d), want (8, 10)", repo.interceptionMapEventID, repo.interceptionMapGuildID)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRemainingPointAfterRenderGridCapacity(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		events:                    []DivaEvent{{ID: 8, StartTime: 1}},
		characterGuildID:          10,
		interceptionGuildMapScore: 60180,
	}
	session := createMockSession(1, server)

	handleMsgMhfGetUdTacticsRemainingPoint(session, &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{AckHandle: 12345})

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		if got := binary.BigEndian.Uint32(p.data[dataOffset:]); got != 820 {
			t.Errorf("remaining interception points=%d, want 820", got)
		}
	default:
		t.Fatal("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRanking(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		events:           []DivaEvent{{ID: 8, StartTime: 1}},
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
	if got := server.divaRepo.(*mockDivaRepo).interceptionRankEventID; got != 8 {
		t.Fatalf("interception ranking event ID=%d, want 8", got)
	}

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
