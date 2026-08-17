package channelserver

import (
	"encoding/hex"
	"fmt"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func handleMsgMhfGetUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	// Diva defense interception points
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsPoint)

	pointsMap, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID)
	if err != nil {
		s.logger.Warn("Failed to get interception points", zap.Uint32("charID", s.charID), zap.Error(err))
		pointsMap = map[string]int{}
	}

	// The ZZ client decoder for MSG_MHF_GET_UD_TACTICS_POINT expects:
	//   status:u8, total:u32, entry_count:u8, entries:entry_count*u16
	// The entries are optional map-detail values, not quest ID/point pairs.
	// Returning the old u32 count followed by u32 pairs shifted the decoder and
	// made a persisted total disappear (or display arbitrary values) after the
	// result screen's locally calculated total was gone.
	var total int
	for _, pts := range pointsMap {
		if pts <= 0 {
			continue
		}
		total += pts
	}
	s.logger.Debug("Retrieved Diva tactics points",
		zap.Uint32("charID", s.charID),
		zap.Int("total", total),
		zap.Int("storedQuestEntries", len(pointsMap)))

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // status: success
	bf.WriteUint32(uint32(total))
	bf.WriteUint8(0) // no optional map-detail values

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

// udTacticsQuestMin/Max bound the interception (Diva Defense) quest file IDs.
// Every ripped 58xxx quest_id in EventQuests.sql (quest_type 46/47/48, see
// isDivaDefenseQuestType in constants_quest.go) falls in 58043-58128; the
// previous 58079-58083 bound only covered one event batch out of 65 rows.
// This range isn't gap-free (a handful of unused IDs in between are also
// accepted), but that's harmless since no real quest ever sends them here.
const (
	udTacticsQuestMin = 58043
	udTacticsQuestMax = 58128
)

func handleMsgMhfAddUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddUdTacticsPoint)
	questFileID := int(pkt.QuestID)
	points := int(pkt.TacticsPoints)
	s.logger.Info("Received Diva tactics point submission",
		zap.Uint32("charID", s.charID),
		zap.Int("questFileID", questFileID),
		zap.Int("points", points))

	if questFileID < udTacticsQuestMin || questFileID > udTacticsQuestMax {
		s.logger.Warn("AddUdTacticsPoint: quest file ID out of range",
			zap.Int("questFileID", questFileID),
			zap.String("range", fmt.Sprintf("%d-%d", udTacticsQuestMin, udTacticsQuestMax)))
		// The client registered MSG_MHF_ADD_UD_TACTICS_POINT as a buffered
		// response and decodes status:u8, detail_count:u8, then u16 details.
		doAckBufSucceed(s, pkt.AckHandle, []byte{0x00, 0x00})
		return
	}

	if points > 0 {
		if err := s.server.divaRepo.AddInterceptionPoints(s.charID, questFileID, points); err != nil {
			s.logger.Warn("Failed to add interception points",
				zap.Uint32("charID", s.charID),
				zap.Int("questFileID", questFileID),
				zap.Int("points", points),
				zap.Error(err))
		} else {
			s.logger.Info("Saved Diva tactics point submission",
				zap.Uint32("charID", s.charID),
				zap.Int("questFileID", questFileID),
				zap.Int("points", points))
		}
	}

	// An empty successful detail list. A simple ACK here leaves the client's
	// interception submission state unresolved even though the DB update ran.
	doAckBufSucceed(s, pkt.AckHandle, []byte{0x00, 0x00})
}

const divaTacticsPrizeLimit = 512

func writeDivaPrizeList(bf *byteframe.ByteFrame, prizes []DivaPrize) {
	if len(prizes) == 0 {
		prizes = []DivaPrize{{PointsReq: 1}}
	}
	// The client stores each list in a fixed 512-entry array. Its decoder reads
	// a u16 count followed by 11-byte records; widening either the count or the
	// one-byte item type shifts every subsequent field and can overrun that
	// array while opening the Inspector reward screen.
	if len(prizes) > divaTacticsPrizeLimit {
		prizes = prizes[:divaTacticsPrizeLimit]
	}
	bf.WriteUint16(uint16(len(prizes)))
	for _, p := range prizes {
		bf.WriteUint32(uint32(p.PointsReq))
		bf.WriteUint8(divaRewardItemType)
		bf.WriteUint16(divaRewardItemID)
		bf.WriteUint16(divaRewardQuantity)
		// The Inspector uses this byte to select its HR/GR reward page. All
		// Diva Defense live-test rewards are GR rewards regardless of stale DB
		// prize rows created with gr=false by older migrations.
		bf.WriteUint8(1)
		if p.Repeatable {
			bf.WriteUint8(1)
		} else {
			bf.WriteUint8(0)
		}
	}
}

func writeDivaInterceptionAreaPrizeList(bf *byteframe.ByteFrame) {
	// The third Inspector list is the "Reclaimed Areas & Point" track. Its
	// client record is 13 bytes: item type/id/quantity followed by the reclaimed
	// area and interception-point requirements. Keep the live-test reward and
	// its maximum stack identical to the other Diva Defense tracks.
	bf.WriteUint16(1)
	bf.WriteUint8(divaRewardItemType)
	bf.WriteUint16(divaRewardItemID)
	bf.WriteUint16(divaRewardQuantity)
	bf.WriteUint32(1) // at least one reclaimed-area/progression unit
	bf.WriteUint32(1) // at least one interception point
}

func handleMsgMhfGetUdTacticsRewardList(s *Session, p mhfpacket.MHFPacket) {
	// Diva defense interception reward list
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRewardList)

	personal, err := s.server.divaRepo.GetPersonalPrizes()
	if err != nil {
		s.logger.Warn("Failed to get personal prizes", zap.Error(err))
	}
	guild, err := s.server.divaRepo.GetGuildPrizes()
	if err != nil {
		s.logger.Warn("Failed to get guild prizes", zap.Error(err))
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // status: success
	writeDivaPrizeList(bf, personal)
	writeDivaPrizeList(bf, guild)
	writeDivaInterceptionAreaPrizeList(bf)

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsFollower(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsFollower)
	doAckBufSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
}

func handleMsgMhfGetUdTacticsBonusQuest(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsBonusQuest)
	// Temporary canned response
	data, _ := hex.DecodeString("14E2F55DCBFE505DCC1A7003E8E2C55DCC6ED05DCC8AF00258E2CE5DCCDF505DCCFB700279E3075DCD4FD05DCD6BF0041AE2F15DCDC0505DCDDC700258E2C45DCE30D05DCE4CF00258E2F55DCEA1505DCEBD7003E8E2C25DCF11D05DCF2DF00258E2CE5DCF82505DCF9E700279E3075DCFF2D05DD00EF0041AE2CE5DD063505DD07F700279E2F35DD0D3D05DD0EFF0028AE2C35DD144505DD160700258E2F05DD1B4D05DD1D0F00258E2CE5DD225505DD241700279E2F55DD295D05DD2B1F003E8E2F25DD306505DD3227002EEE2CA5DD376D05DD392F00258E3075DD3E7505DD40370041AE2F55DD457D05DD473F003E82027313220686F757273273A3A696E74657276616C29202B2027313220686F757273273A3A696E74657276616C2047524F5550204259206D6170204F52444552204259206D61703B2000C7312B000032")
	doAckBufSucceed(s, pkt.AckHandle, data)
}

// udTacticsFirstQuestBonuses are the static first-quest bonus point values.
var udTacticsFirstQuestBonuses = []uint32{1500, 2000, 2500, 3500, 4500}

func handleMsgMhfGetUdTacticsFirstQuestBonus(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus)
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(len(udTacticsFirstQuestBonuses)))
	for i, bonus := range udTacticsFirstQuestBonuses {
		bf.WriteUint32(bonus)
		bf.WriteUint32(uint32(i))
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsRemainingPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRemainingPoint)
	guildID := pkt.Unk0
	if guildID == 0 {
		guildID, _ = s.server.divaRepo.GetCharacterGuildID(s.charID)
	}
	var guildScore uint32
	if guildID != 0 {
		if rankings, err := s.server.divaRepo.GetInterceptionGuildRankings(); err == nil {
			_, guildScore = findDivaRanking(rankings, guildID)
		}
	}
	remaining := uint32(0)
	if guildScore < divaInterceptionAreaPointRequirement*divaInterceptionAreaCount {
		remaining = divaInterceptionAreaPointRequirement - guildScore%divaInterceptionAreaPointRequirement
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(remaining)
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRanking)
	rankings, err := s.server.divaRepo.GetInterceptionGuildRankings()
	if err != nil {
		s.logger.Error("Failed to query Diva interception guild ranking", zap.Error(err))
		rankings = nil
	}

	// The client may send zero while asking for its own guild. Resolve it here
	// instead of ever placing the numeric guild ID in a display-name field.
	guildID := pkt.GuildID
	if guildID == 0 {
		guildID, err = s.server.divaRepo.GetCharacterGuildID(s.charID)
		if err != nil {
			s.logger.Error("Failed to resolve Diva interception guild", zap.Error(err))
			guildID = 0
		}
	}

	myRank, myScore := findDivaRanking(rankings, guildID)
	myGuildName := ""
	if myRank > 0 {
		myGuildName = rankings[myRank-1].Name
	}

	// mhfo.dll 0x11510490 decodes exactly:
	//   u32 own_rank, u32 own_score, char own_guild_name[32], u8 count
	//   count * (u32 rank, u32 score, char guild_name[32])
	// The registered 4041-byte result buffer therefore holds at most 100 rows.
	if len(rankings) > divaRankingLimit {
		rankings = rankings[:divaRankingLimit]
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(myRank)
	bf.WriteUint32(myScore)
	bf.WriteBytes(stringsupport.PaddedString(myGuildName, 32, true))
	bf.WriteUint8(uint8(len(rankings)))
	for i, ranking := range rankings {
		bf.WriteUint32(uint32(i + 1))
		bf.WriteUint32(divaRankingScore(ranking.Score))
		bf.WriteBytes(stringsupport.PaddedString(ranking.Name, 32, true))
	}
	s.logger.Debug("Retrieved Diva interception guild ranking",
		zap.Uint32("charID", s.charID),
		zap.Uint32("guildID", guildID),
		zap.Uint32("rank", myRank),
		zap.Uint32("score", myScore),
		zap.Int("entries", len(rankings)))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfSetUdTacticsFollower(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

// handleMsgMhfGetUdTacticsLog was previously a bare stub, which meant it sent
// no ack at all -- since the packet carries an AckHandle, that silence is a
// client softlock (see CLAUDE.md's ack requirement), not just a missing
// feature. The real log entry format hasn't been reverse engineered yet, so
// this returns an empty result via the same "no results" convention used
// elsewhere in this file (e.g. handleMsgMhfGetUdTacticsRemainingPoint)
// rather than fabricate a layout.
func handleMsgMhfGetUdTacticsLog(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsLog)
	stubEnumerateNoResults(s, pkt.AckHandle)
}
