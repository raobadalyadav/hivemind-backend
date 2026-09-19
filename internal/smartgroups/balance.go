package smartgroups

import (
	"sort"
	"strings"
)

type Participant struct {
	UserID    string
	Interests []string
}

// BuildGroups sorts participants by their joined interests (so anyone with
// similar interests sorts adjacently) then deals them round-robin across
// ceil(n/targetSize) groups — round-robin, not contiguous slices, is what
// spreads any given interest cluster evenly across groups rather than
// dumping it all into one.
// ponytail: sort-then-deal is O(n log n), not real k-means clustering;
// upgrade to a real clustering pass if group quality complaints show up
// with real data.
func BuildGroups(participants []Participant, targetSize int) [][]Participant {
	if len(participants) == 0 || targetSize <= 0 {
		return nil
	}

	sorted := make([]Participant, len(participants))
	copy(sorted, participants)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.Join(sorted[i].Interests, ",") < strings.Join(sorted[j].Interests, ",")
	})

	n := (len(sorted) + targetSize - 1) / targetSize // ceil
	if n < 1 {
		n = 1
	}
	groups := make([][]Participant, n)
	for i, p := range sorted {
		groups[i%n] = append(groups[i%n], p)
	}
	return groups
}
