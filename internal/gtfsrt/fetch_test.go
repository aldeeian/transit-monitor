package gtfsrt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

func feedBytes(t *testing.T, msg *gtfs.FeedMessage) []byte {
	t.Helper()
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sampleFeed(ts uint64) *gtfs.FeedMessage {
	return &gtfs.FeedMessage{
		Header: &gtfs.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:           proto.Uint64(ts),
		},
		Entity: []*gtfs.FeedEntity{{
			Id:    proto.String("alert-1"),
			Alert: &gtfs.Alert{Effect: gtfs.Alert_DETOUR.Enum()},
		}},
	}
}

func TestNewFetcher(t *testing.T) {
	f := NewFetcher(DefaultAlertsURL)
	if f.URL != DefaultAlertsURL {
		t.Errorf("URL = %q", f.URL)
	}
	if f.Client == nil || f.Client.Timeout == 0 {
		t.Error("want a client with a timeout so a hung feed cannot stall the poll loop")
	}
}

func TestFetchDecodesFeed(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write(feedBytes(t, sampleFeed(1700000000)))
	}))
	defer srv.Close()

	msg, err := NewFetcher(srv.URL).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.GetEntity()) != 1 || msg.GetEntity()[0].GetId() != "alert-1" {
		t.Errorf("entities = %v", msg.GetEntity())
	}
	if !strings.Contains(gotUA, "transit-monitor") {
		t.Errorf("User-Agent = %q, want an identifying agent", gotUA)
	}
}

func TestFetchErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantMsg string
	}{
		{
			name:    "non-200",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			wantMsg: "HTTP 502",
		},
		{
			name:    "empty body",
			handler: func(w http.ResponseWriter, r *http.Request) {},
			wantMsg: "empty body",
		},
		{
			name: "not protobuf",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("<html>upstream error page</html>"))
			},
			wantMsg: "decode protobuf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			_, err := NewFetcher(srv.URL).Fetch(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantMsg)
			}
		})
	}
}

func TestFetchOversizedBody(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-body test in -short mode")
	}
	chunk := make([]byte, 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i <= 20; i++ {
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	_, err := NewFetcher(srv.URL).Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
}

func TestFetchBadURL(t *testing.T) {
	_, err := NewFetcher("://not a url").Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("err = %v, want a request-building error", err)
	}
}

func TestFetchCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(feedBytes(t, sampleFeed(1)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewFetcher(srv.URL).Fetch(ctx); err == nil {
		t.Fatal("want error for a cancelled context")
	}
}

func TestFeedTimestamp(t *testing.T) {
	if got := FeedTimestamp(sampleFeed(1700000000)); !got.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("FeedTimestamp = %v", got)
	}
	noTS := &gtfs.FeedMessage{Header: &gtfs.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")}}
	if got := FeedTimestamp(noTS); !got.IsZero() {
		t.Errorf("missing timestamp = %v, want zero time", got)
	}
	if got := FeedTimestamp(&gtfs.FeedMessage{}); !got.IsZero() {
		t.Errorf("missing header = %v, want zero time", got)
	}
}
