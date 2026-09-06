package channelserver

import (
	"sort"
	"strconv"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

const divaTacticsCompletedQuestLimit = 255

func isDivaTacticsQuestID(questID uint16) bool {
	for _, candidate := range divaInterceptionQuestIDs {
		if questID == candidate {
			return true
		}
	}
	for _, candidate := range divaInterceptionBranchQuestIDs {
		if questID == candidate {
			return true
		}
	}
	return false
}

func divaTacticsCompletedQuestIDs(pointsMap map[string]int) []uint16 {
	questIDs := make([]uint16, 0, len(pointsMap))
	for rawQuestID, points := range pointsMap {
		if points <= 0 {
			continue
		}
		questID, err := strconv.ParseUint(rawQuestID, 10, 16)
		if err != nil || !isDivaTacticsQuestID(uint16(questID)) {
			continue
		}
		questIDs = append(questIDs, uint16(questID))
	}
	sort.Slice(questIDs, func(i, j int) bool { return questIDs[i] < questIDs[j] })
	if len(questIDs) > divaTacticsCompletedQuestLimit {
		questIDs = questIDs[:divaTacticsCompletedQuestLimit]
	}
	return questIDs
}

func writeDivaTacticsCompletedQuestIDs(bf *byteframe.ByteFrame, questIDs []uint16) {
	if len(questIDs) > divaTacticsCompletedQuestLimit {
		questIDs = questIDs[:divaTacticsCompletedQuestLimit]
	}
	bf.WriteUint8(uint8(len(questIDs)))
	for _, questID := range questIDs {
		bf.WriteUint16(questID)
	}
}

// divaTacticsPointTotal converts the persisted bigint-style point collection
// to the client's u32 protocol field without allowing an overflow to wrap the
// displayed total back to a smaller value.
func divaTacticsPointTotal(pointsMap map[string]int) uint32 {
	const maxTotal = ^uint32(0)
	total := uint64(0)
	for _, points := range pointsMap {
		if points <= 0 {
			continue
		}
		if uint64(points) >= uint64(maxTotal)-total {
			return maxTotal
		}
		total += uint64(points)
	}
	return uint32(total)
}

func handleMsgMhfGetUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	// Diva defense interception points
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsPoint)

	eventID, eventErr := getCurrentDivaEventID(s)
	pointsMap := map[string]int{}
	var err error
	if eventErr == nil && eventID != 0 {
		pointsMap, err = s.server.divaRepo.GetCharacterInterceptionPoints(s.charID, eventID)
	} else if eventErr != nil {
		err = eventErr
	}
	if err != nil {
		s.logger.Warn("Failed to get interception points", zap.Uint32("charID", s.charID), zap.Error(err))
		pointsMap = map[string]int{}
	}

	// The ZZ client decoder for MSG_MHF_GET_UD_TACTICS_POINT expects:
	//   status:u8, total:u32, entry_count:u8, entries:entry_count*u16
	// The entries are completed interception quest IDs, not quest ID/point
	// pairs. The client scans this fixed list while calculating the first-clear
	// point component; returning an empty list makes every repeat look like a
	// first clear and can inflate both the displayed total and urgent progress.
	// Returning the old u32 count followed by u32 pairs shifted the decoder and
	// made a persisted total disappear (or display arbitrary values) after the
	// result screen's locally calculated total was gone.
	total := divaTacticsPointTotal(pointsMap)
	s.logger.Debug("Retrieved Diva tactics points",
		zap.Uint32("charID", s.charID),
		zap.Uint32("total", total),
		zap.Int("storedQuestEntries", len(pointsMap)))

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // status: success
	bf.WriteUint32(total)
	writeDivaTacticsCompletedQuestIDs(bf, divaTacticsCompletedQuestIDs(pointsMap))

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfAddUdTacticsPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddUdTacticsPoint)
	questFileID := int(pkt.QuestID)
	points := int(pkt.TacticsPoints)
	s.logger.Info("Received Diva tactics point submission",
		zap.Uint32("charID", s.charID),
		zap.Int("questFileID", questFileID),
		zap.Int("points", points))

	if !isDivaTacticsQuestID(pkt.QuestID) {
		s.logger.Warn("AddUdTacticsPoint: unknown Diva quest file ID",
			zap.Int("questFileID", questFileID))
		// The client registered MSG_MHF_ADD_UD_TACTICS_POINT as a buffered
		// response and decodes status:u8, detail_count:u8, then u16 details.
		doAckBufSucceed(s, pkt.AckHandle, []byte{0x00, 0x00})
		return
	}

	eventID, eventErr := getCurrentDivaEventID(s)
	if points > 0 {
		if eventErr != nil || eventID == 0 {
			s.logger.Warn("Ignored Diva tactics points without a current event",
				zap.Uint32("charID", s.charID), zap.Error(eventErr))
		} else if err := s.server.divaRepo.AddInterceptionPoints(s.charID, eventID, questFileID, points); err != nil {
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

	// ADD uses the same completed-quest array as GET, but without the total.
	// Return the complete updated set because the decoder overwrites from slot
	// zero and its consumers scan all 255 slots without retaining a count.
	pointsMap := map[string]int{}
	if eventErr == nil && eventID != 0 {
		if storedPoints, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID, eventID); err != nil {
			s.logger.Warn("Failed to refresh completed Diva tactics quests",
				zap.Uint32("charID", s.charID), zap.Error(err))
		} else {
			pointsMap = storedPoints
		}
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // status: success
	writeDivaTacticsCompletedQuestIDs(bf, divaTacticsCompletedQuestIDs(pointsMap))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
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

const divaTacticsBonusQuestLimit = 32

type divaTacticsBonusQuestEntry struct {
	questID    uint16
	startEpoch uint32
	endEpoch   uint32
	points     uint16
}

// writeDivaTacticsBonusQuestList emits the exact structure decoded by the ZZ
// client: u8 count followed by 12-byte, big-endian schedule records. The client
// owns 32 fixed internal slots, so never advertise more than that.
func writeDivaTacticsBonusQuestList(bf *byteframe.ByteFrame, entries []divaTacticsBonusQuestEntry) {
	if len(entries) > divaTacticsBonusQuestLimit {
		entries = entries[:divaTacticsBonusQuestLimit]
	}
	bf.WriteUint8(uint8(len(entries)))
	for _, entry := range entries {
		bf.WriteUint16(entry.questID)
		bf.WriteUint32(entry.startEpoch)
		bf.WriteUint32(entry.endEpoch)
		bf.WriteUint16(entry.points)
	}
}

func handleMsgMhfGetUdTacticsBonusQuest(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsBonusQuest)
	bf := byteframe.NewByteFrame()
	// Opcode 0x185 is the time-limited Bonus Quest schedule shown in the
	// top-right HUD panel. It is not the branch-route quest list. Branch quests
	// are derived locally from the guild-map topology, so advertising one here
	// creates a misleading Bonus Quest notice without making it selectable from
	// the Branch Route menu. Until real bonus schedules are configured, return
	// the client's valid zero-entry representation.
	writeDivaTacticsBonusQuestList(bf, nil)
	s.logger.Debug("No Diva tactics bonus quest configured", zap.Uint32("charID", s.charID))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
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
	eventID, _ := getCurrentDivaEventID(s)
	if guildID != 0 && eventID != 0 {
		if score, err := s.server.divaRepo.GetInterceptionGuildMapScore(eventID, guildID); err == nil {
			guildScore = score
		}
	}
	remaining := uint32(0)
	if divaInterceptionAreaPointRequirement != 0 {
		// The generated route is shorter than the 60-cell render grid. At route
		// completion the terminal contact gauge deliberately keeps cycling, so
		// the remaining-point response must use the same modulo rule rather than
		// an unrelated 60,000-point board cap.
		remaining = divaInterceptionAreaPointRequirement - guildScore%divaInterceptionAreaPointRequirement
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(remaining)
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdTacticsRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTacticsRanking)
	eventID, eventErr := getCurrentDivaEventID(s)
	var rankings []DivaRankingEntry
	var err error
	if eventErr == nil && eventID != 0 {
		rankings, err = s.server.divaRepo.GetInterceptionGuildRankings(eventID)
	} else {
		err = eventErr
	}
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
