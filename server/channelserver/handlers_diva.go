package channelserver

import (
	"fmt"
	"sort"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// Diva Defense event duration constants (all values in seconds)
const (
	divaPhaseDuration = 601200      // 6d 23h = first song phase
	divaInterlude     = 3900        // 65 min = gap between phases
	divaWeekDuration  = secsPerWeek // 7 days = subsequent phase length
	divaTotalLifespan = 2977200     // ~34.5 days = full event window
)

// divaTimestampsFromStart is the side-effect-free form of the schedule sent to
// the client. Read-only handlers use it so checking an availability window can
// never rotate the event or mutate the database.
func divaTimestampsFromStart(start uint32) []uint32 {
	timestamps := make([]uint32, 6)
	timestamps[0] = start
	timestamps[1] = timestamps[0] + divaPhaseDuration
	timestamps[2] = timestamps[1] + divaInterlude
	timestamps[3] = timestamps[1] + divaWeekDuration
	timestamps[4] = timestamps[3] + divaInterlude
	timestamps[5] = timestamps[3] + divaWeekDuration
	return timestamps
}

func cleanupDiva(s *Session) {
	if err := s.server.divaRepo.DeleteEvents(); err != nil {
		s.logger.Error("Failed to delete diva events", zap.Error(err))
	}
	if err := s.server.divaRepo.CleanupBeads(); err != nil {
		s.logger.Error("Failed to cleanup diva beads", zap.Error(err))
	}
}

func generateDivaTimestamps(s *Session, start uint32, debug bool) []uint32 {
	if debug && start <= 3 {
		timestamps := make([]uint32, 6)
		midnight := uint32(TimeMidnight().Unix())
		switch start {
		case 1:
			timestamps[0] = midnight
			timestamps[1] = timestamps[0] + divaPhaseDuration
			timestamps[2] = timestamps[1] + divaInterlude
			timestamps[3] = timestamps[1] + divaWeekDuration
			timestamps[4] = timestamps[3] + divaInterlude
			timestamps[5] = timestamps[3] + divaWeekDuration
		case 2:
			timestamps[0] = midnight - (divaPhaseDuration + divaInterlude)
			timestamps[1] = midnight - divaInterlude
			timestamps[2] = midnight
			timestamps[3] = timestamps[1] + divaWeekDuration
			timestamps[4] = timestamps[3] + divaInterlude
			timestamps[5] = timestamps[3] + divaWeekDuration
		case 3:
			timestamps[0] = midnight - (divaPhaseDuration + divaInterlude + divaWeekDuration + divaInterlude)
			timestamps[1] = midnight - (divaWeekDuration + divaInterlude)
			timestamps[2] = midnight - divaWeekDuration
			timestamps[3] = midnight - divaInterlude
			timestamps[4] = midnight
			timestamps[5] = timestamps[3] + divaWeekDuration
		}
		return timestamps
	}
	if start == 0 || TimeAdjusted().Unix() > int64(start)+divaTotalLifespan {
		cleanupDiva(s)
		// Generate a new diva defense, starting midnight tomorrow
		start = uint32(TimeMidnight().Add(24 * time.Hour).Unix())
		if err := s.server.divaRepo.InsertEvent(start); err != nil {
			s.logger.Error("Failed to insert diva event", zap.Error(err))
		}
	}
	return divaTimestampsFromStart(start)
}

func handleMsgMhfGetUdSchedule(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdSchedule)
	bf := byteframe.NewByteFrame()

	const divaIDSentinel = uint32(0xCAFEBEEF)
	id, start := divaIDSentinel, uint32(0)
	events, err := s.server.divaRepo.GetEvents()
	if err != nil {
		s.logger.Error("Failed to query diva schedule", zap.Error(err))
	} else if len(events) > 0 {
		last := events[len(events)-1]
		id = last.ID
		start = last.StartTime
	}

	var timestamps []uint32
	if s.server.erupeConfig.DebugOptions.DivaOverride >= 0 {
		if s.server.erupeConfig.DebugOptions.DivaOverride == 0 {
			if s.server.erupeConfig.RealClientMode >= cfg.Z2 {
				doAckBufSucceed(s, pkt.AckHandle, make([]byte, 36))
			} else {
				doAckBufSucceed(s, pkt.AckHandle, make([]byte, 32))
			}
			return
		}
		timestamps = generateDivaTimestamps(s, uint32(s.server.erupeConfig.DebugOptions.DivaOverride), true)
	} else {
		timestamps = generateDivaTimestamps(s, start, false)
	}

	if s.server.erupeConfig.RealClientMode >= cfg.Z2 {
		bf.WriteUint32(id)
	}
	for i := range timestamps {
		bf.WriteUint32(timestamps[i])
	}

	bf.WriteUint16(0x19) // Unk 00011001
	bf.WriteUint16(0x2D) // Unk 00101101
	bf.WriteUint16(0x02) // Unk 00000010
	bf.WriteUint16(0x02) // Unk 00000010

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdInfo)
	// Message that appears on the Diva Defense NPC and triggers the green exclamation mark
	udInfos := []struct {
		Text      string
		StartTime time.Time
		EndTime   time.Time
	}{}

	resp := byteframe.NewByteFrame()
	resp.WriteUint8(uint8(len(udInfos)))
	for _, udInfo := range udInfos {
		resp.WriteBytes(stringsupport.PaddedString(udInfo.Text, 1024, true))
		resp.WriteUint32(uint32(udInfo.StartTime.Unix()))
		resp.WriteUint32(uint32(udInfo.EndTime.Unix()))
	}

	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

// defaultBeadTypes are used when the database has no bead rows configured.
var defaultBeadTypes = []int{1, 3, 4, 8}

const maxDivaBeadSlots = 4

func getDivaBeadTypes(repo DivaRepo) []int {
	beadTypes, err := repo.GetBeads()
	if err != nil || len(beadTypes) == 0 {
		beadTypes = defaultBeadTypes
	}
	if len(beadTypes) > maxDivaBeadSlots {
		beadTypes = beadTypes[:maxDivaBeadSlots]
	}
	return beadTypes
}

func handleMsgMhfGetKijuInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetKijuInfo)

	// RE-confirmed entry layout (546 bytes each):
	//   +0x000 char[32]  name
	//   +0x020 char[512] description
	//   +0x220 u8        color_id  (slot index, 1-based)
	//   +0x221 u8        bead_type (effect ID)
	// Response: u8 count + count × 546 bytes.
	beadTypes := getDivaBeadTypes(s.server.divaRepo)

	lang := getLangStrings(s.server)
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(uint8(len(beadTypes)))
	for i, bt := range beadTypes {
		name, desc := lang.beadName(bt), lang.beadDescription(bt)
		bf.WriteBytes(stringsupport.PaddedString(name, 32, true))
		bf.WriteBytes(stringsupport.PaddedString(desc, 512, true))
		bf.WriteUint8(uint8(i + 1)) // color_id: slot 1..N
		bf.WriteUint8(uint8(bt))    // bead_type: effect ID
	}

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfSetKiju(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSetKiju)
	colorID := int(pkt.Unk1)
	beadTypes := getDivaBeadTypes(s.server.divaRepo)
	if colorID < 1 || colorID > len(beadTypes) {
		s.logger.Warn("Rejected invalid Diva bead selection",
			zap.Uint32("charID", s.charID),
			zap.Int("colorID", colorID),
			zap.Int("slotCount", len(beadTypes)))
		doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00})
		return
	}
	expiry := nextDivaSelectionExpiry(TimeAdjusted())
	if err := s.server.divaRepo.AssignBead(s.charID, colorID, expiry); err != nil {
		s.logger.Warn("Failed to assign bead",
			zap.Uint32("charID", s.charID),
			zap.Int("colorID", colorID),
			zap.Error(err))
	} else {
		s.currentBeadIndex = colorID
		s.logger.Info("Saved Diva bead selection",
			zap.Uint32("charID", s.charID),
			zap.Int("colorID", colorID))
	}
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00})
}

// nextDivaSelectionExpiry aligns prayer-bead selection with the same JST noon
// boundary used by Diva daily results instead of using a rolling 24 hours.
func nextDivaSelectionExpiry(now time.Time) time.Time {
	y, m, d := now.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, now.Location())
	if !now.Before(noon) {
		noon = noon.Add(24 * time.Hour)
	}
	return noon
}

func handleMsgMhfAddUdPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAddUdPoint)
	// Prayer-bead selection persists across the JST-noon tally boundary. Reload
	// a missing session value before submission so reconnects cannot record only
	// the event total while silently omitting the daily bead row.
	if s.currentBeadIndex < 0 {
		restoreDivaBeadSelection(s)
	}
	s.logger.Info("Received Diva point submission",
		zap.Uint32("charID", s.charID),
		zap.Uint32("questPoints", pkt.QuestPoints),
		zap.Uint32("bonusPoints", pkt.BonusPoints),
		zap.Uint32("ackHandle", pkt.AckHandle),
		zap.Int("activeColorID", s.currentBeadIndex))
	if s.hasLastDivaPointAck && s.lastDivaPointAck == pkt.AckHandle {
		s.logger.Info("Ignored duplicate Diva point submission",
			zap.Uint32("charID", s.charID),
			zap.Uint32("ackHandle", pkt.AckHandle))
		doAckBufSucceed(s, pkt.AckHandle, []byte{0x00})
		return
	}

	// Find the current diva event to associate points with.
	eventID := uint32(0)
	if s.server.divaRepo != nil {
		events, err := s.server.divaRepo.GetEvents()
		if err == nil && len(events) > 0 {
			eventID = events[len(events)-1].ID
		}
	}

	if eventID != 0 && s.charID != 0 && (pkt.QuestPoints > 0 || pkt.BonusPoints > 0) {
		if err := s.server.divaRepo.AddPointSubmission(s.charID, eventID, pkt.QuestPoints, pkt.BonusPoints, s.currentBeadIndex); err != nil {
			s.logger.Warn("Failed to add diva points",
				zap.Uint32("charID", s.charID),
				zap.Uint32("questPoints", pkt.QuestPoints),
				zap.Uint32("bonusPoints", pkt.BonusPoints),
				zap.Error(err))
		} else {
			s.lastDivaPointAck = pkt.AckHandle
			s.hasLastDivaPointAck = true
			s.logger.Info("Saved Diva point submission",
				zap.Uint32("charID", s.charID),
				zap.Uint32("eventID", eventID),
				zap.Uint32("questPoints", pkt.QuestPoints),
				zap.Uint32("bonusPoints", pkt.BonusPoints))
		}
	} else {
		s.logger.Warn("Ignored Diva point submission",
			zap.Uint32("charID", s.charID),
			zap.Uint32("eventID", eventID),
			zap.Uint32("questPoints", pkt.QuestPoints),
			zap.Uint32("bonusPoints", pkt.BonusPoints))
	}

	doAckBufSucceed(s, pkt.AckHandle, []byte{0x00})
}

func handleMsgMhfGetUdMyPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMyPoint)

	// RE confirms: u8 error/status followed by exactly 8 entries; no count prefix.
	// Per-entry stride is 18 bytes:
	//   +0x00 u8  bead A index
	//   +0x01 u32 bead A quest points
	//   +0x05 u32 bead A bonus points
	//   +0x09 u8  bead B index
	//   +0x0A u32 bead B quest points
	//   +0x0E u32 bead B bonus points
	// Total: 1 + 8 × 18 = 145 bytes.
	eventID := uint32(0)
	events, err := s.server.divaRepo.GetEvents()
	if err == nil && len(events) > 0 {
		eventID = events[len(events)-1].ID
	}
	var entries []DivaBeadPointEntry
	if eventID != 0 {
		entries, err = s.server.divaRepo.GetCharacterBeadPointEntries(s.charID, eventID)
	}
	if err != nil {
		s.logger.Warn("Failed to get Diva character point history", zap.Uint32("charID", s.charID), zap.Uint32("eventID", eventID), zap.Error(err))
		entries = nil
	}
	// Refresh the persisted selection. A bead remains selected across noon; its
	// expiry is the change-lock boundary rather than selection invalidation.
	restoreDivaBeadSelection(s)

	type pointPair struct{ quest, bonus uint64 }
	daily := make([]map[int]pointPair, 8)
	if len(events) > 0 {
		eventStart := time.Unix(int64(events[len(events)-1].StartTime), 0)
		for _, entry := range entries {
			for day := 0; day < len(daily); day++ {
				start, end := divaTallyWindow(eventStart, day)
				if !entry.Timestamp.Before(start) && entry.Timestamp.Before(end) {
					if daily[day] == nil {
						daily[day] = make(map[int]pointPair)
					}
					p := daily[day][entry.BeadIndex]
					p.quest += uint64(entry.QuestPoints)
					p.bonus += uint64(entry.BonusPoints)
					daily[day][entry.BeadIndex] = p
					break
				}
			}
		}
		// A newly selected bead must remain visible after reconnect even before
		// the player earns the first points of that day.
		if s.currentBeadIndex >= 0 {
			now := TimeAdjusted()
			for day := 0; day < len(daily); day++ {
				start, end := divaTallyWindow(eventStart, day)
				if !now.Before(start) && now.Before(end) {
					if daily[day] == nil {
						daily[day] = make(map[int]pointPair)
					}
					if _, exists := daily[day][s.currentBeadIndex]; !exists {
						daily[day][s.currentBeadIndex] = pointPair{}
					}
					break
				}
			}
		}
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // error/status: success
	for day := 0; day < 8; day++ {
		beads := make([]int, 0, len(daily[day]))
		for bead := range daily[day] {
			beads = append(beads, bead)
		}
		sort.Ints(beads)
		for slot := 0; slot < 2; slot++ {
			if slot < len(beads) {
				bead := beads[slot]
				points := daily[day][bead]
				bf.WriteUint8(uint8(bead))
				bf.WriteUint32(uint32(points.quest))
				bf.WriteUint32(uint32(points.bonus))
			} else {
				bf.WriteUint8(0)
				bf.WriteUint32(0)
				bf.WriteUint32(0)
			}
		}
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

// udMilestones are the global contribution milestones for Diva Defense.
// RE confirms: 64 × u64 target_values + 64 × u8 target_types + u64 total = ~585 bytes.
// Slots 0–12 are populated; slots 13–63 are zero.
var udMilestones = []uint64{
	500000, 1000000, 2000000, 3000000, 5000000, 7000000, 10000000,
	15000000, 20000000, 30000000, 50000000, 70000000, 100000000,
}

func handleMsgMhfGetUdTotalPointInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdTotalPointInfo)

	eventID := uint32(0)
	events, eventErr := s.server.divaRepo.GetEvents()
	if eventErr == nil && len(events) > 0 {
		eventID = events[len(events)-1].ID
	}
	total, err := s.server.divaRepo.GetTotalBeadPoints(eventID)
	if err != nil {
		s.logger.Warn("Failed to get total bead points", zap.Error(err))
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // error = success
	// 64 × u64 target_values (big-endian)
	for i := 0; i < 64; i++ {
		var v uint64
		if i < len(udMilestones) {
			v = udMilestones[i]
		}
		bf.WriteUint64(v)
	}
	// 64 × u8 target_types (0 = global)
	for i := 0; i < 64; i++ {
		bf.WriteUint8(0)
	}
	// u64 total_souls
	bf.WriteUint64(uint64(total))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdSelectedColorInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdSelectedColorInfo)

	// RE confirms: exactly 9 bytes = u8 error + u8[8] winning colors.
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0) // error = success
	var eventStart time.Time
	var eventID uint32
	events, err := s.server.divaRepo.GetEvents()
	if err != nil {
		s.logger.Error("Failed to query Diva event for selected colors", zap.Error(err))
	} else if len(events) > 0 {
		currentEvent := events[len(events)-1]
		eventID = currentEvent.ID
		eventStart = time.Unix(int64(currentEvent.StartTime), 0)
	}
	for day := 0; day < 8; day++ {
		topBead := 0
		if !eventStart.IsZero() {
			topBead, err = s.server.divaRepo.GetTopBeadPerDay(eventID, eventStart, day)
		}
		if err != nil {
			s.logger.Warn("Failed to get Diva winning color",
				zap.Int("day", day),
				zap.Error(err))
			topBead = 0
		}
		bf.WriteUint8(uint8(topBead))
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdMonsterPoint(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMonsterPoint)

	monsterPoints := []struct {
		MID    uint8
		Points uint16
	}{
		{MID: 0x01, Points: 0x3C}, // em1 Rathian
		{MID: 0x02, Points: 0x5A}, // em2 Fatalis
		{MID: 0x06, Points: 0x14}, // em6 Yian Kut-Ku
		{MID: 0x07, Points: 0x50}, // em7 Lao-Shan Lung
		{MID: 0x08, Points: 0x28}, // em8 Cephadrome
		{MID: 0x0B, Points: 0x3C}, // em11 Rathalos
		{MID: 0x0E, Points: 0x3C}, // em14 Diablos
		{MID: 0x0F, Points: 0x46}, // em15 Khezu
		{MID: 0x11, Points: 0x46}, // em17 Gravios
		{MID: 0x14, Points: 0x28}, // em20 Gypceros
		{MID: 0x15, Points: 0x3C}, // em21 Plesioth
		{MID: 0x16, Points: 0x32}, // em22 Basarios
		{MID: 0x1A, Points: 0x32}, // em26 Monoblos
		{MID: 0x1B, Points: 0x0A}, // em27 Velocidrome
		{MID: 0x1C, Points: 0x0A}, // em28 Gendrome
		{MID: 0x1F, Points: 0x0A}, // em31 Iodrome
		{MID: 0x21, Points: 0x50}, // em33 Kirin
		{MID: 0x24, Points: 0x64}, // em36 Crimson Fatalis
		{MID: 0x25, Points: 0x3C}, // em37 Pink Rathian
		{MID: 0x26, Points: 0x1E}, // em38 Blue Yian Kut-Ku
		{MID: 0x27, Points: 0x28}, // em39 Purple Gypceros
		{MID: 0x28, Points: 0x50}, // em40 Yian Garuga
		{MID: 0x29, Points: 0x5A}, // em41 Silver Rathalos
		{MID: 0x2A, Points: 0x50}, // em42 Gold Rathian
		{MID: 0x2B, Points: 0x3C}, // em43 Black Diablos
		{MID: 0x2C, Points: 0x3C}, // em44 White Monoblos
		{MID: 0x2D, Points: 0x46}, // em45 Red Khezu
		{MID: 0x2E, Points: 0x3C}, // em46 Green Plesioth
		{MID: 0x2F, Points: 0x50}, // em47 Black Gravios
		{MID: 0x30, Points: 0x1E}, // em48 Daimyo Hermitaur
		{MID: 0x31, Points: 0x3C}, // em49 Azure Rathalos
		{MID: 0x32, Points: 0x50}, // em50 Ashen Lao-Shan Lung
		{MID: 0x33, Points: 0x3C}, // em51 Blangonga
		{MID: 0x34, Points: 0x28}, // em52 Congalala
		{MID: 0x35, Points: 0x50}, // em53 Rajang
		{MID: 0x36, Points: 0x6E}, // em54 Kushala Daora
		{MID: 0x37, Points: 0x50}, // em55 Shen Gaoren
		{MID: 0x3A, Points: 0x50}, // em58 Yama Tsukami
		{MID: 0x3B, Points: 0x6E}, // em59 Chameleos
		{MID: 0x40, Points: 0x64}, // em64 Lunastra
		{MID: 0x41, Points: 0x6E}, // em65 Teostra
		{MID: 0x43, Points: 0x28}, // em67 Shogun Ceanataur
		{MID: 0x44, Points: 0x0A}, // em68 Bulldrome
		{MID: 0x47, Points: 0x6E}, // em71 White Fatalis
		{MID: 0x4A, Points: 0xFA}, // em74 Hypnocatrice
		{MID: 0x4B, Points: 0xFA}, // em75 Lavasioth
		{MID: 0x4C, Points: 0x46}, // em76 Tigrex
		{MID: 0x4D, Points: 0x64}, // em77 Akantor
		{MID: 0x4E, Points: 0xFA}, // em78 Bright Hypnoc
		{MID: 0x4F, Points: 0xFA}, // em79 Lavasioth Subspecies
		{MID: 0x50, Points: 0xFA}, // em80 Espinas
		{MID: 0x51, Points: 0xFA}, // em81 Orange Espinas
		{MID: 0x52, Points: 0xFA}, // em82 White Hypnoc
		{MID: 0x53, Points: 0xFA}, // em83 Akura Vashimu
		{MID: 0x54, Points: 0xFA}, // em84 Akura Jebia
		{MID: 0x55, Points: 0xFA}, // em85 Berukyurosu
		{MID: 0x59, Points: 0xFA}, // em89 Pariapuria
		{MID: 0x5A, Points: 0xFA}, // em90 White Espinas
		{MID: 0x5B, Points: 0xFA}, // em91 Kamu Orugaron
		{MID: 0x5C, Points: 0xFA}, // em92 Nono Orugaron
		{MID: 0x5E, Points: 0xFA}, // em94 Dyuragaua
		{MID: 0x5F, Points: 0xFA}, // em95 Doragyurosu
		{MID: 0x60, Points: 0xFA}, // em96 Gurenzeburu
		{MID: 0x63, Points: 0xFA}, // em99 Rukodiora
		{MID: 0x65, Points: 0xFA}, // em101 Gogomoa
		{MID: 0x67, Points: 0xFA}, // em103 Taikun Zamuza
		{MID: 0x68, Points: 0xFA}, // em104 Abiorugu
		{MID: 0x69, Points: 0xFA}, // em105 Kuarusepusu
		{MID: 0x6A, Points: 0xFA}, // em106 Odibatorasu
		{MID: 0x6B, Points: 0xFA}, // em107 Disufiroa
		{MID: 0x6C, Points: 0xFA}, // em108 Rebidiora
		{MID: 0x6D, Points: 0xFA}, // em109 Anorupatisu
		{MID: 0x6E, Points: 0xFA}, // em110 Hyujikiki
		{MID: 0x6F, Points: 0xFA}, // em111 Midogaron
		{MID: 0x70, Points: 0xFA}, // em112 Giaorugu
		{MID: 0x72, Points: 0xFA}, // em114 Farunokku
		{MID: 0x73, Points: 0xFA}, // em115 Pokaradon
		{MID: 0x74, Points: 0xFA}, // em116 Shantien
		{MID: 0x77, Points: 0xFA}, // em119 Goruganosu
		{MID: 0x78, Points: 0xFA}, // em120 Aruganosu
		{MID: 0x79, Points: 0xFA}, // em121 Baruragaru
		{MID: 0x7A, Points: 0xFA}, // em122 Zerureusu
		{MID: 0x7B, Points: 0xFA}, // em123 Gougarf
		{MID: 0x7D, Points: 0xFA}, // em125 Forokururu
		{MID: 0x7E, Points: 0xFA}, // em126 Meraginasu
		{MID: 0x7F, Points: 0xFA}, // em127 Diorekkusu
		{MID: 0x80, Points: 0xFA}, // em128 Garuba Daora
		{MID: 0x81, Points: 0xFA}, // em129 Inagami
		{MID: 0x82, Points: 0xFA}, // em130 Varusaburosu
		{MID: 0x83, Points: 0xFA}, // em131 Poborubarumu
		{MID: 0x8B, Points: 0xFA}, // em139 Gureadomosu
		{MID: 0x8C, Points: 0xFA}, // em140 Harudomerugu
		{MID: 0x8D, Points: 0xFA}, // em141 Toridcless
		{MID: 0x8E, Points: 0xFA}, // em142 Gasurabazura
		{MID: 0x90, Points: 0xFA}, // em144 Yama Kurai
		{MID: 0x92, Points: 0x78}, // em146 Zinogre
		{MID: 0x93, Points: 0x78}, // em147 Deviljho
		{MID: 0x94, Points: 0x78}, // em148 Brachydios
		{MID: 0x96, Points: 0xFA}, // em150 Toa Tesukatora
		{MID: 0x97, Points: 0x78}, // em151 Barioth
		{MID: 0x98, Points: 0x78}, // em152 Uragaan
		{MID: 0x99, Points: 0x78}, // em153 Stygian Zinogre
		{MID: 0x9A, Points: 0xFA}, // em154 Guanzorumu
		{MID: 0x9E, Points: 0xFA}, // em158 Voljang
		{MID: 0x9F, Points: 0x78}, // em159 Nargacuga
		{MID: 0xA0, Points: 0xFA}, // em160 Keoaruboru
		{MID: 0xA1, Points: 0xFA}, // em161 Zenaserisu
		{MID: 0xA2, Points: 0x78}, // em162 Gore Magala
		{MID: 0xA4, Points: 0x78}, // em164 Shagaru Magala
		{MID: 0xA5, Points: 0x78}, // em165 Amatsu
		{MID: 0xA6, Points: 0xFA}, // em166 Elzelion
		{MID: 0xA9, Points: 0x78}, // em169 Seregios
		{MID: 0xAA, Points: 0xFA}, // em170 Bogabadorumu
	}

	resp := byteframe.NewByteFrame()
	resp.WriteUint8(uint8(len(monsterPoints)))
	for _, mp := range monsterPoints {
		resp.WriteUint8(mp.MID)
		resp.WriteUint16(mp.Points)
	}

	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgMhfGetUdDailyPresentList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdDailyPresentList)
	// DailyPresentList: u16 count + count × 15-byte entries.
	// Entry: u8 item_type, u16 item_id, u16 quantity, u8 condition_type,
	//        u32 condition_from, u32 condition_to, u8 participation_day.
	// Condition type 1 selects the GR page/range branch. The final byte is a
	// one-based grouping/display key for participation days 1..7.
	bf := byteframe.NewByteFrame()
	bf.WriteUint16(7)
	for day := uint16(1); day <= 7; day++ {
		bf.WriteUint8(divaRewardItemType)
		bf.WriteUint16(divaRewardItemID)
		bf.WriteUint16(divaRewardQuantity)
		bf.WriteUint8(1)  // GR condition
		bf.WriteUint32(1) // GR1
		bf.WriteUint32(999)
		bf.WriteUint8(uint8(day))
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdNormaPresentList(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdNormaPresentList)
	// NormaPresentList: u16 count + count × 19-byte entries.
	// Same item/condition layout as DailyPresent (+0x00..+0x0D), plus:
	//   +0x0E u32 points_required (norma threshold)
	//   +0x12 u8  bead_type (BeadType that unlocks this tier)
	bf := byteframe.NewByteFrame()
	bf.WriteUint16(1)
	bf.WriteUint8(divaRewardItemType)
	bf.WriteUint16(divaRewardItemID)
	bf.WriteUint16(divaRewardQuantity)
	bf.WriteUint8(1) // GR condition
	bf.WriteUint32(1)
	bf.WriteUint32(999) // GR1 and above (retail maximum GR is 999)
	bf.WriteUint32(1)   // any positive personal contribution
	bf.WriteUint8(0)    // any prayer bead
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

const (
	// Client item acquisition dispatches ordinary inventory items only for
	// type 7. Type 0 is displayable but deliberately performs no grant.
	divaRewardItemType = uint8(7)
	divaRewardItemID   = uint16(12306)
	divaRewardQuantity = uint16(99)
	divaRewardMaxBatch = 33
)

type divaGrantedReward struct {
	rewardID uint32
	itemType uint8
	itemID   uint16
	quantity uint16
}

func divaRewardKeys(s *Session, event DivaEvent, rewardType uint8) ([]string, error) {
	switch rewardType {
	case 0: // daily participation, one claim for every contributed prayer day
		days, err := s.server.divaRepo.GetParticipationDays(
			s.charID, event.ID, time.Unix(int64(event.StartTime), 0))
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(days))
		for _, day := range days {
			keys = append(keys, fmt.Sprintf("day:%d", day+1))
		}
		return keys, nil
	case 1, 4: // normal-Diva personal milestone/GCP
		quest, bonus, err := s.server.divaRepo.GetPoints(s.charID, event.ID)
		if err != nil || quest+bonus <= 0 {
			return nil, err
		}
		return []string{"participation"}, nil
	case 6: // interception personal achievement
		points, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID, event.ID)
		if err != nil {
			return nil, err
		}
		for _, value := range points {
			if value > 0 {
				return []string{"interception-personal"}, nil
			}
		}
		return nil, nil
	case 2: // personal ranking
		rankings, err := s.server.divaRepo.GetPersonalRankings(event.ID)
		if err != nil {
			return nil, err
		}
		rank, _ := findDivaRanking(rankings, s.charID)
		if rank == 0 {
			return nil, nil
		}
		return []string{"ranking"}, nil
	case 3: // normal-Diva guild ranking
		guildID, err := s.server.divaRepo.GetCharacterGuildID(s.charID)
		if err != nil || guildID == 0 {
			return nil, err
		}
		rankings, err := s.server.divaRepo.GetGuildRankings(event.ID)
		if err != nil {
			return nil, err
		}
		rank, _ := findDivaRanking(rankings, guildID)
		if rank == 0 {
			return nil, nil
		}
		return []string{"guild"}, nil
	case 7: // interception guild achievement
		guildID, err := s.server.divaRepo.GetCharacterGuildID(s.charID)
		if err != nil || guildID == 0 {
			return nil, err
		}
		rankings, err := s.server.divaRepo.GetInterceptionGuildRankings(event.ID)
		if err != nil {
			return nil, err
		}
		rank, _ := findDivaRanking(rankings, guildID)
		if rank == 0 {
			return nil, nil
		}
		return []string{"interception-guild"}, nil
	case 5: // interception treasure/achievement
		points, err := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID, event.ID)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(divaInterceptionBranchQuestIDs))
		for _, questID := range divaInterceptionBranchQuestIDs {
			if points[fmt.Sprint(questID)] >= int(divaInterceptionAreaPointRequirement) {
				// Some installations reuse the same events row for another run.
				// Include its start timestamp so a previous run's generic or branch
				// claim cannot suppress the current treasure reward.
				keys = append(keys, fmt.Sprintf("branch:%d:%d", questID, event.StartTime))
			}
		}
		return keys, nil
	}
	return nil, nil
}

func handleMsgMhfAcquireUdItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAcquireUdItem)
	// Client response decoder expects a buffered payload:
	//   u8 status, u8 result_count, result_count * 9-byte entries.
	// Each result is u32 reward_id, u8 item_type, u16 item_id, u16 quantity.
	// Returning a simple ACK makes the client treat unrelated ACK bytes as the
	// result array and can terminate the client while claiming a daily reward.
	granted := make([]divaGrantedReward, 0, len(pkt.ItemIDs))
	events, err := s.server.divaRepo.GetEvents()
	if err == nil && len(events) > 0 && pkt.RewardType <= 7 {
		event := events[len(events)-1]
		keys, keyErr := divaRewardKeys(s, event, pkt.RewardType)
		if keyErr != nil {
			s.logger.Error("Failed to resolve Diva reward eligibility", zap.Error(keyErr))
		} else {
			requestCount := len(pkt.ItemIDs)
			claimAll := pkt.Unk0 != 0
			if claimAll {
				requestCount = int(pkt.ItemIDCount)
				if requestCount == 0 || requestCount > len(keys) {
					requestCount = len(keys)
				}
			}
			if requestCount > divaRewardMaxBatch {
				requestCount = divaRewardMaxBatch
			}
			for i := 0; i < requestCount; i++ {
				if !claimAll && pkt.ItemIDs[i] != uint32(divaRewardItemID) {
					continue
				}
				for _, key := range keys {
					claimed, claimErr := s.server.divaRepo.TryClaimReward(
						event.ID, s.charID, pkt.RewardType, key,
						uint32(divaRewardItemID), uint32(divaRewardQuantity))
					if claimErr != nil {
						s.logger.Error("Failed to reserve Diva reward", zap.Error(claimErr))
						break
					}
					if !claimed {
						continue
					}
					if markErr := s.server.divaRepo.MarkRewardDelivered(event.ID, s.charID, pkt.RewardType, key); markErr != nil {
						_ = s.server.divaRepo.ReleaseRewardClaim(event.ID, s.charID, pkt.RewardType, key)
						s.logger.Error("Failed to mark Diva reward delivered", zap.Error(markErr))
						break
					}
					granted = append(granted, divaGrantedReward{
						rewardID: uint32(divaRewardItemID), itemType: divaRewardItemType,
						itemID:   divaRewardItemID,
						quantity: divaRewardQuantity,
					})
					break
				}
			}
		}
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint8(uint8(len(granted)))
	for _, reward := range granted {
		bf.WriteUint32(reward.rewardID)
		bf.WriteUint8(reward.itemType)
		bf.WriteUint16(reward.itemID)
		bf.WriteUint16(reward.quantity)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

const divaRankingLimit = 100

func divaRankingScore(score int64) uint32 {
	if score <= 0 {
		return 0
	}
	if score > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(score)
}

func getCurrentDivaEventID(s *Session) (uint32, error) {
	events, err := s.server.divaRepo.GetEvents()
	if err != nil || len(events) == 0 {
		return 0, err
	}
	return events[len(events)-1].ID, nil
}

// writeDivaPersonalRankings writes the client's fixed 100-entry shape:
// u16 rank, char[25] name, u32 score.
func writeDivaPersonalRankings(bf *byteframe.ByteFrame, rankings []DivaRankingEntry) {
	for i := 0; i < divaRankingLimit; i++ {
		if i < len(rankings) {
			bf.WriteUint16(uint16(i + 1))
			bf.WriteBytes(stringsupport.PaddedString(rankings[i].Name, 25, true))
			bf.WriteUint32(divaRankingScore(rankings[i].Score))
			continue
		}
		bf.WriteBytes(make([]byte, 31))
	}
}

// writeDivaGuildRankings writes u16 rank, char[25] name, four guild-emblem
// uint32 fields, and u32 score. Emblem data is not stored by the Diva service,
// so those cosmetic fields remain zero without affecting rank/name/score.
func writeDivaGuildRankings(bf *byteframe.ByteFrame, rankings []DivaRankingEntry) {
	for i := 0; i < divaRankingLimit; i++ {
		if i < len(rankings) {
			bf.WriteUint16(uint16(i + 1))
			bf.WriteBytes(stringsupport.PaddedString(rankings[i].Name, 25, true))
			bf.WriteBytes(make([]byte, 16))
			bf.WriteUint32(divaRankingScore(rankings[i].Score))
			continue
		}
		bf.WriteBytes(make([]byte, 47))
	}
}

func findDivaRanking(rankings []DivaRankingEntry, id uint32) (uint32, uint32) {
	for i, entry := range rankings {
		if entry.ID == id {
			return uint32(i + 1), divaRankingScore(entry.Score)
		}
	}
	return 0, 0
}

func handleMsgMhfGetUdRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdRanking)
	bf := byteframe.NewByteFrame()
	eventID, err := getCurrentDivaEventID(s)
	if err != nil || eventID == 0 {
		if err != nil {
			s.logger.Error("Failed to resolve Diva event for ranking", zap.Error(err))
		}
		if pkt.Unk0&1 == 0 {
			writeDivaPersonalRankings(bf, nil)
		} else {
			writeDivaGuildRankings(bf, nil)
		}
		doAckBufSucceed(s, pkt.AckHandle, bf.Data())
		return
	}

	if pkt.Unk0&1 == 0 {
		rankings, rankingErr := s.server.divaRepo.GetPersonalRankings(eventID)
		if rankingErr != nil {
			s.logger.Error("Failed to query Diva personal ranking", zap.Error(rankingErr))
			rankings = nil
		}
		writeDivaPersonalRankings(bf, rankings)
	} else {
		rankings, rankingErr := s.server.divaRepo.GetGuildRankings(eventID)
		if rankingErr != nil {
			s.logger.Error("Failed to query Diva guild ranking", zap.Error(rankingErr))
			rankings = nil
		}
		writeDivaGuildRankings(bf, rankings)
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdMyRanking(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdMyRanking)
	bf := byteframe.NewByteFrame()
	eventID, err := getCurrentDivaEventID(s)
	if err != nil || eventID == 0 {
		bf.WriteBytes(make([]byte, 49))
		doAckBufSucceed(s, pkt.AckHandle, bf.Data())
		return
	}

	personal, personalErr := s.server.divaRepo.GetPersonalRankings(eventID)
	guilds, guildErr := s.server.divaRepo.GetGuildRankings(eventID)
	guildID, membershipErr := s.server.divaRepo.GetCharacterGuildID(s.charID)
	if personalErr != nil || guildErr != nil || membershipErr != nil {
		fields := make([]zap.Field, 0, 3)
		if personalErr != nil {
			fields = append(fields, zap.NamedError("personal", personalErr))
		}
		if guildErr != nil {
			fields = append(fields, zap.NamedError("guild", guildErr))
		}
		if membershipErr != nil {
			fields = append(fields, zap.NamedError("membership", membershipErr))
		}
		s.logger.Error("Failed to query Diva current ranking", fields...)
		bf.WriteBytes(make([]byte, 49))
		doAckBufSucceed(s, pkt.AckHandle, bf.Data())
		return
	}

	personalRank, personalScore := findDivaRanking(personal, s.charID)
	guildRank, guildScore := findDivaRanking(guilds, guildID)
	// The client decodes current/previous/score for personal and guild, followed
	// by the 25-byte guild name. Until ranking snapshots exist, previous equals
	// current, which avoids fabricating a movement indicator.
	bf.WriteUint32(personalRank)
	bf.WriteUint32(personalRank)
	bf.WriteUint32(personalScore)
	bf.WriteUint32(guildRank)
	bf.WriteUint32(guildRank)
	bf.WriteUint32(guildScore)
	guildName := ""
	if guildRank > 0 {
		guildName = guilds[guildRank-1].Name
	}
	bf.WriteBytes(stringsupport.PaddedString(guildName, 25, true))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}
