package plans

import (
	"context"
	"errors"
)

var ErrNotParticipant = errors.New("plans: you are not attending this plan")

const maxParticipantCards = 50

type ParticipantCard struct {
	UserID                string
	DisplayName           string
	Occupation            string
	Interests             []string
	PhotoURL              string
	SharedInterestCount   int32
	MutualConnectionCount int32
	SharedCommunityCount  int32
}

type Participants struct {
	Cards                []ParticipantCard
	TotalAttending       int32
	HiddenCount          int32
	ConnectionsAttending int32
}

// shownPredicate is the single definition of "this attendee may appear on a
// card to the caller ($2)": they didn't hide themselves for this plan, didn't
// opt out of previews globally, and there's no block in either direction.
const shownPredicate = `
	pp.visibility = 'visible'
	AND COALESCE(up.show_in_participant_previews, true)
	AND NOT EXISTS (SELECT 1 FROM blocks b
		WHERE (b.user_id = $2::uuid AND b.blocked_user_id = pp.user_id)
		   OR (b.user_id = pp.user_id AND b.blocked_user_id = $2::uuid))`

func (r *Repository) ParticipantCards(ctx context.Context, planID, callerID string) (*Participants, error) {
	out := &Participants{}
	err := r.pool.QueryRow(ctx, `
		WITH my AS (
			SELECT CASE WHEN requester_id = $2::uuid THEN recipient_id ELSE requester_id END AS uid
			FROM connections WHERE status = 'accepted'::connection_status AND $2::uuid IN (requester_id, recipient_id)
		)
		SELECT
			count(*),
			count(*) FILTER (WHERE pp.user_id <> $2::uuid AND NOT (`+shownPredicate+`)),
			count(*) FILTER (WHERE pp.user_id <> $2::uuid AND (`+shownPredicate+`) AND pp.user_id IN (SELECT uid FROM my))
		FROM plan_participants pp
		LEFT JOIN user_profiles up ON up.user_id = pp.user_id
		WHERE pp.plan_id = $1 AND pp.status = 'confirmed'`,
		planID, callerID,
	).Scan(&out.TotalAttending, &out.HiddenCount, &out.ConnectionsAttending)
	if err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx, `
		WITH my AS (
			SELECT CASE WHEN requester_id = $2::uuid THEN recipient_id ELSE requester_id END AS uid
			FROM connections WHERE status = 'accepted'::connection_status AND $2::uuid IN (requester_id, recipient_id)
		)
		SELECT pp.user_id::text, up.display_name, up.occupation, up.interests,
			COALESCE((SELECT url FROM profile_photos ph WHERE ph.user_id = pp.user_id ORDER BY position, created_at LIMIT 1), ''),
			cardinality(ARRAY(SELECT unnest(up.interests) INTERSECT
				SELECT unnest(COALESCE((SELECT interests FROM user_profiles WHERE user_id = $2::uuid), '{}')))),
			(SELECT count(*) FROM connections c
				WHERE c.status = 'accepted'::connection_status AND pp.user_id IN (c.requester_id, c.recipient_id)
				  AND (CASE WHEN c.requester_id = pp.user_id THEN c.recipient_id ELSE c.requester_id END) IN (SELECT uid FROM my)),
			(SELECT count(*) FROM community_members a JOIN community_members b ON b.community_id = a.community_id
				WHERE a.user_id = $2::uuid AND b.user_id = pp.user_id)
		FROM plan_participants pp
		JOIN user_profiles up ON up.user_id = pp.user_id
		WHERE pp.plan_id = $1 AND pp.status = 'confirmed' AND pp.user_id <> $2::uuid AND (`+shownPredicate+`)
		ORDER BY 7 DESC, 6 DESC, pp.joined_at
		LIMIT $3`,
		planID, callerID, maxParticipantCards,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ParticipantCard
		if err := rows.Scan(&c.UserID, &c.DisplayName, &c.Occupation, &c.Interests, &c.PhotoURL,
			&c.SharedInterestCount, &c.MutualConnectionCount, &c.SharedCommunityCount); err != nil {
			return nil, err
		}
		out.Cards = append(out.Cards, c)
	}
	return out, rows.Err()
}

func (r *Repository) SetParticipantVisibility(ctx context.Context, planID, userID string, visible bool) error {
	v := "hidden"
	if visible {
		v = "visible"
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE plan_participants SET visibility = $3 WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed'`,
		planID, userID, v)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotParticipant
	}
	return nil
}

// GetPlanParticipants powers flow.md §11 "who's going" — visible to anyone
// who can see the plan itself, with each attendee filtered by shownPredicate.
func (s *Service) GetPlanParticipants(ctx context.Context, planID, callerID, callerRole string) (*Participants, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.GetPlanAsUser(ctx, planID, callerID, callerRole); err != nil {
		return nil, err
	}
	return s.repo.ParticipantCards(ctx, planID, callerID)
}

func (s *Service) SetParticipantVisibility(ctx context.Context, planID, callerID string, visible bool) error {
	if planID == "" || callerID == "" {
		return ErrInvalidInput
	}
	return s.repo.SetParticipantVisibility(ctx, planID, callerID, visible)
}
