-- Automated moderation (Phase 4): rule-based screening on post/message
-- creation classifies content as safe|review|severe. 'review' auto-creates
-- a case without blocking the write; 'severe' rejects the write outright
-- (never reaches this table). auto_flagged distinguishes system-created
-- cases from human SubmitReport calls in the same queue.
CREATE TYPE case_severity AS ENUM ('safe', 'review', 'severe');

ALTER TABLE moderation_cases ADD COLUMN severity case_severity NOT NULL DEFAULT 'review';
ALTER TABLE moderation_cases ADD COLUMN auto_flagged BOOLEAN NOT NULL DEFAULT false;
