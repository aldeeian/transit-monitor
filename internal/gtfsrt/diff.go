package gtfsrt

// DiffResult classifies one poll's events against the previous poll.
type DiffResult struct {
	New      []Event  // IDs not seen before
	Changed  []Event  // same ID, different content fingerprint
	Resolved []string // IDs present before but gone from this poll
}

// Diff compares the current snapshot against the previous fingerprint map
// (ID -> fingerprint). It is pure so it can be tested without a feed.
func Diff(prev map[string]string, curr []Event) DiffResult {
	var res DiffResult
	seen := make(map[string]bool, len(curr))
	for _, ev := range curr {
		seen[ev.ID] = true
		old, ok := prev[ev.ID]
		switch {
		case !ok:
			res.New = append(res.New, ev)
		case old != ev.Fingerprint:
			res.Changed = append(res.Changed, ev)
		}
	}
	for id := range prev {
		if !seen[id] {
			res.Resolved = append(res.Resolved, id)
		}
	}
	return res
}

// FingerprintMap builds the prev-state map for the next Diff call.
func FingerprintMap(events []Event) map[string]string {
	m := make(map[string]string, len(events))
	for _, ev := range events {
		m[ev.ID] = ev.Fingerprint
	}
	return m
}
