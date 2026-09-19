package profiles

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Prefs is flow.md §5 (intent) + §6 (optional quiz). "" = unanswered.
type Prefs struct {
	Intents      []string
	GroupPref    string
	EnergyPref   string
	PlanningPref string
	TimePref     string
	SettingPref  string
}

var validIntents = map[string]bool{
	"make_friends": true, "activity_partners": true, "networking": true, "explore_city": true,
	"new_in_city": true, "travel_companions": true, "join_communities": true, "attend_experiences": true,
}

// quiz maps each personality question to its two allowed answers.
var quiz = map[string][2]string{
	"group":    {"small", "large"},
	"energy":   {"quiet", "energetic"},
	"planning": {"planned", "spontaneous"},
	"time":     {"morning", "evening"},
	"setting":  {"indoor", "outdoor"},
}

func validAnswer(question, v string) bool {
	if v == "" {
		return true
	}
	a := quiz[question]
	return v == a[0] || v == a[1]
}

func (r *Repository) GetPrefs(ctx context.Context, userID string) (*Prefs, error) {
	p := Prefs{Intents: []string{}}
	var g, e, pl, t, s *string
	err := r.pool.QueryRow(ctx, `
		SELECT intents, group_pref, energy_pref, planning_pref, time_pref, setting_pref
		FROM user_preferences WHERE user_id = $1`, userID,
	).Scan(&p.Intents, &g, &e, &pl, &t, &s)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &p, nil
		}
		return nil, err
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	p.GroupPref, p.EnergyPref, p.PlanningPref, p.TimePref, p.SettingPref = deref(g), deref(e), deref(pl), deref(t), deref(s)
	return &p, nil
}

func (r *Repository) SetIntents(ctx context.Context, userID string, intents []string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, intents) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET intents = EXCLUDED.intents, updated_at = now()`, userID, intents)
	return err
}

func (r *Repository) SetPersonality(ctx context.Context, userID string, p *Prefs) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, group_pref, energy_pref, planning_pref, time_pref, setting_pref)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''))
		ON CONFLICT (user_id) DO UPDATE SET group_pref = EXCLUDED.group_pref, energy_pref = EXCLUDED.energy_pref,
			planning_pref = EXCLUDED.planning_pref, time_pref = EXCLUDED.time_pref,
			setting_pref = EXCLUDED.setting_pref, updated_at = now()`,
		userID, p.GroupPref, p.EnergyPref, p.PlanningPref, p.TimePref, p.SettingPref)
	return err
}
