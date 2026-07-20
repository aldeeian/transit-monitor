// Package email abstracts transactional email sending. Production uses
// Resend's HTTP API; development writes .html files to an outbox directory
// so the full pipeline is verifiable without an API key.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Sender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// DevSender writes each email as an .html file into Dir. The filename
// carries the recipient and subject for easy manual inspection.
type DevSender struct {
	Dir string
}

func (d *DevSender) Send(_ context.Context, to, subject, htmlBody string) error {
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d_%s_%s.html",
		time.Now().UnixNano(), sanitize(to), sanitize(subject))
	body := fmt.Sprintf("<!-- to: %s -->\n<!-- subject: %s -->\n%s", to, subject, htmlBody)
	return os.WriteFile(filepath.Join(d.Dir, name), []byte(body), 0o644)
}

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '@' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// ResendSender sends via https://resend.com (free tier: 3k/month, 100/day).
type ResendSender struct {
	APIKey string
	From   string // e.g. "Transit Monitor <onboarding@resend.dev>"
	Client *http.Client
}

func (r *ResendSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	payload, err := json.Marshal(map[string]any{
		"from": r.From, "to": []string{to}, "subject": subject, "html": htmlBody,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("resend returned %d: %s", resp.StatusCode, body)
	}
	return nil
}
