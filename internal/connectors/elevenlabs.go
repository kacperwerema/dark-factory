package connectors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A meter: elevenlabs connector is ElevenLabs' text-to-speech, called by
// issue-cli itself: the key from its headers: is read into this process
// only, the characters billed are known from the call, and the answer's
// audio and character timings are written where the caller asked.

// DefaultElevenLabsURL is the API's base when the connector has no url:.
const DefaultElevenLabsURL = "https://api.elevenlabs.io"

// TTSTimeout is how long one text-to-speech call may take.
const TTSTimeout = 3 * time.Minute

// TTSMaxBody is the most of an answer read (the audio is in it, base64).
const TTSMaxBody = 64 << 20

// TTSRequest is one text-to-speech call.
type TTSRequest struct {
	BaseURL string
	Header  http.Header
	VoiceID string
	Model   string
	Text    string
}

// TTSResult is what the call returned: the MP3, the character alignment as
// ElevenLabs sends it (characters, character_start_times_seconds,
// character_end_times_seconds) and the characters billed.
type TTSResult struct {
	Audio      []byte
	Alignment  json.RawMessage
	Characters int
	// CostKnown is a Characters the answer's character-cost header gave,
	// not the text's count.
	CostKnown bool
}

// TTSClient follows no redirect, so the key's request can't be sent on.
func TTSClient() *http.Client {
	return &http.Client{
		Timeout:       TTSTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TTS posts r to <base>/v1/text-to-speech/<voice>/with-timestamps. The
// characters billed are the answer's character-cost header when it has
// one, else the text's characters. An error never repeats the key.
func TTS(ctx context.Context, hc *http.Client, r TTSRequest) (TTSResult, error) {
	base := strings.TrimRight(strings.TrimSpace(r.BaseURL), "/")
	if base == "" {
		base = DefaultElevenLabsURL
	}
	body := map[string]string{"text": r.Text}
	if r.Model != "" {
		body["model_id"] = r.Model
	}
	b, _ := json.Marshal(body)
	u := base + "/v1/text-to-speech/" + url.PathEscape(r.VoiceID) + "/with-timestamps?output_format=mp3_44100_128"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return TTSResult{}, fmt.Errorf("text-to-speech: %v", err)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return TTSResult{}, fmt.Errorf("text-to-speech: %s", restTransportWords(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, TTSMaxBody+1))
	if err != nil {
		return TTSResult{}, fmt.Errorf("text-to-speech: reading the answer: %s", restTransportWords(err))
	}
	if len(raw) > TTSMaxBody {
		return TTSResult{}, fmt.Errorf("text-to-speech: the answer is over %d MB", TTSMaxBody>>20)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(scrubSecrets(raw, r.Header)))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return TTSResult{}, fmt.Errorf("text-to-speech: ElevenLabs answered %d: %s", resp.StatusCode, msg)
	}
	var ans struct {
		Audio     string          `json:"audio_base64"`
		Alignment json.RawMessage `json:"alignment"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return TTSResult{}, fmt.Errorf("text-to-speech: the answer is not ElevenLabs' with-timestamps JSON")
	}
	audio, err := base64.StdEncoding.DecodeString(ans.Audio)
	if err != nil || len(audio) == 0 {
		return TTSResult{}, fmt.Errorf("text-to-speech: the answer has no audio")
	}
	res := TTSResult{Audio: audio, Alignment: ans.Alignment, Characters: utf8.RuneCountInString(r.Text)}
	for _, h := range []string{"character-cost", "x-character-count"} {
		if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get(h))); err == nil && n >= 0 {
			res.Characters, res.CostKnown = n, true
			break
		}
	}
	return res, nil
}

// ElevenLabsMaxJSON is the most of a JSON answer read (a subscription, a
// page of shared voices).
const ElevenLabsMaxJSON = 4 << 20

// MusicCreditsPerMinute is ElevenLabs' published price of music, in the
// credits its subscription counts as character_count (pricing page,
// 2026-11): what a music call is estimated at before it is sent.
const MusicCreditsPerMinute = 900

// Music lengths the API takes: 3 s to the 5 minutes the capabilities page
// states (the API itself accepts up to 10).
const (
	MusicMinMS = 3000
	MusicMaxMS = 300000
)

// MusicEstimate is a music call's estimated credits: whole credits, up.
func MusicEstimate(lengthMS int) int {
	return (lengthMS*MusicCreditsPerMinute + 59999) / 60000
}

// elevenCall makes one call with the key's header and returns the answer's
// status, headers and body (at most max bytes). An error never repeats
// the key.
func elevenCall(ctx context.Context, hc *http.Client, method, u string, header http.Header, body []byte, accept string, max int64) (int, http.Header, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", accept)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, nil, errors.New(restTransportWords(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("reading the answer: %s", restTransportWords(err))
	}
	if int64(len(raw)) > max {
		return 0, nil, nil, fmt.Errorf("the answer is over %d MB", max>>20)
	}
	return resp.StatusCode, resp.Header, raw, nil
}

// elevenError is a non-2xx answer in a line, the key scrubbed out.
func elevenError(what string, status int, raw []byte, header http.Header) error {
	msg := strings.TrimSpace(string(scrubSecrets(raw, header)))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return &ElevenLabsError{What: what, Status: status, Msg: msg}
}

// ElevenLabsError is an answer that wasn't a 2xx.
type ElevenLabsError struct {
	What   string
	Status int
	Msg    string
}

func (e *ElevenLabsError) Error() string {
	return fmt.Sprintf("%s: ElevenLabs answered %d: %s", e.What, e.Status, e.Msg)
}

// Refused is an answer that refused the key (401 or 403).
func (e *ElevenLabsError) Refused() bool { return e.Status == 401 || e.Status == 403 }

func elevenBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return DefaultElevenLabsURL
	}
	return base
}

// Subscription is the part of GET /v1/user/subscription a check prints and
// a music call reads: the plan, and the credits (which the API names
// characters) used and allowed this period.
type Subscription struct {
	Tier      string `json:"tier"`
	Status    string `json:"status"`
	Used      int    `json:"character_count"`
	Limit     int    `json:"character_limit"`
	ResetUnix int64  `json:"next_character_count_reset_unix"`
}

// Free is a plan with no paid subscription: the Music API is for paid
// plans, and free-plan music isn't cleared for commercial use.
func (s Subscription) Free() bool {
	t := strings.ToLower(strings.TrimSpace(s.Tier))
	return t == "" || t == "free" || strings.HasPrefix(strings.ToLower(s.Status), "free")
}

// Words is the subscription in a line: "starter plan, active: 1240 used,
// 28760 left of 30000 characters (credits) until 2026-12-03".
func (s Subscription) Words() string {
	tier := s.Tier
	if tier == "" {
		tier = "unknown"
	}
	w := tier + " plan"
	if s.Status != "" {
		w += ", " + s.Status
	}
	left := s.Limit - s.Used
	if left < 0 {
		left = 0
	}
	w += fmt.Sprintf(": %d used, %d left of %d characters (credits) this period", s.Used, left, s.Limit)
	if s.ResetUnix > 0 {
		w += ", until " + time.Unix(s.ResetUnix, 0).Format("2006-01-02")
	}
	return w
}

// GetSubscription reads <base>/v1/user/subscription. It bills nothing.
func GetSubscription(ctx context.Context, hc *http.Client, base string, header http.Header) (Subscription, error) {
	status, _, raw, err := elevenCall(ctx, hc, http.MethodGet, elevenBase(base)+"/v1/user/subscription", header, nil, "application/json", ElevenLabsMaxJSON)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription: %v", err)
	}
	if status < 200 || status > 299 {
		return Subscription{}, elevenError("subscription", status, raw, header)
	}
	var s Subscription
	if err := json.Unmarshal(raw, &s); err != nil {
		return Subscription{}, fmt.Errorf("subscription: the answer is not ElevenLabs' subscription JSON")
	}
	return s, nil
}

// VoiceQuery is a search of the shared voice library.
type VoiceQuery struct {
	Language string
	Gender   string
	UseCase  string
	Accent   string
	Search   string
	// Commercial keeps voices with no terms beyond the plan's: no custom
	// rate and no live moderation.
	Commercial bool
	Limit      int
}

// SharedVoice is one voice of the library, as a search prints it.
type SharedVoice struct {
	VoiceID     string `json:"voice_id"`
	Name        string `json:"name"`
	Accent      string `json:"accent,omitempty"`
	Gender      string `json:"gender,omitempty"`
	Age         string `json:"age,omitempty"`
	UseCase     string `json:"use_case,omitempty"`
	Language    string `json:"language,omitempty"`
	Descriptive string `json:"descriptive,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	// LiveModerated and Rate are the terms --commercial filters on.
	LiveModerated bool     `json:"live_moderation_enabled,omitempty"`
	Rate          *float64 `json:"rate,omitempty"`
}

// SharedVoices searches <base>/v1/shared-voices. It bills nothing.
func SharedVoices(ctx context.Context, hc *http.Client, base string, header http.Header, q VoiceQuery) ([]SharedVoice, error) {
	v := url.Values{}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	v.Set("page_size", strconv.Itoa(limit))
	for k, val := range map[string]string{"language": q.Language, "gender": q.Gender, "use_cases": q.UseCase, "accent": q.Accent, "search": q.Search} {
		if s := strings.TrimSpace(val); s != "" {
			v.Set(k, s)
		}
	}
	if q.Commercial {
		v.Set("include_custom_rates", "false")
		v.Set("include_live_moderated", "false")
	}
	status, _, raw, err := elevenCall(ctx, hc, http.MethodGet, elevenBase(base)+"/v1/shared-voices?"+v.Encode(), header, nil, "application/json", ElevenLabsMaxJSON)
	if err != nil {
		return nil, fmt.Errorf("shared voices: %v", err)
	}
	if status < 200 || status > 299 {
		return nil, elevenError("shared voices", status, raw, header)
	}
	var ans struct {
		Voices []SharedVoice `json:"voices"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("shared voices: the answer is not ElevenLabs' shared-voices JSON")
	}
	out := ans.Voices[:0]
	for _, sv := range ans.Voices {
		if q.Commercial && (sv.LiveModerated || (sv.Rate != nil && *sv.Rate > 0)) {
			continue
		}
		out = append(out, sv)
	}
	return out, nil
}

// MusicTimeout is how long one music call may take.
const MusicTimeout = 10 * time.Minute

// MusicMaxBody is the most of a music answer read (the MP3 itself).
const MusicMaxBody = 64 << 20

// MusicRequest is one music call.
type MusicRequest struct {
	BaseURL      string
	Header       http.Header
	Prompt       string
	LengthMS     int
	Model        string
	Instrumental bool
}

// MusicResult is the MP3, the song's id when the answer names one, and the
// cost when the answer's character-cost header gives it.
type MusicResult struct {
	Audio     []byte
	SongID    string
	Cost      int
	CostKnown bool
}

// MusicClient follows no redirect and waits MusicTimeout.
func MusicClient() *http.Client {
	return &http.Client{
		Timeout:       MusicTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ComposeMusic posts r to <base>/v1/music and returns the MP3.
func ComposeMusic(ctx context.Context, hc *http.Client, r MusicRequest) (MusicResult, error) {
	body := map[string]any{"prompt": r.Prompt, "music_length_ms": r.LengthMS}
	if r.Model != "" {
		body["model_id"] = r.Model
	}
	if r.Instrumental {
		body["force_instrumental"] = true
	}
	b, _ := json.Marshal(body)
	status, h, raw, err := elevenCall(ctx, hc, http.MethodPost, elevenBase(r.BaseURL)+"/v1/music?output_format=mp3_44100_128", r.Header, b, "audio/mpeg", MusicMaxBody)
	if err != nil {
		return MusicResult{}, fmt.Errorf("music: %v", err)
	}
	if status < 200 || status > 299 {
		return MusicResult{}, elevenError("music", status, raw, r.Header)
	}
	if len(raw) == 0 || strings.HasPrefix(strings.TrimSpace(h.Get("Content-Type")), "application/json") {
		return MusicResult{}, fmt.Errorf("music: the answer has no audio")
	}
	res := MusicResult{Audio: raw, SongID: strings.TrimSpace(h.Get("song-id"))}
	for _, k := range []string{"character-cost", "x-character-count"} {
		if n, err := strconv.Atoi(strings.TrimSpace(h.Get(k))); err == nil && n >= 0 {
			res.Cost, res.CostKnown = n, true
			break
		}
	}
	return res, nil
}

// CheckElevenLabs is the built-in check of a meter: elevenlabs connector:
// GET /v1/user/subscription with the key. Detail is the plan line.
func CheckElevenLabs(ctx context.Context, c *tracker.Connector) (result, detail string) {
	h, err := Headers(c)
	if err != nil {
		return ResultMissing, ""
	}
	base := c.URL
	if err := tracker.CheckConnectorURL(elevenBase(base)); err != nil {
		return ResultFailing, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	s, err := GetSubscription(ctx, RestClient(), base, h)
	if err != nil {
		var ee *ElevenLabsError
		if errors.As(err, &ee) && ee.Refused() {
			return ResultFailing, fmt.Sprintf("ElevenLabs refused the key (%d)", ee.Status)
		}
		return ResultFailing, err.Error()
	}
	return ResultOK, s.Words()
}
