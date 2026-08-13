package channelserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// DivaRepository centralizes all database access for diva defense events.
type DivaRepository struct {
	db *sqlx.DB
}

// NewDivaRepository creates a new DivaRepository.
func NewDivaRepository(db *sqlx.DB) *DivaRepository {
	return &DivaRepository{db: db}
}

// DeleteEvents removes all diva events.
func (r *DivaRepository) DeleteEvents() error {
	_, err := r.db.Exec("DELETE FROM events WHERE event_type='diva'")
	return err
}

// InsertEvent creates a new diva event with the given start epoch.
func (r *DivaRepository) InsertEvent(startEpoch uint32) error {
	_, err := r.db.Exec("INSERT INTO events (event_type, start_time) VALUES ('diva', to_timestamp($1)::timestamp without time zone)", startEpoch)
	return err
}

// DivaEvent represents a diva event row with ID and start_time epoch.
type DivaEvent struct {
	ID        uint32 `db:"id"`
	StartTime uint32 `db:"start_time"`
}

// GetEvents returns all diva events with their ID and start_time epoch.
func (r *DivaRepository) GetEvents() ([]DivaEvent, error) {
	var result []DivaEvent
	err := r.db.Select(&result, "SELECT id, (EXTRACT(epoch FROM start_time)::int) as start_time FROM events WHERE event_type='diva' ORDER BY start_time, id")
	return result, err
}

// AddPointSubmission atomically records both the character totals and the
// selected prayer bead contribution produced by one client submission.
func (r *DivaRepository) AddPointSubmission(charID, eventID, questPoints, bonusPoints uint32, beadIndex int) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.Exec(`
		INSERT INTO diva_points (char_id, event_id, quest_points, bonus_points, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (char_id, event_id) DO UPDATE
		SET quest_points = diva_points.quest_points + EXCLUDED.quest_points,
		    bonus_points = diva_points.bonus_points + EXCLUDED.bonus_points,
		    updated_at = now()`, charID, eventID, questPoints, bonusPoints); err != nil {
		return err
	}

	if beadIndex >= 0 {
		total := uint64(questPoints) + uint64(bonusPoints)
		if total > 0 {
			if _, err = tx.Exec(`
				INSERT INTO diva_beads_points
				    (character_id, event_id, bead_index, points, quest_points, bonus_points)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				charID, eventID, beadIndex, total, questPoints, bonusPoints); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// DivaBeadPointEntry is one quest contribution with the original ordinary and
// bonus components retained for the client's per-day, per-bead display.
type DivaBeadPointEntry struct {
	BeadIndex   int       `db:"bead_index"`
	QuestPoints int64     `db:"quest_points"`
	BonusPoints int64     `db:"bonus_points"`
	Timestamp   time.Time `db:"timestamp"`
}

// GetCharacterBeadPointEntries returns event-scoped contribution history in
// chronological order for construction of the eight GetUdMyPoint day slots.
func (r *DivaRepository) GetCharacterBeadPointEntries(characterID, eventID uint32) ([]DivaBeadPointEntry, error) {
	var entries []DivaBeadPointEntry
	err := r.db.Select(&entries, `
		SELECT bead_index, quest_points, bonus_points, timestamp
		FROM diva_beads_points
		WHERE character_id=$1 AND event_id=$2
		ORDER BY timestamp, id`, characterID, eventID)
	return entries, err
}

// AddPoints atomically adds quest and bonus points for a character in a diva event.
func (r *DivaRepository) AddPoints(charID, eventID, questPoints, bonusPoints uint32) error {
	_, err := r.db.Exec(`
		INSERT INTO diva_points (char_id, event_id, quest_points, bonus_points, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (char_id, event_id) DO UPDATE
		SET quest_points = diva_points.quest_points + EXCLUDED.quest_points,
		    bonus_points = diva_points.bonus_points + EXCLUDED.bonus_points,
		    updated_at = now()`,
		charID, eventID, questPoints, bonusPoints)
	return err
}

// GetPoints returns the accumulated quest and bonus points for a character in an event.
func (r *DivaRepository) GetPoints(charID, eventID uint32) (int64, int64, error) {
	var qp, bp int64
	err := r.db.QueryRow(
		"SELECT COALESCE(SUM(quest_points),0), COALESCE(SUM(bonus_points),0) FROM diva_points WHERE char_id=$1 AND event_id=$2",
		charID, eventID).Scan(&qp, &bp)
	if err != nil {
		return 0, 0, err
	}
	return qp, bp, nil
}

// GetTotalPoints returns the sum of all players' quest and bonus points for an event.
func (r *DivaRepository) GetTotalPoints(eventID uint32) (int64, int64, error) {
	var qp, bp int64
	err := r.db.QueryRow(
		"SELECT COALESCE(SUM(quest_points),0), COALESCE(SUM(bonus_points),0) FROM diva_points WHERE event_id=$1",
		eventID).Scan(&qp, &bp)
	if err != nil {
		return 0, 0, err
	}
	return qp, bp, nil
}

// GetPersonalRankings returns all contributors for one Diva event in stable
// score order. Until Premium score is implemented, stored Song Spheres are the
// ranking score as well.
func (r *DivaRepository) GetPersonalRankings(eventID uint32) ([]DivaRankingEntry, error) {
	var result []DivaRankingEntry
	err := r.db.Select(&result, `
		SELECT dp.char_id AS id, c.name,
		       (dp.quest_points + dp.bonus_points)::bigint AS score
		FROM diva_points dp
		JOIN characters c ON c.id = dp.char_id
		WHERE dp.event_id = $1
		  AND dp.quest_points + dp.bonus_points > 0
		ORDER BY score DESC, dp.char_id ASC`, eventID)
	return result, err
}

// GetGuildRankings aggregates the same event-scoped score by current guild.
func (r *DivaRepository) GetGuildRankings(eventID uint32) ([]DivaRankingEntry, error) {
	var result []DivaRankingEntry
	err := r.db.Select(&result, `
		SELECT g.id, g.name,
		       SUM(dp.quest_points + dp.bonus_points)::bigint AS score
		FROM diva_points dp
		JOIN guild_characters gc ON gc.character_id = dp.char_id
		JOIN guilds g ON g.id = gc.guild_id
		WHERE dp.event_id = $1
		GROUP BY g.id, g.name
		HAVING SUM(dp.quest_points + dp.bonus_points) > 0
		ORDER BY score DESC, g.id ASC`, eventID)
	return result, err
}

// GetCharacterGuildID returns zero when the character is not in a guild.
func (r *DivaRepository) GetCharacterGuildID(characterID uint32) (uint32, error) {
	var guildID uint32
	err := r.db.QueryRow(
		"SELECT guild_id FROM guild_characters WHERE character_id=$1",
		characterID).Scan(&guildID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return guildID, err
}

// GetParticipationDays returns zero-based prayer days with a positive recorded
// contribution for this character and event.
func (r *DivaRepository) GetParticipationDays(characterID, eventID uint32, eventStart time.Time) ([]int, error) {
	entries, err := r.GetCharacterBeadPointEntries(characterID, eventID)
	if err != nil {
		return nil, err
	}
	days := make([]int, 0, 7)
	for day := 0; day < 7; day++ {
		start, end := divaTallyWindow(eventStart, day)
		for _, entry := range entries {
			if entry.QuestPoints+entry.BonusPoints > 0 && !entry.Timestamp.Before(start) && entry.Timestamp.Before(end) {
				days = append(days, day)
				break
			}
		}
	}
	return days, nil
}

// TryClaimReward reserves one reward key. The unique primary key makes repeat
// client requests and concurrent sessions idempotent.
func (r *DivaRepository) TryClaimReward(eventID, characterID uint32, rewardType uint8, rewardKey string, itemID, quantity uint32) (bool, error) {
	result, err := r.db.Exec(`
		INSERT INTO diva_reward_claims
		    (event_id, character_id, reward_type, reward_key, item_id, quantity)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id, character_id, reward_type, reward_key) DO UPDATE
		SET item_id=EXCLUDED.item_id, quantity=EXCLUDED.quantity
		WHERE diva_reward_claims.delivered_at IS NULL`, eventID, characterID, rewardType, rewardKey, itemID, quantity)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// MarkRewardDelivered distinguishes a completed gift-box write from a pending
// reservation. Pending rows can be retried after an interrupted/ineffective
// warehouse update without paying completed claims twice.
func (r *DivaRepository) MarkRewardDelivered(eventID, characterID uint32, rewardType uint8, rewardKey string) error {
	result, err := r.db.Exec(`
		UPDATE diva_reward_claims SET delivered_at=now()
		WHERE event_id=$1 AND character_id=$2 AND reward_type=$3 AND reward_key=$4
		  AND delivered_at IS NULL`, eventID, characterID, rewardType, rewardKey)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("Diva reward delivery marker updated %d rows", rows)
	}
	return nil
}

// ReleaseRewardClaim permits a retry if delivery to the gift box failed.
func (r *DivaRepository) ReleaseRewardClaim(eventID, characterID uint32, rewardType uint8, rewardKey string) error {
	_, err := r.db.Exec(`
		DELETE FROM diva_reward_claims
		WHERE event_id=$1 AND character_id=$2 AND reward_type=$3 AND reward_key=$4`,
		eventID, characterID, rewardType, rewardKey)
	return err
}

// GetBeads returns all active bead types from the diva_beads table.
func (r *DivaRepository) GetBeads() ([]int, error) {
	var types []int
	err := r.db.Select(&types, "SELECT type FROM diva_beads ORDER BY id")
	return types, err
}

// AssignBead inserts or replaces the active bead assignment for a character.
func (r *DivaRepository) AssignBead(characterID uint32, beadIndex int, expiry time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO diva_beads_assignment (character_id, bead_index, expiry)
		VALUES ($1, $2, $3)
		ON CONFLICT (character_id) DO UPDATE
		SET bead_index = EXCLUDED.bead_index,
		    expiry = EXCLUDED.expiry`,
		characterID, beadIndex, expiry)
	return err
}

// GetAssignedBead returns the character's selected prayer bead.
//
// The expiry column is the next-noon selection-lock boundary: it determines
// when the client may change its selection, not when the selected bead stops
// receiving Song Spheres.  A selection therefore remains active across daily
// tally boundaries until it is replaced (or the event cleanup removes it).
func (r *DivaRepository) GetAssignedBead(characterID uint32, now time.Time) (int, error) {
	var beadIndex int
	err := r.db.QueryRow(`
		SELECT bead_index
		FROM diva_beads_assignment
		WHERE character_id = $1`,
		characterID).Scan(&beadIndex)
	return beadIndex, err
}

// AddBeadPoints records a bead point contribution for a character in an event.
func (r *DivaRepository) AddBeadPoints(characterID, eventID uint32, beadIndex int, points int) error {
	_, err := r.db.Exec(
		"INSERT INTO diva_beads_points (character_id, event_id, bead_index, points) VALUES ($1, $2, $3, $4)",
		characterID, eventID, beadIndex, points)
	return err
}

// GetCharacterBeadPoints returns the summed points per bead_index for a character.
func (r *DivaRepository) GetCharacterBeadPoints(characterID, eventID uint32) (map[int]int, error) {
	rows, err := r.db.Query(
		"SELECT bead_index, COALESCE(SUM(points),0) FROM diva_beads_points WHERE character_id=$1 AND event_id=$2 GROUP BY bead_index",
		characterID, eventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[int]int)
	for rows.Next() {
		var idx, pts int
		if err := rows.Scan(&idx, &pts); err != nil {
			return nil, err
		}
		result[idx] = pts
	}
	return result, rows.Err()
}

// GetTotalBeadPoints returns the sum of all points across all characters and bead slots.
func (r *DivaRepository) GetTotalBeadPoints(eventID uint32) (int64, error) {
	var total int64
	err := r.db.QueryRow("SELECT COALESCE(SUM(points),0) FROM diva_beads_points WHERE event_id=$1", eventID).Scan(&total)
	return total, err
}

// divaTallyWindow returns the contribution window for a zero-based Diva result
// slot. The first result is tallied at noon on the event's first day, so its
// window starts with the event itself. Later results cover noon-to-noon days.
func divaTallyWindow(eventStart time.Time, day int) (time.Time, time.Time) {
	eventStart = eventStart.In(TimeAdjusted().Location())
	y, m, d := eventStart.Date()
	firstNoon := time.Date(y, m, d, 12, 0, 0, 0, eventStart.Location())
	if !eventStart.Before(firstNoon) {
		firstNoon = firstNoon.Add(24 * time.Hour)
	}

	end := firstNoon.Add(time.Duration(day) * 24 * time.Hour)
	if day == 0 {
		return eventStart, end
	}
	return end.Add(-24 * time.Hour), end
}

// GetTopBeadPerDay returns the bead_index with the most points contributed in
// the requested event result slot. Returns 0 if no data exists for that slot.
func (r *DivaRepository) GetTopBeadPerDay(eventID uint32, eventStart time.Time, day int) (int, error) {
	start, end := divaTallyWindow(eventStart, day)
	var beadIndex int
	err := r.db.QueryRow(`
		SELECT bead_index
		FROM diva_beads_points
		WHERE event_id = $1
		  AND timestamp >= $2
		  AND timestamp <  $3
		GROUP BY bead_index
		ORDER BY SUM(points) DESC, bead_index ASC
		LIMIT 1`,
		eventID, start, end).Scan(&beadIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return beadIndex, nil
}

// CleanupBeads deletes all rows from diva_beads, diva_beads_assignment, and diva_beads_points.
func (r *DivaRepository) CleanupBeads() error {
	if _, err := r.db.Exec("DELETE FROM diva_beads_points"); err != nil {
		return err
	}
	if _, err := r.db.Exec("DELETE FROM diva_beads_assignment"); err != nil {
		return err
	}
	_, err := r.db.Exec("DELETE FROM diva_beads")
	return err
}

// GetPersonalPrizes returns all prize rows with type='personal', ordered by points_req.
func (r *DivaRepository) GetPersonalPrizes() ([]DivaPrize, error) {
	return r.getPrizesByType("personal")
}

// GetGuildPrizes returns all prize rows with type='guild', ordered by points_req.
func (r *DivaRepository) GetGuildPrizes() ([]DivaPrize, error) {
	return r.getPrizesByType("guild")
}

func (r *DivaRepository) getPrizesByType(prizeType string) ([]DivaPrize, error) {
	rows, err := r.db.Query(`
		SELECT id, type, points_req, item_type, item_id, quantity, gr, repeatable
		FROM diva_prizes
		WHERE type=$1
		ORDER BY points_req`,
		prizeType)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var prizes []DivaPrize
	for rows.Next() {
		var p DivaPrize
		if err := rows.Scan(&p.ID, &p.Type, &p.PointsReq, &p.ItemType, &p.ItemID, &p.Quantity, &p.GR, &p.Repeatable); err != nil {
			return nil, err
		}
		prizes = append(prizes, p)
	}
	return prizes, rows.Err()
}

// GetCharacterInterceptionPoints returns the interception_points JSON map from guild_characters.
func (r *DivaRepository) GetCharacterInterceptionPoints(characterID uint32) (map[string]int, error) {
	var raw []byte
	err := r.db.QueryRow(
		"SELECT interception_points FROM guild_characters WHERE character_id=$1",
		characterID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make(map[string]int)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// AddInterceptionPoints increments the interception points for a quest file ID in guild_characters.
func (r *DivaRepository) AddInterceptionPoints(characterID uint32, questFileID int, points int) error {
	result, err := r.db.Exec(`
		UPDATE guild_characters
		SET interception_points = interception_points || jsonb_build_object(
			$2::text,
			COALESCE((interception_points->>$2::text)::int, 0) + $3
		)
		WHERE character_id=$1`,
		characterID, questFileID, points)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("Diva interception point update matched %d guild memberships for character %d", rows, characterID)
	}
	return nil
}

// GetInterceptionGuildRankings aggregates the second-week interception score
// map across every current member of each guild. Unlike the first-week Diva
// ranking, these points live in guild_characters.interception_points and are
// not scoped by diva_points.event_id.
func (r *DivaRepository) GetInterceptionGuildRankings() ([]DivaRankingEntry, error) {
	var result []DivaRankingEntry
	err := r.db.Select(&result, `
		SELECT g.id, g.name, SUM(point.value::bigint)::bigint AS score
		FROM guild_characters gc
		JOIN guilds g ON g.id = gc.guild_id
		JOIN LATERAL jsonb_each_text(
			COALESCE(gc.interception_points, '{}'::jsonb)
		) AS point(key, value) ON TRUE
		GROUP BY g.id, g.name
		HAVING SUM(point.value::bigint) > 0
		ORDER BY score DESC, g.id ASC`)
	return result, err
}
