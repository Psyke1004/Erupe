package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"time"
)

func TestHandleMsgMhfGetUdInfo(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdInfo{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdInfo(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetKijuInfo(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetKijuInfo{
		AckHandle: 12345,
	}

	handleMsgMhfGetKijuInfo(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestGetDivaBeadTypesCapsClientListAtFour(t *testing.T) {
	repo := &mockDivaRepo{beadTypes: []int{1, 2, 3, 4, 5}}
	got := getDivaBeadTypes(repo)
	if len(got) != maxDivaBeadSlots {
		t.Fatalf("len(getDivaBeadTypes)=%d, want %d", len(got), maxDivaBeadSlots)
	}
}

func TestHandleMsgMhfSetKiju(t *testing.T) {
	server := createMockServer()
	repo := &mockDivaRepo{}
	server.divaRepo = repo
	session := createMockSession(1, server)
	session.currentBeadIndex = -1

	pkt := &mhfpacket.MsgMhfSetKiju{
		AckHandle: 12345,
		Unk1:      4,
	}

	handleMsgMhfSetKiju(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
	if session.currentBeadIndex != 4 || repo.assignedBead != 4 || !repo.hasAssignedBead {
		t.Fatalf("valid selection was not persisted and activated")
	}
}

func TestNextDivaSelectionExpiryUsesJSTNoon(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before noon",
			now:  time.Date(2026, time.August, 6, 8, 30, 0, 0, jst),
			want: time.Date(2026, time.August, 6, 12, 0, 0, 0, jst),
		},
		{
			name: "after noon",
			now:  time.Date(2026, time.August, 6, 19, 0, 0, 0, jst),
			want: time.Date(2026, time.August, 7, 12, 0, 0, 0, jst),
		},
	}
	for _, tt := range tests {
		if got := nextDivaSelectionExpiry(tt.now); !got.Equal(tt.want) {
			t.Errorf("%s: expiry=%s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestHandleMsgMhfSetKijuRejectsOutOfRangeSelection(t *testing.T) {
	server := createMockServer()
	repo := &mockDivaRepo{}
	server.divaRepo = repo
	session := createMockSession(1, server)
	session.currentBeadIndex = -1

	pkt := &mhfpacket.MsgMhfSetKiju{AckHandle: 12345, Unk1: 0}
	handleMsgMhfSetKiju(session, pkt)
	<-session.sendPackets

	if session.currentBeadIndex != -1 || repo.hasAssignedBead {
		t.Fatalf("invalid selection was persisted or activated")
	}
}

func TestRestoreDivaBeadSelection(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		assignedBead:    3,
		hasAssignedBead: true,
	}
	session := createMockSession(1, server)
	session.currentBeadIndex = -1

	restoreDivaBeadSelection(session)
	if session.currentBeadIndex != 3 {
		t.Fatalf("currentBeadIndex=%d, want restored value 3", session.currentBeadIndex)
	}
}

func TestRestoreDivaBeadSelectionLeavesNoSelectionAtMinusOne(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{}
	session := createMockSession(1, server)
	session.currentBeadIndex = 2

	restoreDivaBeadSelection(session)
	if session.currentBeadIndex != -1 {
		t.Fatalf("currentBeadIndex=%d, want -1 without active assignment", session.currentBeadIndex)
	}
}

func TestHandleMsgMhfAddUdPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAddUdPoint{
		AckHandle: 12345,
	}

	handleMsgMhfAddUdPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		const ackBufDataOffset = 10
		if got := len(p.data) - ackBufDataOffset; got != 1 {
			t.Errorf("AddUdPoint ACK payload size=%d, want 1", got)
		}
		if got := p.data[ackBufDataOffset]; got != 0 {
			t.Errorf("AddUdPoint ACK status=%d, want 0", got)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAddUdPoint_AccumulatesPoints(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{
		events: []DivaEvent{{ID: 42, StartTime: uint32(time.Now().Unix())}},
	}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100
	s.currentBeadIndex = 2

	// First quest: 500 quest points + 50 bonus
	pkt := &mhfpacket.MsgMhfAddUdPoint{AckHandle: 1, QuestPoints: 500, BonusPoints: 50}
	handleMsgMhfAddUdPoint(s, pkt)
	<-s.sendPackets

	qp, bp, err := repo.GetPoints(100, 42)
	if err != nil {
		t.Fatal(err)
	}
	if qp != 500 || bp != 50 {
		t.Errorf("After first quest: quest=%d bonus=%d, want 500/50", qp, bp)
	}
	if repo.beadPointCharacterID != 100 || repo.beadPointEventID != 42 || repo.beadPointIndex != 2 || repo.beadPointValue != 550 {
		t.Errorf("bead point contribution=(char %d, event %d, bead %d, points %d), want (100, 42, 2, 550)",
			repo.beadPointCharacterID, repo.beadPointEventID, repo.beadPointIndex, repo.beadPointValue)
	}

	// Second quest: 300 quest points + 30 bonus
	pkt2 := &mhfpacket.MsgMhfAddUdPoint{AckHandle: 2, QuestPoints: 300, BonusPoints: 30}
	handleMsgMhfAddUdPoint(s, pkt2)
	<-s.sendPackets

	qp, bp, err = repo.GetPoints(100, 42)
	if err != nil {
		t.Fatal(err)
	}
	if qp != 800 || bp != 80 {
		t.Errorf("After second quest: quest=%d bonus=%d, want 800/80", qp, bp)
	}
}

func TestHandleMsgMhfAddUdPoint_RestoresPersistentBeadBeforeSaving(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{
		events:          []DivaEvent{{ID: 42, StartTime: uint32(time.Now().Unix())}},
		assignedBead:    3,
		hasAssignedBead: true,
	}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100
	s.currentBeadIndex = -1

	handleMsgMhfAddUdPoint(s, &mhfpacket.MsgMhfAddUdPoint{
		AckHandle: 77, QuestPoints: 500, BonusPoints: 132,
	})
	<-s.sendPackets

	if s.currentBeadIndex != 3 {
		t.Fatalf("currentBeadIndex=%d, want restored bead 3", s.currentBeadIndex)
	}
	if repo.beadPointIndex != 3 || repo.beadPointValue != 632 {
		t.Fatalf("saved bead=%d points=%d, want bead=3 points=632",
			repo.beadPointIndex, repo.beadPointValue)
	}
}

func TestHandleMsgMhfAddUdPoint_IgnoresSameAckRetransmission(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{events: []DivaEvent{{ID: 42, StartTime: uint32(time.Now().Unix())}}}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100
	s.currentBeadIndex = 2
	pkt := &mhfpacket.MsgMhfAddUdPoint{AckHandle: 77, QuestPoints: 204, BonusPoints: 120}

	handleMsgMhfAddUdPoint(s, pkt)
	<-s.sendPackets
	handleMsgMhfAddUdPoint(s, pkt)
	<-s.sendPackets

	qp, bp, err := repo.GetPoints(100, 42)
	if err != nil {
		t.Fatal(err)
	}
	if qp != 204 || bp != 120 {
		t.Fatalf("duplicate submission stored: quest=%d bonus=%d, want 204/120", qp, bp)
	}
}

func TestHandleMsgMhfAddUdPoint_NoEvent(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{} // no events
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100

	pkt := &mhfpacket.MsgMhfAddUdPoint{AckHandle: 1, QuestPoints: 500, BonusPoints: 50}
	handleMsgMhfAddUdPoint(s, pkt)
	<-s.sendPackets

	// Should still ACK successfully even with no event
	if len(repo.points) != 0 {
		t.Error("Should not store points when no event is active")
	}
}

func TestHandleMsgMhfAddUdPoint_ZeroPoints(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{
		events: []DivaEvent{{ID: 1, StartTime: uint32(time.Now().Unix())}},
	}
	srv.divaRepo = repo
	s := createMockSession(1, srv)
	s.charID = 100

	pkt := &mhfpacket.MsgMhfAddUdPoint{AckHandle: 1, QuestPoints: 0, BonusPoints: 0}
	handleMsgMhfAddUdPoint(s, pkt)
	<-s.sendPackets

	// Should not create a row for zero points
	if len(repo.points) != 0 {
		t.Error("Should not store zero points")
	}
}

func TestHandleMsgMhfGetUdMyPoint(t *testing.T) {
	eventStart := time.Now()
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		events: []DivaEvent{{ID: 42, StartTime: uint32(eventStart.Unix())}},
		beadPointEntries: []DivaBeadPointEntry{{
			BeadIndex: 2, QuestPoints: 204, BonusPoints: 120, Timestamp: eventStart.Add(time.Minute),
		}},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdMyPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdMyPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		const ackBufDataOffset = 10
		const payloadSize = 1 + 8*18
		if got := len(p.data) - ackBufDataOffset; got != payloadSize {
			t.Fatalf("GetUdMyPoint payload size=%d, want %d", got, payloadSize)
		}
		if status := p.data[ackBufDataOffset]; status != 0 {
			t.Errorf("GetUdMyPoint status=%d, want success (0)", status)
		}
		const firstEntry = ackBufDataOffset + 1
		if got := p.data[firstEntry]; got != 2 {
			t.Errorf("GetUdMyPoint first bead=%d, want 2", got)
		}
		if got := binary.BigEndian.Uint32(p.data[firstEntry+1 : firstEntry+5]); got != 204 {
			t.Errorf("GetUdMyPoint quest points=%d, want 204", got)
		}
		if got := binary.BigEndian.Uint32(p.data[firstEntry+5 : firstEntry+9]); got != 120 {
			t.Errorf("GetUdMyPoint bonus points=%d, want 120", got)
		}
		const secondDay = firstEntry + 18
		if got := p.data[secondDay]; got != 0 {
			t.Errorf("GetUdMyPoint day 2 bead=%d, want 0", got)
		}
		if got := binary.BigEndian.Uint32(p.data[secondDay+1 : secondDay+5]); got != 0 {
			t.Errorf("GetUdMyPoint repeated day 1 points into day 2: %d", got)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTotalPointInfo(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTotalPointInfo{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTotalPointInfo(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdSelectedColorInfo(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdSelectedColorInfo{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdSelectedColorInfo(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdMonsterPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdMonsterPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdMonsterPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		const ackBufDataOffset = 10
		// count u8, followed by 3-byte entries: monster ID u8, points u16.
		expectedPrefix := []byte{0x01, 0x00, 0x3C, 0x02, 0x00, 0x5A}
		if len(p.data) < ackBufDataOffset+1+len(expectedPrefix) {
			t.Fatalf("Monster point response too short: %d", len(p.data))
		}
		got := p.data[ackBufDataOffset+1 : ackBufDataOffset+1+len(expectedPrefix)]
		if !bytes.Equal(got, expectedPrefix) {
			t.Errorf("Unexpected monster point entry layout: got % X, want % X", got, expectedPrefix)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdDailyPresentList(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdDailyPresentList{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdDailyPresentList(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		data := p.data[dataOffset:]
		if len(data) != 2+7*15 {
			t.Fatalf("daily reward payload size=%d, want 107", len(data))
		}
		for i := 0; i < 7; i++ {
			entry := data[2+i*15 : 2+(i+1)*15]
			if entry[0] != 7 || binary.BigEndian.Uint16(entry[1:3]) != divaRewardItemID ||
				binary.BigEndian.Uint16(entry[3:5]) != divaRewardQuantity || entry[5] != 1 ||
				binary.BigEndian.Uint32(entry[6:10]) != 1 || binary.BigEndian.Uint32(entry[10:14]) != 999 ||
				entry[14] != uint8(i+1) {
				t.Errorf("daily reward entry %d=% X, want participation day %d and item %d x%d", i, entry, i+1, divaRewardItemID, divaRewardQuantity)
			}
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdNormaPresentList(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdNormaPresentList{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdNormaPresentList(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		data := p.data[dataOffset:]
		if len(data) != 2+19 {
			t.Fatalf("norma reward payload size=%d, want 21", len(data))
		}
		entry := data[2:]
		if entry[0] != 7 || binary.BigEndian.Uint16(entry[1:3]) != divaRewardItemID ||
			binary.BigEndian.Uint16(entry[3:5]) != divaRewardQuantity || entry[5] != 1 ||
			binary.BigEndian.Uint32(entry[6:10]) != 1 || binary.BigEndian.Uint32(entry[10:14]) != 999 ||
			binary.BigEndian.Uint32(entry[14:18]) != 1 {
			t.Errorf("norma reward entry=% X, unexpected client field order", entry)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdRankingRewardListUsesClientFieldOrder(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	handleMsgMhfGetUdRankingRewardList(session, &mhfpacket.MsgMhfGetUdRankingRewardList{AckHandle: 12345})

	const dataOffset = 10
	data := (<-session.sendPackets).data[dataOffset:]
	if len(data) != 2+2*14 {
		t.Fatalf("ranking reward payload size=%d, want 30", len(data))
	}
	if got := binary.BigEndian.Uint16(data[:2]); got != 2 {
		t.Fatalf("ranking reward count=%d, want 2", got)
	}
	for i, rankType := range []uint8{0, 1} {
		entry := data[2+i*14 : 2+(i+1)*14]
		if entry[0] != divaRewardItemType || binary.BigEndian.Uint16(entry[1:3]) != divaRewardItemID ||
			binary.BigEndian.Uint16(entry[3:5]) != divaRewardQuantity || entry[5] != rankType ||
			binary.BigEndian.Uint32(entry[6:10]) != 1 || binary.BigEndian.Uint32(entry[10:14]) != 100 {
			t.Errorf("ranking reward entry %d=% X, unexpected client field order", i, entry)
		}
	}
}

func TestHandleMsgMhfAcquireUdItem(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAcquireUdItem{
		AckHandle: 12345,
	}

	handleMsgMhfAcquireUdItem(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		if len(p.data) != dataOffset+2 {
			t.Fatalf("AcquireUdItem packet size=%d, want %d", len(p.data), dataOffset+2)
		}
		if got := p.data[dataOffset:]; !bytes.Equal(got, []byte{0x00, 0x00}) {
			t.Errorf("AcquireUdItem payload=% X, want 00 00", got)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAcquireUdItemDeliversUnifiedRewardOnce(t *testing.T) {
	server := createMockServer()
	repo := &mockDivaRepo{
		events: []DivaEvent{{ID: 8, StartTime: uint32(time.Now().Unix())}},
		points: map[[2]uint32][2]int64{{1, 8}: {100, 20}},
	}
	server.divaRepo = repo
	session := createMockSession(1, server)
	pkt := &mhfpacket.MsgMhfAcquireUdItem{
		AckHandle: 1, RewardType: 1, ItemIDCount: 1,
		ItemIDs: []uint32{uint32(divaRewardItemID)},
	}

	handleMsgMhfAcquireUdItem(session, pkt)
	const dataOffset = 10
	data := (<-session.sendPackets).data[dataOffset:]
	if len(data) != 11 || data[1] != 1 {
		t.Fatalf("first claim payload=% X, want one 9-byte result", data)
	}
	if got := binary.BigEndian.Uint32(data[2:6]); got != uint32(divaRewardItemID) {
		t.Errorf("reward item=%d, want %d", got, divaRewardItemID)
	}
	if got := data[6]; got != 7 {
		t.Errorf("reward item type=%d, want 7 for client inventory grant", got)
	}
	if got := binary.BigEndian.Uint16(data[7:9]); got != divaRewardItemID {
		t.Errorf("display reward item=%d, want %d", got, divaRewardItemID)
	}
	if got := binary.BigEndian.Uint16(data[9:11]); got != divaRewardQuantity {
		t.Errorf("reward quantity=%d, want %d", got, divaRewardQuantity)
	}
	pkt.AckHandle = 2
	handleMsgMhfAcquireUdItem(session, pkt)
	data = (<-session.sendPackets).data[dataOffset:]
	if len(data) != 2 || data[1] != 0 {
		t.Fatalf("duplicate claim payload=% X, want zero results", data)
	}

	// The client claim-all form sends the count but deliberately omits IDs.
	pkt.AckHandle = 3
	pkt.Unk0 = 1
	pkt.RewardType = 4
	pkt.ItemIDCount = 1
	pkt.ItemIDs = nil
	handleMsgMhfAcquireUdItem(session, pkt)
	data = (<-session.sendPackets).data[dataOffset:]
	if len(data) != 11 || data[1] != 1 {
		t.Fatalf("claim-all payload=% X, want one 9-byte result", data)
	}
}

func TestHandleMsgMhfAcquireUdItemRequiresCompletedInterceptionBranch(t *testing.T) {
	server := createMockServer()
	repo := &mockDivaRepo{
		events:             []DivaEvent{{ID: 8, StartTime: 12345}},
		interceptionPoints: map[string]int{"58081": 999},
		rewardClaims:       map[string]bool{"5:interception": true},
	}
	server.divaRepo = repo
	session := createMockSession(1, server)
	pkt := &mhfpacket.MsgMhfAcquireUdItem{
		AckHandle: 1, RewardType: 5, ItemIDCount: 1,
		ItemIDs: []uint32{uint32(divaRewardItemID)},
	}

	handleMsgMhfAcquireUdItem(session, pkt)
	const dataOffset = 10
	data := (<-session.sendPackets).data[dataOffset:]
	if len(data) != 2 || data[1] != 0 {
		t.Fatalf("incomplete branch claim payload=% X, want zero results", data)
	}

	repo.interceptionPoints["58081"] = int(divaInterceptionAreaPointRequirement)
	pkt.AckHandle = 2
	handleMsgMhfAcquireUdItem(session, pkt)
	data = (<-session.sendPackets).data[dataOffset:]
	if len(data) != 11 || data[1] != 1 {
		t.Fatalf("completed branch claim payload=% X, want one result", data)
	}
	if !repo.rewardClaims["5:branch:58081:12345"] {
		t.Fatal("completed branch did not use an event-run-specific reward key")
	}

	pkt.AckHandle = 3
	handleMsgMhfAcquireUdItem(session, pkt)
	data = (<-session.sendPackets).data[dataOffset:]
	if len(data) != 2 || data[1] != 0 {
		t.Fatalf("duplicate branch claim payload=% X, want zero results", data)
	}
}

func TestHandleMsgMhfGetUdRanking(t *testing.T) {
	tests := []struct {
		mode        uint8
		payloadSize int
	}{
		{mode: 0, payloadSize: 100 * 31},
		{mode: 1, payloadSize: 100 * 47},
		{mode: 2, payloadSize: 100 * 31},
		{mode: 3, payloadSize: 100 * 47},
	}

	for _, tt := range tests {
		server := createMockServer()
		session := createMockSession(1, server)
		pkt := &mhfpacket.MsgMhfGetUdRanking{AckHandle: 12345, Unk0: tt.mode}

		handleMsgMhfGetUdRanking(session, pkt)

		select {
		case p := <-session.sendPackets:
			const ackBufDataOffset = 10
			if got := len(p.data) - ackBufDataOffset; got != tt.payloadSize {
				t.Errorf("mode %d ranking payload size=%d, want %d", tt.mode, got, tt.payloadSize)
			}
		default:
			t.Errorf("mode %d: no response packet queued", tt.mode)
		}
	}
}

func TestHandleMsgMhfGetUdRankingWritesPersonalAndGuildEntries(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		events: []DivaEvent{{ID: 8}},
		personalRankings: []DivaRankingEntry{
			{ID: 1, Name: "HunterOne", Score: 860},
			{ID: 2, Name: "HunterTwo", Score: 400},
		},
		guildRankings: []DivaRankingEntry{
			{ID: 10, Name: "TestGuild", Score: 1260},
		},
	}
	session := createMockSession(1, server)
	const dataOffset = 10

	handleMsgMhfGetUdRanking(session, &mhfpacket.MsgMhfGetUdRanking{AckHandle: 1, Unk0: 0})
	personal := (<-session.sendPackets).data[dataOffset:]
	if got := binary.BigEndian.Uint16(personal[0:2]); got != 1 {
		t.Fatalf("personal rank=%d, want 1", got)
	}
	if got := string(bytes.TrimRight(personal[2:27], "\x00")); got != "HunterOne" {
		t.Errorf("personal name=%q, want HunterOne", got)
	}
	if got := binary.BigEndian.Uint32(personal[27:31]); got != 860 {
		t.Errorf("personal score=%d, want 860", got)
	}
	if got := binary.BigEndian.Uint16(personal[31:33]); got != 2 {
		t.Errorf("second personal rank=%d, want 2", got)
	}

	handleMsgMhfGetUdRanking(session, &mhfpacket.MsgMhfGetUdRanking{AckHandle: 2, Unk0: 1})
	guild := (<-session.sendPackets).data[dataOffset:]
	if got := binary.BigEndian.Uint16(guild[0:2]); got != 1 {
		t.Fatalf("guild rank=%d, want 1", got)
	}
	if got := string(bytes.TrimRight(guild[2:27], "\x00")); got != "TestGuild" {
		t.Errorf("guild name=%q, want TestGuild", got)
	}
	if got := binary.BigEndian.Uint32(guild[43:47]); got != 1260 {
		t.Errorf("guild score=%d, want 1260", got)
	}
}

func TestHandleMsgMhfGetUdMyRanking(t *testing.T) {
	server := createMockServer()
	server.divaRepo = &mockDivaRepo{
		events:           []DivaEvent{{ID: 8}},
		characterGuildID: 10,
		personalRankings: []DivaRankingEntry{
			{ID: 2, Name: "Other", Score: 900},
			{ID: 1, Name: "HunterOne", Score: 860},
		},
		guildRankings: []DivaRankingEntry{
			{ID: 10, Name: "TestGuild", Score: 1760},
		},
	}
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdMyRanking{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdMyRanking(session, pkt)

	select {
	case p := <-session.sendPackets:
		const dataOffset = 10
		data := p.data[dataOffset:]
		if len(data) != 49 {
			t.Fatalf("MyRanking payload=%d, want 49", len(data))
		}
		want := []uint32{2, 2, 860, 1, 1, 1760}
		for i, value := range want {
			if got := binary.BigEndian.Uint32(data[i*4 : i*4+4]); got != value {
				t.Errorf("MyRanking field %d=%d, want %d", i, got, value)
			}
		}
		if got := string(bytes.TrimRight(data[24:49], "\x00")); got != "TestGuild" {
			t.Errorf("MyRanking guild name=%q, want TestGuild", got)
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestGenerateDivaTimestamps_Debug(t *testing.T) {
	// Test debug mode timestamps
	tests := []struct {
		name  string
		start uint32
	}{
		{"Debug_Start1", 1},
		{"Debug_Start2", 2},
		{"Debug_Start3", 3},
	}

	server := createMockServer()
	session := createMockSession(1, server)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamps := generateDivaTimestamps(session, tt.start, true)
			if len(timestamps) != 6 {
				t.Errorf("Expected 6 timestamps, got %d", len(timestamps))
			}
			// Verify timestamps are non-zero
			for i, ts := range timestamps {
				if ts == 0 {
					t.Errorf("Timestamp %d should not be zero", i)
				}
			}
		})
	}
}

func TestGenerateDivaTimestamps_Debug_StartGreaterThan3(t *testing.T) {
	// Test debug mode with start > 3 (falls through to non-debug path)
	server := createMockServer()
	session := createMockSession(1, server)

	// With debug=true but start > 3, should fall through to non-debug path
	// This will try to access DB which will panic, so we catch it
	defer func() {
		if r := recover(); r != nil {
			t.Log("Expected panic due to nil database in test")
		}
	}()

	timestamps := generateDivaTimestamps(session, 100, true)
	if len(timestamps) != 6 {
		t.Errorf("Expected 6 timestamps, got %d", len(timestamps))
	}
}

func TestGenerateDivaTimestamps_NonDebug_WithValidStart(t *testing.T) {
	// Test non-debug mode with valid start timestamp (not expired)
	server := createMockServer()
	session := createMockSession(1, server)

	// Use a start time in the future (won't trigger cleanup)
	futureStart := uint32(TimeAdjusted().Unix() + 1000000) // Far in the future

	timestamps := generateDivaTimestamps(session, futureStart, false)
	if len(timestamps) != 6 {
		t.Errorf("Expected 6 timestamps, got %d", len(timestamps))
	}

	// Verify first timestamp matches start
	if timestamps[0] != futureStart {
		t.Errorf("First timestamp should match start, got %d want %d", timestamps[0], futureStart)
	}

	// Verify timestamp intervals
	if timestamps[1] != timestamps[0]+601200 {
		t.Error("Second timestamp should be start + 601200")
	}
	if timestamps[2] != timestamps[1]+3900 {
		t.Error("Third timestamp should be second + 3900")
	}
}

func TestHandleMsgMhfGetUdSchedule_DivaOverrideZero_ZZ(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = 0
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_DivaOverrideZero_OlderClient(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = 0
	srv.erupeConfig.RealClientMode = cfg.G10
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_DivaOverride1(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = 1
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_DivaOverride2(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = 2
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_DivaOverride3(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = 3
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_WithExistingEvent(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{
		events: []DivaEvent{{ID: 1, StartTime: uint32(time.Now().Unix())}},
	}
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfGetUdSchedule_NoEvents(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{}
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 1}
	handleMsgMhfGetUdSchedule(s, pkt)
	<-s.sendPackets
}
