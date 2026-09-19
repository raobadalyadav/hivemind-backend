CREATE TYPE message_type AS ENUM ('text','image','voice','poll','announcement','location');

ALTER TABLE messages
    ADD COLUMN type message_type NOT NULL DEFAULT 'text',
    ADD COLUMN meta jsonb NOT NULL DEFAULT '{}';

ALTER TABLE chat_rooms
    ADD COLUMN pinned_message_id uuid REFERENCES messages(id) ON DELETE SET NULL;

CREATE TABLE chat_polls (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id uuid NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
    question   text NOT NULL
);

CREATE TABLE chat_poll_options (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    poll_id  uuid NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
    label    text NOT NULL,
    position integer NOT NULL,
    UNIQUE (poll_id, id)
);

-- One vote per user per poll; the composite FK guarantees the chosen option
-- belongs to that poll, and the PK makes changing a vote a race-free upsert.
CREATE TABLE chat_poll_votes (
    poll_id   uuid NOT NULL,
    user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    option_id uuid NOT NULL,
    voted_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (poll_id, user_id),
    FOREIGN KEY (poll_id, option_id) REFERENCES chat_poll_options (poll_id, id) ON DELETE CASCADE
);
