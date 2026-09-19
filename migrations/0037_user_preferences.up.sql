-- NULL = unanswered (both the intent list and the optional quiz are skippable).
CREATE TABLE user_preferences (
    user_id       uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    intents       text[] NOT NULL DEFAULT '{}',
    group_pref    text CHECK (group_pref    IN ('small','large')),
    energy_pref   text CHECK (energy_pref   IN ('quiet','energetic')),
    planning_pref text CHECK (planning_pref IN ('planned','spontaneous')),
    time_pref     text CHECK (time_pref     IN ('morning','evening')),
    setting_pref  text CHECK (setting_pref  IN ('indoor','outdoor')),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
