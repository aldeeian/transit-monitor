package gtfsrt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	gtfs "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

// Verified live download URLs from Open Calgary (Socrata), 2026-07-19.
const (
	DefaultAlertsURL      = "https://data.calgary.ca/download/jhgn-ynqj/application%2Foctet-stream"
	DefaultTripUpdatesURL = "https://data.calgary.ca/download/gs4m-mdc2/application%2Foctet-stream"
	DefaultVehiclesURL    = "https://data.calgary.ca/download/am7c-qe3u/application%2Foctet-stream"
)

// maxFeedBytes caps how much we read from the feed; the alerts feed is ~35KB
// and trip updates ~250KB, so 20MB means something is very wrong upstream.
const maxFeedBytes = 20 << 20

type Fetcher struct {
	URL    string
	Client *http.Client
}

func NewFetcher(url string) *Fetcher {
	return &Fetcher{
		URL: url,
		Client: &http.Client{
			Timeout: 25 * time.Second,
		},
	}
}

// Fetch downloads and decodes one snapshot of the feed. All failures are
// returned as errors — never panics — so the poll loop can log and retry.
func (f *Fetcher) Fetch(ctx context.Context) (*gtfs.FeedMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "transit-monitor/0.1 (Calgary Transit delay monitor)")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("feed exceeds %d byte limit", maxFeedBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("feed returned empty body")
	}

	msg := &gtfs.FeedMessage{}
	if err := proto.Unmarshal(body, msg); err != nil {
		return nil, fmt.Errorf("decode protobuf (%d bytes): %w", len(body), err)
	}
	return msg, nil
}

// FeedTimestamp returns the feed header timestamp, or zero time if absent.
func FeedTimestamp(msg *gtfs.FeedMessage) time.Time {
	if msg.GetHeader() == nil || msg.GetHeader().Timestamp == nil {
		return time.Time{}
	}
	return time.Unix(int64(msg.GetHeader().GetTimestamp()), 0)
}
