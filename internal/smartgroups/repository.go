// Package smartgroups implements flow.md §12 "Smart Group Matching" —
// splitting a plan's confirmed participants into balanced subgroups, each
// with its own ephemeral chat room. Every RPC is fully implemented.
package smartgroups

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Group struct {
	ID         string
	PlanID     string
	ChatRoomID string
	Label      string
	MemberIDs  []string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Interests carry trait tokens (energy:quiet, intent:networking, …) after the
// real interests, so the existing sort-then-deal grouping also balances
// personality and intent with no algorithm change.
func (r *Repository) ConfirmedParticipantsWithInterests(ctx context.Context, planID string) ([]Participant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pp.user_id,
			COALESCE(up.interests, '{}')
			|| array_remove(ARRAY['group:'||pr.group_pref, 'energy:'||pr.energy_pref, 'planning:'||pr.planning_pref,
			                      'time:'||pr.time_pref, 'setting:'||pr.setting_pref], NULL)
			|| ARRAY(SELECT 'intent:'||i FROM unnest(COALESCE(pr.intents, '{}')) i)
		FROM plan_participants pp
		LEFT JOIN user_profiles up ON up.user_id = pp.user_id
		LEFT JOIN user_preferences pr ON pr.user_id = pp.user_id
		WHERE pp.plan_id = $1 AND pp.status = 'confirmed'`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Participant
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.UserID, &p.Interests); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// IsConfirmedParticipant satisfies the ListSmartGroups authorization check
// — a caller who isn't the plan's host, an admin, or a confirmed
// participant of the plan itself must not see group membership (which
// users landed in which group), even though the group IDs/labels alone
// aren't sensitive.
func (r *Repository) IsConfirmedParticipant(ctx context.Context, planID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM plan_participants WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed')`,
		planID, userID,
	).Scan(&exists)
	return exists, err
}

// CreateGroup inserts one plan_groups row and its members in one
// transaction.
func (r *Repository) CreateGroup(ctx context.Context, planID, chatRoomID, label string, userIDs []string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var groupID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO plan_groups (plan_id, chat_room_id, label) VALUES ($1, $2, $3) RETURNING id`,
		planID, chatRoomID, label,
	).Scan(&groupID); err != nil {
		return "", err
	}

	for _, userID := range userIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO plan_group_members (plan_group_id, user_id) VALUES ($1, $2)`,
			groupID, userID,
		); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return groupID, nil
}

// ListForPlan returns any groups already generated for this plan — used
// both by ListSmartGroups and by GenerateSmartGroups' idempotency check.
func (r *Repository) ListForPlan(ctx context.Context, planID string) ([]*Group, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT pg.id, pg.chat_room_id, pg.label, pgm.user_id
		FROM plan_groups pg
		LEFT JOIN plan_group_members pgm ON pgm.plan_group_id = pg.id
		WHERE pg.plan_id = $1
		ORDER BY pg.label, pgm.user_id`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]*Group{}
	var order []string
	for rows.Next() {
		var groupID, chatRoomID, label string
		var userID *string
		if err := rows.Scan(&groupID, &chatRoomID, &label, &userID); err != nil {
			return nil, err
		}
		g, ok := byID[groupID]
		if !ok {
			g = &Group{ID: groupID, PlanID: planID, ChatRoomID: chatRoomID, Label: label}
			byID[groupID] = g
			order = append(order, groupID)
		}
		if userID != nil {
			g.MemberIDs = append(g.MemberIDs, *userID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*Group, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}
