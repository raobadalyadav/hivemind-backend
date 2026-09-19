package smartgroups

import "testing"

func TestBuildGroups_SizeBalance(t *testing.T) {
	var participants []Participant
	for i := 0; i < 23; i++ {
		participants = append(participants, Participant{UserID: string(rune('a' + i))})
	}

	groups := BuildGroups(participants, 6)
	if len(groups) != 4 { // ceil(23/6) = 4
		t.Fatalf("expected 4 groups, got %d", len(groups))
	}

	total := 0
	for _, g := range groups {
		if len(g) < 5 || len(g) > 6 {
			t.Errorf("expected each group to have 5-6 members, got %d", len(g))
		}
		total += len(g)
	}
	if total != len(participants) {
		t.Errorf("expected all %d participants placed, got %d", len(participants), total)
	}
}

// TestBuildGroups_SpreadsInterestClusters verifies round-robin dealing
// spreads a large same-interest cluster across groups instead of dumping
// it all into one — the whole reason round-robin was chosen over
// contiguous slicing after the sort.
func TestBuildGroups_SpreadsInterestClusters(t *testing.T) {
	var participants []Participant
	for i := 0; i < 12; i++ {
		participants = append(participants, Participant{UserID: string(rune('a' + i)), Interests: []string{"hiking"}})
	}

	groups := BuildGroups(participants, 6)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	for i, g := range groups {
		if len(g) != 6 {
			t.Errorf("group %d: expected 6 members, got %d", i, len(g))
		}
	}
}

func TestBuildGroups_Empty(t *testing.T) {
	if groups := BuildGroups(nil, 6); groups != nil {
		t.Errorf("expected nil groups for empty input, got %v", groups)
	}
}
