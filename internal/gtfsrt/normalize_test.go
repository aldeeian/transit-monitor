package gtfsrt

import (
	"strings"
	"testing"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

func alertEntity(id string, mutate func(*gtfs.Alert)) *gtfs.FeedEntity {
	a := &gtfs.Alert{
		Cause:  gtfs.Alert_CONSTRUCTION.Enum(),
		Effect: gtfs.Alert_DETOUR.Enum(),
		HeaderText: &gtfs.TranslatedString{Translation: []*gtfs.TranslatedString_Translation{
			{Text: proto.String("Route 3 detour via 17 Ave"), Language: proto.String("en")},
		}},
		InformedEntity: []*gtfs.EntitySelector{
			{RouteId: proto.String("3")},
			{StopId: proto.String("5501")},
			{RouteId: proto.String("1")},
			{RouteId: proto.String("3")}, // duplicate on purpose
		},
		ActivePeriod: []*gtfs.TimeRange{
			{Start: proto.Uint64(1752900000), End: proto.Uint64(1752990000)},
		},
	}
	if mutate != nil {
		mutate(a)
	}
	return &gtfs.FeedEntity{Id: proto.String(id), Alert: a}
}

func feedWith(entities ...*gtfs.FeedEntity) *gtfs.FeedMessage {
	return &gtfs.FeedMessage{
		Header: &gtfs.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:           proto.Uint64(uint64(time.Now().Unix())),
		},
		Entity: entities,
	}
}

func TestNormalizeAlerts(t *testing.T) {
	events := NormalizeAlerts(feedWith(alertEntity("a1", nil)))
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.ID != "a1" {
		t.Errorf("ID = %q", ev.ID)
	}
	if ev.Header != "Route 3 detour via 17 Ave" {
		t.Errorf("Header = %q", ev.Header)
	}
	if ev.Effect != "detour" || ev.Cause != "construction" {
		t.Errorf("Effect/Cause = %q/%q", ev.Effect, ev.Cause)
	}
	// Routes must be deduped and sorted.
	if len(ev.Routes) != 2 || ev.Routes[0] != "1" || ev.Routes[1] != "3" {
		t.Errorf("Routes = %v, want [1 3]", ev.Routes)
	}
	if len(ev.Stops) != 1 || ev.Stops[0] != "5501" {
		t.Errorf("Stops = %v", ev.Stops)
	}
	if ev.Starts == nil || ev.Starts.Unix() != 1752900000 {
		t.Errorf("Starts = %v", ev.Starts)
	}
	if ev.Ends == nil || ev.Ends.Unix() != 1752990000 {
		t.Errorf("Ends = %v", ev.Ends)
	}
	if ev.Fingerprint == "" {
		t.Error("Fingerprint empty")
	}
}

func TestNormalizeSkipsNonAlertEntities(t *testing.T) {
	tripEntity := &gtfs.FeedEntity{
		Id:         proto.String("t1"),
		TripUpdate: &gtfs.TripUpdate{Trip: &gtfs.TripDescriptor{TripId: proto.String("x")}},
	}
	events := NormalizeAlerts(feedWith(tripEntity, alertEntity("a1", nil)))
	if len(events) != 1 || events[0].ID != "a1" {
		t.Fatalf("want only a1, got %+v", events)
	}
}

func TestNormalizeEmptyAndNilFieldsDoNotPanic(t *testing.T) {
	bare := &gtfs.FeedEntity{Id: proto.String("bare"), Alert: &gtfs.Alert{}}
	events := NormalizeAlerts(feedWith(bare))
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Starts != nil || ev.Ends != nil || len(ev.Routes) != 0 || len(ev.Stops) != 0 {
		t.Errorf("bare alert should have empty fields: %+v", ev)
	}
}

func TestFingerprintStableAcrossPolls(t *testing.T) {
	a := NormalizeAlerts(feedWith(alertEntity("a1", nil)))[0]
	b := NormalizeAlerts(feedWith(alertEntity("a1", nil)))[0]
	if a.Fingerprint != b.Fingerprint {
		t.Error("identical alerts produced different fingerprints")
	}
}

func TestFingerprintChangesWhenContentChanges(t *testing.T) {
	a := NormalizeAlerts(feedWith(alertEntity("a1", nil)))[0]
	b := NormalizeAlerts(feedWith(alertEntity("a1", func(al *gtfs.Alert) {
		al.HeaderText.Translation[0].Text = proto.String("Detour extended to 26 Ave")
	})))[0]
	if a.Fingerprint == b.Fingerprint {
		t.Error("edited alert kept the same fingerprint")
	}
}

func TestEnglishTranslationPreferred(t *testing.T) {
	ev := NormalizeAlerts(feedWith(alertEntity("a1", func(al *gtfs.Alert) {
		al.HeaderText = &gtfs.TranslatedString{Translation: []*gtfs.TranslatedString_Translation{
			{Text: proto.String("Détour"), Language: proto.String("fr")},
			{Text: proto.String("Detour"), Language: proto.String("en-CA")},
		}}
	})))[0]
	if ev.Header != "Detour" {
		t.Errorf("Header = %q, want English translation", ev.Header)
	}
}

func TestDiff(t *testing.T) {
	orig := NormalizeAlerts(feedWith(alertEntity("a1", nil), alertEntity("a2", nil)))
	prev := FingerprintMap(orig)

	// a1 unchanged, a2 edited, a3 new, and a former alert a0 resolved.
	prev["a0"] = "deadbeef00000000"
	curr := NormalizeAlerts(feedWith(
		alertEntity("a1", nil),
		alertEntity("a2", func(al *gtfs.Alert) { al.Effect = gtfs.Alert_NO_SERVICE.Enum() }),
		alertEntity("a3", nil),
	))

	d := Diff(prev, curr)
	if len(d.New) != 1 || d.New[0].ID != "a3" {
		t.Errorf("New = %+v, want [a3]", ids(d.New))
	}
	if len(d.Changed) != 1 || d.Changed[0].ID != "a2" {
		t.Errorf("Changed = %+v, want [a2]", ids(d.Changed))
	}
	if len(d.Resolved) != 1 || d.Resolved[0] != "a0" {
		t.Errorf("Resolved = %v, want [a0]", d.Resolved)
	}
}

func TestDiffIdempotentOnRepoll(t *testing.T) {
	curr := NormalizeAlerts(feedWith(alertEntity("a1", nil)))
	prev := FingerprintMap(curr)
	d := Diff(prev, curr)
	if len(d.New)+len(d.Changed)+len(d.Resolved) != 0 {
		t.Errorf("re-polling identical feed produced changes: %+v", d)
	}
}

// calgaryStyleAlert mirrors what the live feed actually sends: empty header,
// HTML description with the effect in a data attribute, no proto effect set.
func calgaryStyleAlert(id string) *gtfs.FeedEntity {
	return &gtfs.FeedEntity{
		Id: proto.String(id),
		Alert: &gtfs.Alert{
			HeaderText: &gtfs.TranslatedString{Translation: []*gtfs.TranslatedString_Translation{
				{Text: proto.String(""), Language: proto.String("en")},
			}},
			DescriptionText: &gtfs.TranslatedString{Translation: []*gtfs.TranslatedString_Translation{
				{Text: proto.String("<div data-effect=\"DETOUR\"></div>\n<p>Routes 38, 43, and 57 will be detoured. More detail here.</p>\n<p>The following stops will be temporarily closed:</p>\n<ul>\n  <li>Whitehorn LRT Station (#6253) - Route 43</li>\n  <li>Whitehorn Stn Parking (#2451) &amp; more</li>\n</ul>"), Language: proto.String("en")},
			}},
			InformedEntity: []*gtfs.EntitySelector{
				{RouteId: proto.String("38"), StopId: proto.String("2451")},
			},
		},
	}
}

func TestCalgaryEffectExtractedFromHTML(t *testing.T) {
	ev := NormalizeAlerts(feedWith(calgaryStyleAlert("c1")))[0]
	if ev.Effect != "detour" {
		t.Errorf("Effect = %q, want detour (from data-effect attr)", ev.Effect)
	}
}

func TestProtoEffectWinsOverHTML(t *testing.T) {
	e := calgaryStyleAlert("c1")
	e.Alert.Effect = gtfs.Alert_NO_SERVICE.Enum()
	ev := NormalizeAlerts(feedWith(e))[0]
	if ev.Effect != "no service" {
		t.Errorf("Effect = %q, want explicit proto effect to win", ev.Effect)
	}
}

func TestCalgaryHTMLStrippedAndHeadlineSynthesized(t *testing.T) {
	ev := NormalizeAlerts(feedWith(calgaryStyleAlert("c1")))[0]
	if strings.Contains(ev.Description, "<") {
		t.Errorf("Description still contains HTML: %q", ev.Description)
	}
	if !strings.Contains(ev.Description, "• Whitehorn LRT Station (#6253) - Route 43") {
		t.Errorf("list items not bulleted: %q", ev.Description)
	}
	if !strings.Contains(ev.Description, "& more") {
		t.Errorf("entities not unescaped: %q", ev.Description)
	}
	if ev.Header != "Routes 38, 43, and 57 will be detoured." {
		t.Errorf("Header = %q, want first sentence of description", ev.Header)
	}
}

func TestHeadlineTruncatesLongFirstSentence(t *testing.T) {
	long := strings.Repeat("word ", 60) // ~300 chars, no period
	h := headline(long)
	if len(h) > 170 {
		t.Errorf("headline too long: %d chars", len(h))
	}
	if !strings.HasSuffix(h, "…") {
		t.Errorf("truncated headline should end with ellipsis: %q", h)
	}
}

func ids(evs []Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.ID
	}
	return out
}
