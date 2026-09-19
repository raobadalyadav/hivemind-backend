package moderation

// DeriveBadges applies fixed thresholds to TrustSignals and returns badge
// strings only — never a number, per PRD §22's explicit rule against
// exposing a raw risk score to end users.
// ponytail: fixed thresholds, not learned/weighted; revisit if
// false-positive complaints show up with real data.
func DeriveBadges(sig *TrustSignals) []string {
	var badges []string
	if sig.AttendedCount >= 5 && sig.NoShowCount == 0 {
		badges = append(badges, "reliable_attendee")
	}
	if sig.HostAvgRating >= 4.5 && sig.AttendedCount >= 3 {
		badges = append(badges, "trusted_host")
	}
	if sig.VerificationTier == "id" {
		badges = append(badges, "id_verified")
	}
	if sig.TenureDays >= 180 {
		badges = append(badges, "community_veteran")
	}
	if sig.ReportCount >= 3 {
		badges = append(badges, "under_review")
	}
	return badges
}
