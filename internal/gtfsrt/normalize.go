package gtfsrt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
)

// Event is a normalized disruption event — the app-level shape that later
// phases persist to Postgres and serve over the API.
type Event struct {
	ID          string     // feed entity ID, stable across polls
	Routes      []string   // affected route IDs (sorted, deduped)
	Stops       []string   // affected stop IDs (sorted, deduped)
	Header      string     // short human-readable summary from the agency
	Description string     // longer detail text, may be empty
	Cause       string     // plain-language cause
	Effect      string     // plain-language effect
	Starts      *time.Time // first active period start, nil if open-ended
	Ends        *time.Time // last active period end, nil if open-ended
	Fingerprint string     // content hash used to detect updates to an alert
}

var causeText = map[gtfs.Alert_Cause]string{
	gtfs.Alert_UNKNOWN_CAUSE:     "unknown cause",
	gtfs.Alert_OTHER_CAUSE:       "other cause",
	gtfs.Alert_TECHNICAL_PROBLEM: "technical problem",
	gtfs.Alert_STRIKE:            "labour action",
	gtfs.Alert_DEMONSTRATION:     "demonstration",
	gtfs.Alert_ACCIDENT:          "accident",
	gtfs.Alert_HOLIDAY:           "holiday service",
	gtfs.Alert_WEATHER:           "weather",
	gtfs.Alert_MAINTENANCE:       "maintenance",
	gtfs.Alert_CONSTRUCTION:      "construction",
	gtfs.Alert_POLICE_ACTIVITY:   "police activity",
	gtfs.Alert_MEDICAL_EMERGENCY: "medical emergency",
}

var effectText = map[gtfs.Alert_Effect]string{
	gtfs.Alert_NO_SERVICE:         "no service",
	gtfs.Alert_REDUCED_SERVICE:    "reduced service",
	gtfs.Alert_SIGNIFICANT_DELAYS: "significant delays",
	gtfs.Alert_DETOUR:             "detour",
	gtfs.Alert_ADDITIONAL_SERVICE: "additional service",
	gtfs.Alert_MODIFIED_SERVICE:   "modified service",
	gtfs.Alert_OTHER_EFFECT:       "service change",
	gtfs.Alert_UNKNOWN_EFFECT:     "service change",
	gtfs.Alert_STOP_MOVED:         "stop moved",
	gtfs.Alert_NO_EFFECT:          "informational",
	gtfs.Alert_ACCESSIBILITY_ISSUE: "accessibility issue",
}

// NormalizeAlerts converts a service-alerts FeedMessage into normalized
// events. Entities without an alert payload are skipped.
func NormalizeAlerts(msg *gtfs.FeedMessage) []Event {
	var events []Event
	for _, entity := range msg.GetEntity() {
		alert := entity.GetAlert()
		if alert == nil {
			continue
		}
		rawDesc := firstTranslation(alert.GetDescriptionText())
		ev := Event{
			ID:          entity.GetId(),
			Header:      firstTranslation(alert.GetHeaderText()),
			Description: stripHTML(rawDesc),
			Cause:       causeText[alert.GetCause()],
			Effect:      effectText[alert.GetEffect()],
		}

		// Calgary quirk: the proto effect field is unset and the real effect
		// is embedded in the description HTML as <div data-effect="DETOUR">.
		if alert.Effect == nil {
			if eff, ok := effectFromHTML(rawDesc); ok {
				ev.Effect = effectText[eff]
			}
		}
		// Calgary quirk: header_text is present but empty; synthesize a
		// headline from the first sentence of the description.
		if ev.Header == "" {
			ev.Header = headline(ev.Description)
		}

		routeSet := map[string]bool{}
		stopSet := map[string]bool{}
		for _, ie := range alert.GetInformedEntity() {
			if r := ie.GetRouteId(); r != "" {
				routeSet[r] = true
			}
			if s := ie.GetStopId(); s != "" {
				stopSet[s] = true
			}
		}
		ev.Routes = sortedKeys(routeSet)
		ev.Stops = sortedKeys(stopSet)

		for _, p := range alert.GetActivePeriod() {
			if p.Start != nil {
				t := time.Unix(int64(p.GetStart()), 0)
				if ev.Starts == nil || t.Before(*ev.Starts) {
					ev.Starts = &t
				}
			}
			if p.End != nil {
				t := time.Unix(int64(p.GetEnd()), 0)
				if ev.Ends == nil || t.After(*ev.Ends) {
					ev.Ends = &t
				}
			}
		}

		ev.Fingerprint = fingerprint(ev)
		events = append(events, ev)
	}
	return events
}

// firstTranslation picks the English translation if present, else the first.
func firstTranslation(ts *gtfs.TranslatedString) string {
	if ts == nil {
		return ""
	}
	trs := ts.GetTranslation()
	for _, tr := range trs {
		if strings.HasPrefix(strings.ToLower(tr.GetLanguage()), "en") {
			return strings.TrimSpace(tr.GetText())
		}
	}
	if len(trs) > 0 {
		return strings.TrimSpace(trs[0].GetText())
	}
	return ""
}

var (
	dataEffectRe = regexp.MustCompile(`data-effect="([A-Z_]+)"`)
	blockEndRe   = regexp.MustCompile(`(?i)</(p|div|ul|ol|h[1-6])>|<br\s*/?>`)
	listItemRe   = regexp.MustCompile(`(?i)<li>`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	blankLinesRe = regexp.MustCompile(`\n{3,}`)
)

// effectFromHTML extracts the effect Calgary embeds as a data attribute in
// the description HTML, e.g. <div data-effect="STOP_MOVED"></div>.
func effectFromHTML(desc string) (gtfs.Alert_Effect, bool) {
	m := dataEffectRe.FindStringSubmatch(desc)
	if m == nil {
		return 0, false
	}
	v, ok := gtfs.Alert_Effect_value[m[1]]
	if !ok {
		return 0, false
	}
	return gtfs.Alert_Effect(v), true
}

// stripHTML converts alert HTML into readable plain text: list items become
// bullets, block boundaries become newlines, entities are unescaped.
func stripHTML(s string) string {
	if s == "" {
		return ""
	}
	s = listItemRe.ReplaceAllString(s, "• ")
	s = blockEndRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// headline returns the first sentence (or line) of plain text, capped so it
// reads as a one-line summary.
func headline(plain string) string {
	line := plain
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if i := strings.Index(line, ". "); i >= 0 && i < 160 {
		return line[:i+1]
	}
	if len(line) > 160 {
		cut := line[:160]
		if j := strings.LastIndexByte(cut, ' '); j > 0 {
			cut = cut[:j]
		}
		return cut + "…"
	}
	return line
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fingerprint hashes the alert content (not the ID) so an agency editing an
// existing alert in place shows up as changed rather than being ignored.
func fingerprint(ev Event) string {
	h := sha256.New()
	fmt.Fprintln(h, ev.Header)
	fmt.Fprintln(h, ev.Description)
	fmt.Fprintln(h, ev.Cause)
	fmt.Fprintln(h, ev.Effect)
	fmt.Fprintln(h, strings.Join(ev.Routes, ","))
	fmt.Fprintln(h, strings.Join(ev.Stops, ","))
	if ev.Starts != nil {
		fmt.Fprintln(h, ev.Starts.Unix())
	}
	if ev.Ends != nil {
		fmt.Fprintln(h, ev.Ends.Unix())
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
