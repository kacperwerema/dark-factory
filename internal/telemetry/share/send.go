package share

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BatchPath is the collector's endpoint, under the configured base URL.
const BatchPath = "/v1/batch"

// Sender posts batches with retry and backoff.
type Sender struct {
	Client *http.Client
	// Backoff is the wait before each retry; nil means 1s, 4s, 16s.
	Backoff []time.Duration
	Sleep   func(time.Duration)
}

func (s Sender) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Endpoint turns the base URL into the batch endpoint. Only http and https
// with a host are accepted.
func Endpoint(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("collector URL %q must be an http(s) URL with a host", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + BatchPath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Send posts one batch. It returns the last HTTP status (0 when no reply)
// and an error unless the collector answered 2xx. A 4xx other than 429 is
// not retried: the collector refused the batch.
func (s Sender) Send(base string, b Batch) (int, error) {
	ep, err := Endpoint(base)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(b)
	if err != nil {
		return 0, err
	}
	if len(body) > MaxBodyBytes {
		return 0, fmt.Errorf("batch is %d bytes, over the %d byte cap", len(body), MaxBodyBytes)
	}
	backoff := s.Backoff
	if backoff == nil {
		backoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}
	}
	sleep := s.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var status int
	var lastErr error
	for attempt := 0; ; attempt++ {
		status, lastErr = s.post(ep, body)
		if lastErr == nil {
			return status, nil
		}
		if status >= 400 && status < 500 && status != http.StatusTooManyRequests {
			return status, lastErr
		}
		if attempt >= len(backoff) {
			return status, lastErr
		}
		d := backoff[attempt]
		if d > 0 {
			d += time.Duration(rand.Int64N(int64(d)/4 + 1))
		}
		sleep(d)
	}
}

func (s Sender) post(ep string, body []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, ep, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 == 2 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("collector answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}
