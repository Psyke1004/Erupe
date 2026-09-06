package channelserver

import (
	"sort"
	"strconv"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/mhfitem"
	cfg "erupe-ce/config"

	ps "erupe-ce/common/pascalstring"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func handleMsgMhfCreateGuild(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCreateGuild)

	guildId, err := s.server.guildRepo.Create(s.charID, pkt.Name)

	if err != nil {
		bf := byteframe.NewByteFrame()

		// No reasoning behind these values other than they cause a 'failed to create'
		// style message, it's better than nothing for now.
		bf.WriteUint32(0x01010101)

		doAckSimpleFail(s, pkt.AckHandle, bf.Data())
		return
	}

	bf := byteframe.NewByteFrame()

	bf.WriteUint32(uint32(guildId))

	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfArrangeGuildMember(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfArrangeGuildMember)

	guild, err := s.server.guildRepo.GetByID(pkt.GuildID)

	if err != nil || guild == nil {
		s.logger.Error(
			"failed to respond to ArrangeGuildMember message",
			zap.Uint32("charID", s.charID),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if guild.LeaderCharID != s.charID {
		s.logger.Error("non leader attempting to rearrange guild members!",
			zap.Uint32("charID", s.charID),
			zap.Uint32("guildID", guild.ID),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	err = s.server.guildRepo.ArrangeCharacters(pkt.CharIDs)

	if err != nil {
		s.logger.Error(
			"failed to respond to ArrangeGuildMember message",
			zap.Uint32("charID", s.charID),
			zap.Uint32("guildID", guild.ID),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfEnumerateGuildMember(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEnumerateGuildMember)

	var guild *Guild
	var err error

	if pkt.GuildID > 0 {
		guild, err = s.server.guildRepo.GetByID(pkt.GuildID)
	} else {
		guild, err = s.server.guildRepo.GetByCharID(s.charID)
	}

	if guild != nil {
		isApplicant, appErr := s.server.guildRepo.HasApplication(guild.ID, s.charID)
		if appErr != nil {
			s.logger.Warn("Failed to check guild application status", zap.Error(appErr))
		}
		if isApplicant {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 2))
			return
		}
	}

	if guild == nil && s.prevGuildID > 0 {
		guild, err = s.server.guildRepo.GetByID(s.prevGuildID)
	}

	if err != nil {
		s.logger.Warn("failed to retrieve guild sending no result message")
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 2))
		return
	} else if guild == nil {
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 2))
		return
	}

	// Lazy daily RP rollover: move rp_today → rp_yesterday at noon
	midday := TimeMidnight().Add(12 * time.Hour)
	if TimeAdjusted().Before(midday) {
		midday = midday.Add(-24 * time.Hour)
	}
	if guild.RPResetAt.Before(midday) {
		if err := s.server.guildRepo.RolloverDailyRP(guild.ID, midday); err != nil {
			s.logger.Error("Failed to rollover guild daily RP", zap.Error(err))
		}
	}

	guildMembers, err := s.server.guildRepo.GetMembers(guild.ID, false)

	if err != nil {
		s.logger.Error("failed to retrieve guild")
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	alliance, err := s.server.guildRepo.GetAllianceByID(guild.AllianceID)
	if err != nil {
		s.logger.Error("Failed to get alliance data", zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	bf := byteframe.NewByteFrame()

	bf.WriteUint16(uint16(len(guildMembers)))

	sort.Slice(guildMembers[:], func(i, j int) bool {
		return guildMembers[i].OrderIndex < guildMembers[j].OrderIndex
	})

	for _, member := range guildMembers {
		bf.WriteUint32(member.CharID)
		bf.WriteUint16(member.HR)
		if s.server.erupeConfig.RealClientMode >= cfg.G10 {
			bf.WriteUint16(member.GR)
		}
		if s.server.erupeConfig.RealClientMode < cfg.ZZ {
			// Magnet Spike crash workaround
			bf.WriteUint16(0)
		} else {
			bf.WriteUint16(member.WeaponID)
		}
		if member.WeaponType == 1 || member.WeaponType == 5 || member.WeaponType == 10 { // If weapon is ranged
			bf.WriteUint8(7)
		} else {
			bf.WriteUint8(6)
		}
		bf.WriteUint16(member.OrderIndex)
		bf.WriteBool(member.AvoidLeadership)
		ps.Uint8(bf, member.Name, true)
	}

	for _, member := range guildMembers {
		bf.WriteUint32(member.LastLogin)
	}

	if guild.AllianceID > 0 && alliance != nil {
		bf.WriteUint16(alliance.TotalMembers - uint16(len(guildMembers)))
		if guild.ID != alliance.ParentGuildID {
			mems, err := s.server.guildRepo.GetMembers(alliance.ParentGuildID, false)
			if err != nil {
				s.logger.Error("Failed to get parent guild members for alliance", zap.Error(err))
				doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			for _, m := range mems {
				bf.WriteUint32(m.CharID)
			}
		}
		if guild.ID != alliance.SubGuild1ID {
			mems, err := s.server.guildRepo.GetMembers(alliance.SubGuild1ID, false)
			if err != nil {
				s.logger.Error("Failed to get sub guild 1 members for alliance", zap.Error(err))
				doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			for _, m := range mems {
				bf.WriteUint32(m.CharID)
			}
		}
		if guild.ID != alliance.SubGuild2ID {
			mems, err := s.server.guildRepo.GetMembers(alliance.SubGuild2ID, false)
			if err != nil {
				s.logger.Error("Failed to get sub guild 2 members for alliance", zap.Error(err))
				doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
				return
			}
			for _, m := range mems {
				bf.WriteUint32(m.CharID)
			}
		}
	} else {
		bf.WriteUint16(0)
	}

	for _, member := range guildMembers {
		bf.WriteUint16(member.RPToday)
		bf.WriteUint16(member.RPYesterday)
	}

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetGuildManageRight(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGuildManageRight)

	guild, _ := s.server.guildRepo.GetByCharID(s.charID)
	if guild == nil || s.prevGuildID != 0 {
		var err error
		guild, err = s.server.guildRepo.GetByID(s.prevGuildID)
		s.prevGuildID = 0
		if guild == nil || err != nil {
			doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
			return
		}
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(uint32(guild.MemberCount))
	members, err := s.server.guildRepo.GetMembers(guild.ID, false)
	if err != nil {
		s.logger.Error("Failed to get guild members for manage right", zap.Error(err))
		doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	for _, member := range members {
		bf.WriteUint32(member.CharID)
		bf.WriteBool(member.Recruiter)
		bf.WriteBytes(make([]byte, 3))
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfGetUdGuildMapInfo(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetUdGuildMapInfo)
	guild, err := s.server.guildRepo.GetByCharID(s.charID)
	if err != nil || guild == nil {
		s.logger.Warn("Failed to resolve guild for Diva interception map",
			zap.Uint32("charID", s.charID), zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, []byte{1})
		return
	}

	guildScore := uint32(0)
	branchProgress := uint32(0)
	eventID, eventErr := getCurrentDivaEventID(s)
	route := buildDivaInterceptionRoute(eventID, guild.ID)
	if eventErr != nil {
		s.logger.Warn("Failed to resolve current Diva event for interception map", zap.Error(eventErr))
	} else if eventID == 0 {
		s.logger.Warn("No current Diva event for interception map", zap.Uint32("charID", s.charID))
	} else {
		var scoreErr error
		guildScore, scoreErr = s.server.divaRepo.GetInterceptionGuildMapScore(eventID, guild.ID)
		if scoreErr != nil {
			s.logger.Warn("Failed to resolve Diva interception map score", zap.Error(scoreErr))
		}
		points, pointsErr := s.server.divaRepo.GetCharacterInterceptionPoints(s.charID, eventID)
		if pointsErr != nil {
			s.logger.Warn("Failed to resolve Diva interception branch progress", zap.Error(pointsErr))
		} else {
			branchProgress = divaInterceptionBranchProgress(points, route)
		}
	}
	reclaimedAreas := divaInterceptionReclaimedAreas(guildScore, len(route.areaIndexes))
	branchUnlocked := route.branchFromPosition >= 0 && reclaimedAreas > route.branchFromPosition
	bf := buildDivaInterceptionMap(guildScore, branchProgress, route)
	s.logger.Debug("Retrieved Diva interception map",
		zap.Uint32("charID", s.charID),
		zap.Uint32("guildID", guild.ID),
		zap.Uint32("guildScore", guildScore),
		zap.Uint32("branchProgress", branchProgress),
		zap.Int("reclaimedAreas", reclaimedAreas),
		zap.Int("routeAreas", len(route.areaIndexes)),
		zap.Int("branchFromPosition", route.branchFromPosition),
		zap.Int("branchAreas", len(route.branchAreaIndexes)),
		zap.Bool("branchUnlocked", branchUnlocked))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

const divaInterceptionAreaCount = 60
const divaInterceptionMapID = uint32(1)
const divaInterceptionAreaPointRequirement = uint32(1000)
const divaInterceptionSpecialRewardKey = uint32(1)
const divaInterceptionSpecialClass = uint8(2)

// divaInterceptionQuestIDs maps the 60 rendered interception cells to the 60
// real battle quests seeded in EventQuests.sql. The five type-47 branch quests
// (58079-58083, "path to treasure") are deliberately excluded: they describe
// route transitions rather than a monster-base battle and therefore do not
// belong in a selectable map cell.
//
// Keep this in the same order as the type-46/48 seed rows.
var divaInterceptionQuestIDs = [divaInterceptionAreaCount]uint16{
	58043,
	58050, 58051, 58052, 58053, 58054, 58055, 58056, 58057, 58058, 58059,
	58060, 58061, 58062, 58063, 58064, 58065, 58066, 58067, 58068, 58069,
	58070, 58071, 58072, 58074, 58075, 58076, 58077, 58078,
	58088, 58089, 58090, 58091,
	58096, 58097, 58098, 58099,
	58101, 58102, 58103, 58104, 58105, 58106, 58107, 58108, 58109,
	58112, 58113, 58114, 58115,
	58118, 58119, 58120, 58121, 58122, 58123,
	58125, 58126, 58127, 58128,
}

// These type-47 quests are the retail "path to treasure" branch quests. A
// branch endpoint receives one of them instead of borrowing a normal
// interception battle quest.
var divaInterceptionBranchQuestIDs = [...]uint16{58079, 58080, 58081, 58082, 58083}

// divaInterceptionBranchQuestID is shared by the board and the NPC bonus-quest
// list. Keeping the choice in one place prevents a visible treasure endpoint
// from advertising a different quest than the one the NPC offers.
func divaInterceptionBranchQuestID(route divaInterceptionRoute) (uint16, bool) {
	if route.branchFromPosition < 0 || len(route.branchAreaIndexes) == 0 {
		return 0, false
	}
	endpointPosition := len(route.branchAreaIndexes) - 1
	questIndex := (route.branchFromPosition + endpointPosition) % len(divaInterceptionBranchQuestIDs)
	return divaInterceptionBranchQuestIDs[questIndex], true
}

func divaInterceptionBranchProgress(points map[string]int, route divaInterceptionRoute) uint32 {
	questID, ok := divaInterceptionBranchQuestID(route)
	if !ok {
		return 0
	}
	value := points[strconv.Itoa(int(questID))]
	if value <= 0 {
		return 0
	}
	if value >= int(divaInterceptionAreaPointRequirement) {
		return divaInterceptionAreaPointRequirement
	}
	return uint32(value)
}

func divaInterceptionReclaimedAreas(guildScore uint32, areaCount int) int {
	reclaimed := int(guildScore / divaInterceptionAreaPointRequirement)
	if reclaimed > areaCount {
		return areaCount
	}
	return reclaimed
}

// divaInterceptionRoute is generated deterministically from the current event
// and guild. It therefore looks random between events/guilds but never changes
// merely because the board was reopened or the server restarted.
type divaInterceptionRoute struct {
	areaIndexes        []int
	branchFromPosition int
	branchAreaIndexes  []int
}

type divaInterceptionRouteRNG uint32

func (r *divaInterceptionRouteRNG) next() uint32 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = divaInterceptionRouteRNG(x)
	return x
}

func buildDivaInterceptionRoute(eventID, guildID uint32) divaInterceptionRoute {
	seed := eventID*0x9e3779b9 ^ guildID*0x85ebca6b ^ 0xd1b54a35
	if seed == 0 {
		seed = 0x6d2b79f5
	}
	rng := divaInterceptionRouteRNG(seed)

	// Keep the start and goal on the visible upper corners, matching the retail
	// reference. Reserve two horizontal cells after the start so the large
	// hunter-base panel and the frontier actor/progress bar do not overlap during
	// the first few captures.
	areaIndexes := make([]int, 0, 27)
	appendCell := func(row, column int) {
		areaIndexes = append(areaIndexes, row*12+column)
	}
	appendCell(0, 0)
	appendCell(0, 1)
	appendCell(0, 2)
	row := 1
	appendCell(row, 2)
	// Advance exactly one column at a time and make at most one vertical step in
	// each intermediate column. This produces a thin 21-25-cell zig-zag instead
	// of five adjacent horizontal bands, leaving 35-39 true blank cells.
	for column := 3; column <= 11; column++ {
		appendCell(row, column)
		if column == 11 {
			for row > 0 {
				row--
				appendCell(row, column)
			}
			break
		}
		switch row {
		case 0:
			row++
		case 4:
			row--
		default:
			if rng.next()&1 == 0 {
				row--
			} else {
				row++
			}
		}
		appendCell(row, column)
	}

	// Add a short optional spur near the middle of the main route. Its final
	// cell is the treasure panel. Keeping these cells out of areaIndexes is
	// important: optional treasure progress must not advance Acquired Areas or
	// move the ordinary interception frontier.
	used := [divaInterceptionAreaCount]bool{}
	for _, areaIndex := range areaIndexes {
		used[areaIndex] = true
	}
	branchFromPosition := -1
	branchAreaIndexes := make([]int, 0, 2)
	center := len(areaIndexes) / 2
	for distance := 0; distance < len(areaIndexes) && branchFromPosition < 0; distance++ {
		positions := [2]int{center - distance, center + distance}
		for _, position := range positions {
			if position < 4 || position >= len(areaIndexes)-3 {
				continue
			}
			origin := areaIndexes[position]
			row, column := origin/12, origin%12
			rowDirections := [2]int{-1, 1}
			if rng.next()&1 != 0 {
				rowDirections = [2]int{1, -1}
			}
			columnDirection := 1
			if column == 11 {
				columnDirection = -1
			}
			for _, rowDirection := range rowDirections {
				branchRow := row + rowDirection
				branchColumn := column + columnDirection
				if branchRow < 0 || branchRow >= 5 || branchColumn < 0 || branchColumn >= 12 {
					continue
				}
				first := branchRow*12 + column
				second := branchRow*12 + branchColumn
				if used[first] || used[second] {
					continue
				}
				branchFromPosition = position
				branchAreaIndexes = append(branchAreaIndexes, first, second)
				break
			}
			if branchFromPosition >= 0 {
				break
			}
		}
	}
	return divaInterceptionRoute{
		areaIndexes:        areaIndexes,
		branchFromPosition: branchFromPosition,
		branchAreaIndexes:  branchAreaIndexes,
	}
}

// divaInterceptionAreaID maps the client's 5x12 board coordinate to its retail
// decimal area ID. The renderer reverses this as row=5-(id/100), col=id%100-1;
// sequential IDs 1..60 therefore address row 5 outside the board.
func divaInterceptionAreaID(index int) uint16 {
	row := index / 12
	column := index % 12
	return uint16((5-row)*100 + column + 1)
}

func writeDivaInterceptionMapArea(
	bf *byteframe.ByteFrame,
	areaID uint16,
	state uint8,
	progress uint32,
	rewardKey uint32,
) {
	bf.WriteUint16(areaID)
	bf.WriteUint16(0)
	bf.WriteUint16(0)
	bf.WriteUint16(0)
	bf.WriteUint16(0)
	bf.WriteUint16(0)
	bf.WriteUint8(0)
	bf.WriteUint8(state)
	bf.WriteUint32(progress)
	bf.WriteUint8(0)
	bf.WriteUint32(rewardKey)
}

func writeDivaInterceptionSpecialRewardDefinition(bf *byteframe.ByteFrame, rewardKey uint32, class uint8) {
	// The DX9 decoder expands this 14-byte wire record to 16 bytes. Client
	// helpers match key and inclusive topology-header range, then classify the
	// panel from the final byte. Class 2 is the treasure-panel family. Clan and
	// monster bases are raw map states 1 and 2, not class-1 reward overlays.
	bf.WriteUint32(rewardKey)
	bf.WriteUint8(divaRewardItemType)
	bf.WriteUint16(divaRewardItemID)
	bf.WriteUint16(divaRewardQuantity)
	bf.WriteUint16(1) // inclusive topology-header minimum
	bf.WriteUint16(1) // inclusive topology-header maximum
	bf.WriteUint8(class)
}

func writeDivaInterceptionTopologyArea(
	bf *byteframe.ByteFrame,
	areaID uint16,
	routeKey uint16,
	nextAreaID uint16,
	questIDs [3]uint16,
	progress uint32,
	plainRenderOrder uint8,
) {
	// The route decoder stores these as the current and required point values.
	bf.WriteUint32(progress)
	bf.WriteUint32(divaInterceptionAreaPointRequirement)
	// The first ID matches this topology row to its rendered map area. Static
	// analysis of the DX9 client proves that routeKey and nextAreaID are copied
	// into the visible 40-byte board record. A completed cell whose routeKey is
	// its own area ID exposes nextAreaID as its immediate reachable successor.
	bf.WriteUint16(areaID)
	bf.WriteUint16(routeKey)
	bf.WriteUint16(nextAreaID)
	// The selected-cell panel resolves these through the client's quest title
	// catalogue. They are quest IDs; monster IDs happen to resolve to unrelated
	// normal-quest titles such as "Hunter Basics".
	bf.WriteUint16(questIDs[0])
	bf.WriteUint16(questIDs[1])
	bf.WriteUint16(questIDs[2])
	// The first trailing byte is the one-based render slot for a topology row
	// without quest IDs. The DX9 client derives slots for quest-bearing rows
	// itself, placing them after every plain row. Leaving all plain slots zero
	// collapses them into the sentinel entry before the visible pointer array;
	// frontier selection then stays at (-1,-1), which is drawn as the detached
	// lower-right outline, and the contact actors never follow progress.
	bf.WriteUint8(plainRenderOrder)
	bf.WriteUint8(0)
	bf.WriteUint8(0)
}

// divaInterceptionMapRecordOrder preserves the fixed base record first, then
// promotes an unlocked branch approach ahead of ordinary coordinate order.
// The DX9 board builder places cells by their serialized area ID, so changing
// the wire-record order does not move any rendered hex. Its local available-
// quest walker, however, scans the raw 64-record array in wire order and stops
// after ten reachable successors. Without this promotion, a long reclaimed
// main route can fill that limit before the branch approach is examined.
func divaInterceptionMapRecordOrder(route divaInterceptionRoute, branchUnlocked bool) []int {
	order := make([]int, 0, divaInterceptionAreaCount)
	promoted := [divaInterceptionAreaCount]bool{}
	order = append(order, 0)
	promoted[0] = true
	if branchUnlocked {
		for position, areaIndex := range route.branchAreaIndexes {
			if position == len(route.branchAreaIndexes)-1 {
				break // the final branch cell is the neutral treasure endpoint
			}
			if areaIndex < 0 || areaIndex >= divaInterceptionAreaCount || promoted[areaIndex] {
				continue
			}
			order = append(order, areaIndex)
			promoted[areaIndex] = true
		}
	}
	for areaIndex := 1; areaIndex < divaInterceptionAreaCount; areaIndex++ {
		if promoted[areaIndex] {
			continue
		}
		order = append(order, areaIndex)
	}
	return order
}

// buildDivaInterceptionMap emits the wire structure consumed by the ZZ Diva
// map UI. It contains one 60-cell 5x12 board plus the four mandatory records
// in the client's fixed 64-record map array, and one matching topology record.
// Omitting the topology or using IDs outside the decimal coordinate range
// leaves the renderer unable to initialize its board. Do not attach a fixed
// decoder address here: mhfo.dll and mhfo-hd.dll use different layouts and the
// address also changes between client builds.
func buildDivaInterceptionMap(guildScore, branchProgress uint32, route divaInterceptionRoute) *byteframe.ByteFrame {
	bf := byteframe.NewByteFrame()
	reclaimed := divaInterceptionReclaimedAreas(guildScore, len(route.areaIndexes))
	currentProgress := guildScore % divaInterceptionAreaPointRequirement
	// Once the route is complete there is no successor row that can represent
	// the hunter/monster contact. If every row is emitted as completed, the
	// client fails to find a plain active topology row and falls back to the Clan
	// Base at the start of the board. Keep the terminal Monster Base as that
	// active row and cycle its gauge with any points earned after arrival.
	routeComplete := len(route.areaIndexes) > 0 && reclaimed == len(route.areaIndexes)
	terminalPosition := len(route.areaIndexes) - 1
	branchUnlocked := route.branchFromPosition >= 0 && reclaimed > route.branchFromPosition
	if branchProgress > divaInterceptionAreaPointRequirement {
		branchProgress = divaInterceptionAreaPointRequirement
	}
	branchComplete := branchUnlocked && branchProgress >= divaInterceptionAreaPointRequirement
	positions := [divaInterceptionAreaCount]int{}
	for i := range positions {
		positions[i] = -1
	}
	for position, areaIndex := range route.areaIndexes {
		positions[areaIndex] = position
	}
	branchPositions := [divaInterceptionAreaCount]int{}
	for i := range branchPositions {
		branchPositions[i] = -1
	}
	for position, areaIndex := range route.branchAreaIndexes {
		branchPositions[areaIndex] = position
	}
	bf.WriteUint8(0) // status: success
	bf.WriteUint8(1) // one current map
	// Both header IDs participate in the client's map/topology lineage lookup;
	// they are map IDs rather than the database guild ID.
	bf.WriteUint32(divaInterceptionMapID)
	bf.WriteUint32(divaInterceptionMapID)
	for _, i := range divaInterceptionMapRecordOrder(route, branchUnlocked) {
		position := positions[i]
		branchPosition := branchPositions[i]
		// See docs/diva-interception-map.md before changing these values. Live
		// comparison proves state 1 is the fixed Clan Base, state 2 is the
		// uncaptured Monster Base, and state 3 is reclaimed Clan Territory. The
		// moving contact is resolved from topology progress/render order, so the
		// active map cell remains state 0.
		state := uint8(0)
		if position == 0 {
			state = 1
		} else if routeComplete && position == terminalPosition {
			state = 2
		} else if position > 0 && position < reclaimed {
			state = 3
		} else if position == len(route.areaIndexes)-1 && position >= reclaimed && reclaimed < len(route.areaIndexes) {
			state = 2
		}
		// Once the main route has reclaimed the branch origin, expose the short
		// approach to the treasure endpoint as reclaimed territory. The endpoint
		// itself changes to reclaimed only after its branch quest has supplied the
		// required points.
		if branchUnlocked && branchPosition >= 0 && branchPosition < len(route.branchAreaIndexes)-1 {
			state = 3
		} else if branchComplete && branchPosition == len(route.branchAreaIndexes)-1 {
			state = 3
		}
		progress := uint32(0)
		if routeComplete && position == terminalPosition {
			progress = currentProgress
		} else if position >= 0 && position < reclaimed {
			// The map record and its topology row are two independent progress
			// inputs to the client renderer. Leaving completed map progress at
			// zero keeps the hunter/monster contact presentation pinned to an
			// already reclaimed cell even though topology has advanced.
			progress = divaInterceptionAreaPointRequirement
		} else if position == reclaimed && reclaimed < len(route.areaIndexes) {
			progress = currentProgress
		}
		if branchUnlocked && branchPosition >= 0 && branchPosition < len(route.branchAreaIndexes)-1 {
			progress = divaInterceptionAreaPointRequirement
		} else if branchUnlocked && branchPosition == len(route.branchAreaIndexes)-1 {
			progress = branchProgress
		}
		rewardKey := uint32(0)
		if branchPosition >= 0 && branchPosition == len(route.branchAreaIndexes)-1 {
			rewardKey = divaInterceptionSpecialRewardKey
		}
		writeDivaInterceptionMapArea(bf, divaInterceptionAreaID(i), state, progress, rewardKey)
	}
	// The decoder always consumes 64 map records; the last four are padding.
	var emptyMapRecord [23]byte
	for i := divaInterceptionAreaCount; i < 64; i++ {
		bf.WriteBytes(emptyMapRecord[:])
	}
	bf.WriteUint16(1)
	writeDivaInterceptionSpecialRewardDefinition(
		bf,
		divaInterceptionSpecialRewardKey,
		divaInterceptionSpecialClass,
	)
	bf.WriteUint8(1) // one topology matching the current map
	bf.WriteUint32(divaInterceptionMapID)
	bf.WriteUint16(1)
	bf.WriteUint8(uint8(len(route.areaIndexes) + len(route.branchAreaIndexes)))
	plainRenderOrder := uint8(1)
	for position, areaIndex := range route.areaIndexes {
		progress := uint32(0)
		if routeComplete && position == terminalPosition {
			progress = currentProgress
		} else if position < reclaimed {
			// State 3 needs a completed point value to render the visible Clan
			// Territory icon.
			progress = divaInterceptionAreaPointRequirement
		} else if position == reclaimed && reclaimed < len(route.areaIndexes) {
			progress = currentProgress
		}
		nextAreaID := uint16(0)
		if position+1 < len(route.areaIndexes) {
			nextAreaID = divaInterceptionAreaID(route.areaIndexes[position+1])
		}
		areaID := divaInterceptionAreaID(areaIndex)
		questIDs := [3]uint16{}
		if position < reclaimed && !(routeComplete && position == terminalPosition) {
			// Captured cells expose their battle quest. The active contact must stay
			// a plain row so it receives runtime render slot 1 and supplies the
			// contact actors/bar with its partial progress. The actual interception
			// quest list is delivered independently from this board metadata.
			// Ordinary future cells also use zero quest IDs for the yellow enemy-path
			// presentation. The future bonus cell is the one exception: class-2 special presentation
			// and a yellow base cell both map to client state 7, which hides the
			// chest. Giving only that cell quest metadata changes its base display
			// state while leaving the class-2 chest overlay independently visible.
			questIDs[0] = divaInterceptionQuestIDs[position]
		}
		renderOrder := uint8(0)
		if questIDs == [3]uint16{} {
			renderOrder = plainRenderOrder
			plainRenderOrder++
		}
		writeDivaInterceptionTopologyArea(
			bf,
			areaID,
			areaID,
			nextAreaID,
			questIDs,
			progress,
			renderOrder,
		)
	}
	for position, areaIndex := range route.branchAreaIndexes {
		routeKey := divaInterceptionAreaID(route.areaIndexes[route.branchFromPosition])
		if position > 0 {
			routeKey = divaInterceptionAreaID(route.branchAreaIndexes[position-1])
		}
		nextAreaID := uint16(0)
		if position+1 < len(route.branchAreaIndexes) {
			nextAreaID = divaInterceptionAreaID(route.branchAreaIndexes[position+1])
		}
		questIDs := [3]uint16{}
		if position == len(route.branchAreaIndexes)-1 && !branchComplete {
			if questID, ok := divaInterceptionBranchQuestID(route); ok {
				questIDs[0] = questID
			}
		}
		renderOrder := uint8(0)
		if questIDs == [3]uint16{} {
			renderOrder = plainRenderOrder
			plainRenderOrder++
		}
		// The client builds the Branch Route quest list locally. Static analysis
		// of mhfo-hd.dll 0x103B99C0 shows that it only follows a reclaimed map
		// cell when the successor topology row has progress >= required_points;
		// it then reads the quest IDs from that successor. Mark every branch
		// topology row reachable after the main-route origin is reclaimed, while
		// leaving the endpoint map record at zero for future branch-quest points.
		progress := uint32(0)
		if branchUnlocked {
			progress = divaInterceptionAreaPointRequirement
		}
		writeDivaInterceptionTopologyArea(
			bf,
			divaInterceptionAreaID(areaIndex),
			routeKey,
			nextAreaID,
			questIDs,
			progress,
			renderOrder,
		)
	}
	// Displayed directly by the client as Acquired/Reclaimed Areas.
	bf.WriteUint32(uint32(reclaimed))
	return bf
}

func handleMsgMhfGetGuildTargetMemberNum(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGetGuildTargetMemberNum)

	var guild *Guild
	var err error

	if pkt.GuildID == 0x0 {
		guild, err = s.server.guildRepo.GetByCharID(s.charID)
	} else {
		guild, err = s.server.guildRepo.GetByID(pkt.GuildID)
	}

	if err != nil || guild == nil {
		doAckBufSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x02})
		return
	}

	bf := byteframe.NewByteFrame()

	bf.WriteUint16(0x0)
	bf.WriteUint16(guild.MemberCount - 1)

	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfEnumerateGuildItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEnumerateGuildItem)
	items := guildGetItems(s, pkt.GuildID)
	bf := byteframe.NewByteFrame()
	bf.WriteBytes(mhfitem.SerializeWarehouseItems(items))
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfUpdateGuildItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfUpdateGuildItem)
	newStacks := mhfitem.DiffItemStacks(guildGetItems(s, pkt.GuildID), pkt.UpdatedItems)
	if err := s.server.guildRepo.SaveItemBox(pkt.GuildID, mhfitem.SerializeWarehouseItems(newStacks)); err != nil {
		s.logger.Error("Failed to update guild item box", zap.Error(err))
	}
	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfUpdateGuildIcon(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfUpdateGuildIcon)

	guild, err := s.server.guildRepo.GetByID(pkt.GuildID)

	if err != nil || guild == nil {
		s.logger.Error("Failed to get guild info for icon update", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	characterInfo, err := s.server.guildRepo.GetCharacterMembership(s.charID)

	if err != nil || characterInfo == nil {
		s.logger.Error("Failed to get character guild data for icon update", zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if !characterInfo.IsSubLeader() && !characterInfo.IsLeader {
		s.logger.Warn(
			"character without leadership attempting to update guild icon",
			zap.Uint32("guildID", guild.ID),
			zap.Uint32("charID", s.charID),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	icon := &GuildIcon{}

	icon.Parts = make([]GuildIconPart, len(pkt.IconParts))

	for i, p := range pkt.IconParts {
		icon.Parts[i] = GuildIconPart{
			Index:    p.Index,
			ID:       p.ID,
			Page:     p.Page,
			Size:     p.Size,
			Rotation: p.Rotation,
			Red:      p.Red,
			Green:    p.Green,
			Blue:     p.Blue,
			PosX:     p.PosX,
			PosY:     p.PosY,
		}
	}

	guild.Icon = icon

	err = s.server.guildRepo.Save(guild)

	if err != nil {
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfReadGuildcard(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfReadGuildcard)

	resp := byteframe.NewByteFrame()
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)
	resp.WriteUint32(0)

	doAckBufSucceed(s, pkt.AckHandle, resp.Data())
}

func handleMsgMhfEntryRookieGuild(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEntryRookieGuild)

	// pkt.Unk==0: fresh rookie entering a rookie guild (return_type=1).
	// pkt.Unk>=1: returning player entering a comeback/return guild (return_type=2).
	returnType := uint8(1)
	nameTemplate := s.I18n().guild.rookieGuildName
	if pkt.Unk >= 1 {
		returnType = 2
		nameTemplate = s.I18n().guild.returnGuildName
	}

	guildID, err := s.server.guildRepo.FindOrCreateReturnGuild(returnType, nameTemplate)
	if err != nil {
		s.logger.Error("failed to find/create return guild",
			zap.Uint32("charID", s.charID),
			zap.Error(err),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	if err := s.server.guildRepo.AddMember(guildID, s.charID); err != nil {
		s.logger.Error("failed to add character to return guild",
			zap.Uint32("charID", s.charID),
			zap.Uint32("guildID", guildID),
			zap.Error(err),
		)
		doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}

	bf := byteframe.NewByteFrame()
	bf.WriteUint32(guildID)
	doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
}

func handleMsgMhfUpdateForceGuildRank(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgMhfGenerateUdGuildMap(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfGenerateUdGuildMap)
	guild, err := s.server.guildRepo.GetByCharID(s.charID)
	if err != nil || guild == nil {
		s.logger.Warn("Failed to generate Diva interception map",
			zap.Uint32("charID", s.charID), zap.Error(err))
		doAckSimpleFail(s, pkt.AckHandle, []byte{1})
		return
	}
	// The board is deterministic and reconstructed by GetUdGuildMapInfo. The
	// generation command only needs to confirm that the requesting guild has a
	// valid map identity; no speculative blob is persisted.
	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0})
}

func handleMsgMhfUpdateGuild(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

func handleMsgMhfSetGuildManageRight(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfSetGuildManageRight)
	if err := s.server.guildRepo.SetRecruiter(pkt.CharID, pkt.Allowed); err != nil {
		s.logger.Error("Failed to update guild manage right", zap.Error(err))
	}
	doAckBufSucceed(s, pkt.AckHandle, make([]byte, 4))
}

// monthlyTypeString maps the packet's Type field to the DB column prefix.
func monthlyTypeString(t uint8) string {
	switch t {
	case 0:
		return "monthly"
	case 1:
		return "monthly_hl"
	case 2:
		return "monthly_ex"
	default:
		return ""
	}
}

func handleMsgMhfCheckMonthlyItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfCheckMonthlyItem)

	typeStr := monthlyTypeString(pkt.Type)
	if typeStr == "" {
		doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
		return
	}

	claimed, err := s.server.stampRepo.GetMonthlyClaimed(s.charID, typeStr)
	if err != nil || claimed.Before(TimeMonthStart()) {
		doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x00})
		return
	}

	doAckSimpleSucceed(s, pkt.AckHandle, []byte{0x00, 0x00, 0x00, 0x01})
}

func handleMsgMhfAcquireMonthlyItem(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfAcquireMonthlyItem)

	typeStr := monthlyTypeString(pkt.Unk0)
	if typeStr != "" {
		if err := s.server.stampRepo.SetMonthlyClaimed(s.charID, typeStr, TimeAdjusted()); err != nil {
			s.logger.Error("Failed to set monthly item claimed", zap.Error(err))
		}
	}

	doAckSimpleSucceed(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfEnumerateInvGuild(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfEnumerateInvGuild)
	stubEnumerateNoResults(s, pkt.AckHandle)
}

func handleMsgMhfOperationInvGuild(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgMhfOperationInvGuild)
	doAckSimpleFail(s, pkt.AckHandle, make([]byte, 4))
}

func handleMsgMhfUpdateGuildcard(s *Session, p mhfpacket.MHFPacket) {} // stub: unimplemented

// guildGetItems reads and parses the guild item box.
func guildGetItems(s *Session, guildID uint32) []mhfitem.MHFItemStack {
	data, err := s.server.guildRepo.GetItemBox(guildID)
	if err != nil {
		s.logger.Error("Failed to get guild item box", zap.Error(err))
		return nil
	}
	var items []mhfitem.MHFItemStack
	if len(data) > 0 {
		box := byteframe.NewByteFrameFromBytes(data)
		numStacks := box.ReadUint16()
		box.ReadUint16() // Unused
		for i := 0; i < int(numStacks); i++ {
			items = append(items, mhfitem.ReadWarehouseItem(box))
		}
	}
	return items
}
