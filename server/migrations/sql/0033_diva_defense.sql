-- Consolidated Diva Defense persistence required by the final server flow.
--
-- This migration intentionally replaces the development migrations 0026-0032.
-- Every schema operation is idempotent so databases stopped at an intermediate
-- development version can safely apply version 33. Version 33 is deliberate:
-- databases that already ran the former version 32 still execute the final
-- item-type normalization and converge on this schema/data state.

-- A character has one current prayer-bead selection. Legacy development builds
-- appended rows because character_id had no unique key; retain the highest ID.
DELETE FROM diva_beads_assignment a
USING diva_beads_assignment b
WHERE a.character_id = b.character_id
  AND a.id < b.id;

CREATE UNIQUE INDEX IF NOT EXISTS diva_beads_assignment_character_key
    ON diva_beads_assignment (character_id);

-- Bead contributions must be scoped to one Diva event and retain the two point
-- components used by the per-day client response.
ALTER TABLE diva_beads_points
    ADD COLUMN IF NOT EXISTS event_id integer REFERENCES events(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS quest_points bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS bonus_points bigint NOT NULL DEFAULT 0;

-- Attribute legacy event-less rows only when that attribution is unambiguous.
WITH single_event AS (
    SELECT min(id) AS id
    FROM events
    WHERE event_type = 'diva'
    HAVING count(*) = 1
)
UPDATE diva_beads_points p
SET event_id = se.id
FROM single_event se
WHERE p.event_id IS NULL;

-- Legacy rows retained only their combined value. Preserve it as quest points,
-- then recover the exact split when one contribution maps unambiguously to the
-- existing character/event aggregate.
UPDATE diva_beads_points
SET quest_points = points,
    bonus_points = 0
WHERE quest_points = 0
  AND bonus_points = 0;

WITH single_contribution AS (
    SELECT character_id, event_id
    FROM diva_beads_points
    WHERE event_id IS NOT NULL
    GROUP BY character_id, event_id
    HAVING count(*) = 1
)
UPDATE diva_beads_points b
SET quest_points = p.quest_points,
    bonus_points = p.bonus_points
FROM diva_points p
JOIN single_contribution s
  ON s.character_id = p.char_id
 AND s.event_id = p.event_id
WHERE b.character_id = p.char_id
  AND b.event_id = p.event_id;

-- Convert legacy selected_at+24h deadlines to the next JST-noon boundary.
UPDATE diva_beads_assignment
SET expiry = (
    date_trunc('day', (expiry AT TIME ZONE 'Asia/Tokyo') - interval '12 hours')
    + interval '12 hours'
) AT TIME ZONE 'Asia/Tokyo';

CREATE INDEX IF NOT EXISTS diva_beads_points_event_char_bead_idx
    ON diva_beads_points (event_id, character_id, bead_index);
CREATE INDEX IF NOT EXISTS diva_beads_points_event_time_idx
    ON diva_beads_points (event_id, timestamp);
CREATE INDEX IF NOT EXISTS diva_beads_points_event_char_time_idx
    ON diva_beads_points (event_id, character_id, timestamp);

-- Event-scoped, idempotent reward receipt state. delivered_at separates an
-- in-progress reservation from a completed client type-7 item grant response.
CREATE TABLE IF NOT EXISTS diva_reward_claims (
    event_id      integer NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    character_id  integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    reward_type   integer NOT NULL,
    reward_key    varchar(64) NOT NULL,
    item_id       integer NOT NULL,
    quantity      integer NOT NULL,
    claimed_at    timestamptz NOT NULL DEFAULT now(),
    delivered_at  timestamptz,
    PRIMARY KEY (event_id, character_id, reward_type, reward_key)
);

ALTER TABLE diva_reward_claims
    ADD COLUMN IF NOT EXISTS delivered_at timestamptz;

CREATE INDEX IF NOT EXISTS diva_reward_claims_character_event_idx
    ON diva_reward_claims (character_id, event_id);
CREATE INDEX IF NOT EXISTS diva_reward_claims_pending_idx
    ON diva_reward_claims (character_id, event_id)
    WHERE delivered_at IS NULL;

-- Keep the interception personal/guild prize master visible in the DB. Type 7
-- is the client code for an ordinary inventory item; type 0 only displays it.
INSERT INTO diva_prizes (type, points_req, item_type, item_id, quantity, gr, repeatable)
SELECT 'personal', 1, 7, 12306, 99, false, false
WHERE NOT EXISTS (SELECT 1 FROM diva_prizes WHERE type = 'personal');

INSERT INTO diva_prizes (type, points_req, item_type, item_id, quantity, gr, repeatable)
SELECT 'guild', 1, 7, 12306, 99, false, false
WHERE NOT EXISTS (SELECT 1 FROM diva_prizes WHERE type = 'guild');

UPDATE diva_prizes
SET item_type = 7,
    item_id = 12306,
    quantity = 99,
    gr = true;
