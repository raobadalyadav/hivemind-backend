package profiles

import (
	"context"
	"strings"
)

const (
	MinInterests = 5
	MaxInterests = 15
)

func (r *Repository) InterestCatalog(ctx context.Context) (interests, categories []string, err error) {
	for _, q := range []struct {
		sql string
		dst *[]string
	}{
		{`SELECT name FROM interests ORDER BY name`, &interests},
		{`SELECT name FROM categories ORDER BY name`, &categories},
	} {
		rows, qerr := r.pool.Query(ctx, q.sql)
		if qerr != nil {
			return nil, nil, qerr
		}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return nil, nil, err
			}
			*q.dst = append(*q.dst, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return interests, categories, nil
}

// NormalizeInterests validates a selection against the catalog (case-
// insensitive) and returns it in catalog casing — recommendation matches
// interests to category names with ILIKE, so stored casing must be canonical.
func (r *Repository) NormalizeInterests(ctx context.Context, in []string) ([]string, error) {
	lower := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		l := strings.ToLower(strings.TrimSpace(s))
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		lower = append(lower, l)
	}
	if len(lower) < MinInterests || len(lower) > MaxInterests {
		return nil, ErrInvalidInput
	}
	rows, err := r.pool.Query(ctx, `SELECT name FROM interests WHERE lower(name) = ANY($1) ORDER BY name`, lower)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(lower) { // something wasn't in the catalog
		return nil, ErrInvalidInput
	}
	return out, nil
}
