package channelserver

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestRepoDivaAssignBeadReplacesCharacterSelection(t *testing.T) {
	repo, db := setupDivaRepo(t)
	userID := CreateTestUser(t, db, "diva_assign_user")
	charID := CreateTestCharacter(t, db, userID, "DivaAssignChar")

	firstExpiry := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	lastExpiry := firstExpiry.Add(4 * time.Hour)
	for beadIndex := 0; beadIndex < 5; beadIndex++ {
		expiry := firstExpiry.Add(time.Duration(beadIndex) * time.Hour)
		if err := repo.AssignBead(charID, beadIndex, expiry); err != nil {
			t.Fatalf("AssignBead(%d) failed: %v", beadIndex, err)
		}
	}

	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_beads_assignment WHERE character_id=$1`, charID); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one assignment after five selections, got %d", count)
	}

	var got struct {
		BeadIndex int       `db:"bead_index"`
		Expiry    time.Time `db:"expiry"`
	}
	if err := db.Get(&got, `SELECT bead_index, expiry FROM diva_beads_assignment WHERE character_id=$1`, charID); err != nil {
		t.Fatalf("get assignment: %v", err)
	}
	if got.BeadIndex != 4 {
		t.Errorf("expected final bead index 4, got %d", got.BeadIndex)
	}
	if !got.Expiry.Equal(lastExpiry) {
		t.Errorf("expected final expiry %s, got %s", lastExpiry, got.Expiry)
	}
	refreshedExpiry := lastExpiry.Add(24 * time.Hour)
	if err := repo.AssignBead(charID, got.BeadIndex, refreshedExpiry); err != nil {
		t.Fatalf("reselect final bead failed: %v", err)
	}
	if err := db.Get(&got, `SELECT bead_index, expiry FROM diva_beads_assignment WHERE character_id=$1`, charID); err != nil {
		t.Fatalf("get refreshed assignment: %v", err)
	}
	if got.BeadIndex != 4 || !got.Expiry.Equal(refreshedExpiry) {
		t.Errorf("expected bead 4 with refreshed expiry %s, got bead %d expiry %s", refreshedExpiry, got.BeadIndex, got.Expiry)
	}

	otherUserID := CreateTestUser(t, db, "diva_assign_other_user")
	otherCharID := CreateTestCharacter(t, db, otherUserID, "DivaAssignOther")
	otherExpiry := firstExpiry.Add(10 * time.Hour)
	if err := repo.AssignBead(otherCharID, 2, otherExpiry); err != nil {
		t.Fatalf("AssignBead for second character failed: %v", err)
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_beads_assignment WHERE character_id IN ($1, $2)`, charID, otherCharID); err != nil {
		t.Fatalf("count both character assignments: %v", err)
	}
	if count != 2 {
		t.Errorf("expected independent rows for two characters, got %d", count)
	}
}

func TestRepoDivaGetAssignedBeadPersistsAfterLockExpiry(t *testing.T) {
	repo, db := setupDivaRepo(t)
	userID := CreateTestUser(t, db, "diva_restore_user")
	charID := CreateTestCharacter(t, db, userID, "DivaRestoreChar")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := repo.AssignBead(charID, 3, now.Add(time.Hour)); err != nil {
		t.Fatalf("AssignBead failed: %v", err)
	}
	got, err := repo.GetAssignedBead(charID, now)
	if err != nil {
		t.Fatalf("GetAssignedBead failed: %v", err)
	}
	if got != 3 {
		t.Fatalf("GetAssignedBead=%d, want 3", got)
	}

	got, err = repo.GetAssignedBead(charID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("GetAssignedBead after lock expiry failed: %v", err)
	}
	if got != 3 {
		t.Fatalf("GetAssignedBead after lock expiry=%d, want persistent bead 3", got)
	}
}

func TestRepoDivaAddBeadPointsStoresEventID(t *testing.T) {
	repo, db := setupDivaRepo(t)
	userID := CreateTestUser(t, db, "diva_bead_points_user")
	charID := CreateTestCharacter(t, db, userID, "DivaBeadPointsChar")

	var eventID uint32
	if err := db.Get(&eventID, `
		INSERT INTO events (event_type, start_time)
		VALUES ('diva', now())
		RETURNING id`); err != nil {
		t.Fatalf("create Diva event: %v", err)
	}
	if err := repo.AddBeadPoints(charID, eventID, 2, 550); err != nil {
		t.Fatalf("AddBeadPoints failed: %v", err)
	}

	var got struct {
		EventID     uint32 `db:"event_id"`
		CharacterID uint32 `db:"character_id"`
		BeadIndex   int    `db:"bead_index"`
		Points      int    `db:"points"`
	}
	if err := db.Get(&got, `
		SELECT event_id, character_id, bead_index, points
		FROM diva_beads_points
		WHERE character_id=$1`, charID); err != nil {
		t.Fatalf("get bead point contribution: %v", err)
	}
	if got.EventID != eventID || got.CharacterID != charID || got.BeadIndex != 2 || got.Points != 550 {
		t.Errorf("stored contribution=(event %d, char %d, bead %d, points %d), want (%d, %d, 2, 550)",
			got.EventID, got.CharacterID, got.BeadIndex, got.Points, eventID, charID)
	}
}

func TestRepoDivaAddPointSubmissionPreservesDailyComponents(t *testing.T) {
	repo, db := setupDivaRepo(t)
	userID := CreateTestUser(t, db, "diva_daily_points_user")
	charID := CreateTestCharacter(t, db, userID, "DivaDailyPointsChar")

	var eventID uint32
	if err := db.Get(&eventID, `
		INSERT INTO events (event_type, start_time)
		VALUES ('diva', now())
		RETURNING id`); err != nil {
		t.Fatalf("create Diva event: %v", err)
	}
	if err := repo.AddPointSubmission(charID, eventID, 252, 120, 2); err != nil {
		t.Fatalf("AddPointSubmission failed: %v", err)
	}

	entries, err := repo.GetCharacterBeadPointEntries(charID, eventID)
	if err != nil {
		t.Fatalf("GetCharacterBeadPointEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count=%d, want 1", len(entries))
	}
	if got := entries[0]; got.BeadIndex != 2 || got.QuestPoints != 252 || got.BonusPoints != 120 {
		t.Errorf("entry=%+v, want bead=2 quest=252 bonus=120", got)
	}
	qp, bp, err := repo.GetPoints(charID, eventID)
	if err != nil {
		t.Fatalf("GetPoints failed: %v", err)
	}
	if qp != 252 || bp != 120 {
		t.Errorf("totals=%d/%d, want 252/120", qp, bp)
	}
}

func setupDivaRepo(t *testing.T) (*DivaRepository, *sqlx.DB) {
	t.Helper()
	db := SetupTestDB(t)
	repo := NewDivaRepository(db)
	t.Cleanup(func() { TeardownTestDB(t, db) })
	return repo, db
}

func TestDivaTallyWindowUsesEventNoonBoundaries(t *testing.T) {
	jst := time.FixedZone("UTC+9", 9*60*60)
	eventStart := time.Date(2026, time.July, 21, 0, 0, 0, 0, jst)

	tests := []struct {
		day       int
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			day:       0,
			wantStart: eventStart,
			wantEnd:   time.Date(2026, time.July, 21, 12, 0, 0, 0, jst),
		},
		{
			day:       1,
			wantStart: time.Date(2026, time.July, 21, 12, 0, 0, 0, jst),
			wantEnd:   time.Date(2026, time.July, 22, 12, 0, 0, 0, jst),
		},
		{
			day:       7,
			wantStart: time.Date(2026, time.July, 27, 12, 0, 0, 0, jst),
			wantEnd:   time.Date(2026, time.July, 28, 12, 0, 0, 0, jst),
		},
	}

	for _, tt := range tests {
		gotStart, gotEnd := divaTallyWindow(eventStart, tt.day)
		if !gotStart.Equal(tt.wantStart) || !gotEnd.Equal(tt.wantEnd) {
			t.Errorf("day %d: got [%s, %s), want [%s, %s)",
				tt.day, gotStart, gotEnd, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestDivaTallyWindowStartsAtEventWhenEventBeginsAfterNoon(t *testing.T) {
	jst := time.FixedZone("UTC+9", 9*60*60)
	eventStart := time.Date(2026, time.July, 21, 14, 30, 0, 0, jst)

	gotStart, gotEnd := divaTallyWindow(eventStart, 0)
	wantEnd := time.Date(2026, time.July, 22, 12, 0, 0, 0, jst)
	if !gotStart.Equal(eventStart) || !gotEnd.Equal(wantEnd) {
		t.Errorf("got [%s, %s), want [%s, %s)", gotStart, gotEnd, eventStart, wantEnd)
	}
}

func TestRepoDivaInsertAndGetEvents(t *testing.T) {
	repo, _ := setupDivaRepo(t)

	if err := repo.InsertEvent(1700000000); err != nil {
		t.Fatalf("InsertEvent failed: %v", err)
	}

	events, err := repo.GetEvents()
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got: %d", len(events))
	}
	if events[0].StartTime != 1700000000 {
		t.Errorf("Expected start_time=1700000000, got: %d", events[0].StartTime)
	}
}

func TestRepoDivaGetEventsEmpty(t *testing.T) {
	repo, _ := setupDivaRepo(t)

	events, err := repo.GetEvents()
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("Expected 0 events, got: %d", len(events))
	}
}

func TestRepoDivaDeleteEvents(t *testing.T) {
	repo, _ := setupDivaRepo(t)

	if err := repo.InsertEvent(1700000000); err != nil {
		t.Fatalf("InsertEvent failed: %v", err)
	}
	if err := repo.InsertEvent(1700100000); err != nil {
		t.Fatalf("InsertEvent failed: %v", err)
	}

	if err := repo.DeleteEvents(); err != nil {
		t.Fatalf("DeleteEvents failed: %v", err)
	}

	events, err := repo.GetEvents()
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("Expected 0 events after delete, got: %d", len(events))
	}
}

func TestRepoDivaMultipleEvents(t *testing.T) {
	repo, _ := setupDivaRepo(t)

	if err := repo.InsertEvent(1700000000); err != nil {
		t.Fatalf("InsertEvent 1 failed: %v", err)
	}
	if err := repo.InsertEvent(1700100000); err != nil {
		t.Fatalf("InsertEvent 2 failed: %v", err)
	}

	events, err := repo.GetEvents()
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("Expected 2 events, got: %d", len(events))
	}
}

func TestRepoDivaDeleteOnlyDivaEvents(t *testing.T) {
	repo, db := setupDivaRepo(t)

	// Insert a diva event
	if err := repo.InsertEvent(1700000000); err != nil {
		t.Fatalf("InsertEvent failed: %v", err)
	}
	// Insert a festa event (should not be deleted)
	if _, err := db.Exec("INSERT INTO events (event_type, start_time) VALUES ('festa', now())"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if err := repo.DeleteEvents(); err != nil {
		t.Fatalf("DeleteEvents failed: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type='festa'").Scan(&count); err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected festa event to survive, got count=%d", count)
	}
}
