-- Scope Guild Interception contributions to one Diva event. The legacy
-- guild_characters.interception_points JSON is intentionally not imported:
-- it has no event identity, and importing it would make a new event inherit
-- stale reclaimed areas and rankings.
CREATE TABLE IF NOT EXISTS diva_interception_points (
    event_id      integer NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    character_id  integer NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    quest_file_id integer NOT NULL,
    points        bigint NOT NULL DEFAULT 0 CHECK (points >= 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, character_id, quest_file_id)
);

-- The primary-key btree already has (event_id, character_id) as its leading
-- columns, so it also serves event/character reads and event-scoped rankings.
-- A second index on that same prefix would only add storage and write cost.
