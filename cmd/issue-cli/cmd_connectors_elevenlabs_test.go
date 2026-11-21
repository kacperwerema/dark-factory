package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// elevenAPI stands in for ElevenLabs' subscription, shared-voices and music
// endpoints. used is the subscription's character_count; a music call adds
// musicCost to it and, with costHeader, names it in character-cost.
type elevenAPI struct {
	*httptest.Server
	mu         sync.Mutex
	seen       []string // "<method> <path>?<query> <body>"
	tier       string
	used       int
	musicCost  int
	costHeader bool
	// lag is how many subscription reads after a music or tts call still
	// show the count from before it, as ElevenLabs' does for a while.
	lag     int
	pending int
	// subFail makes every subscription read after the next call answer 500.
	subFail bool
	failing bool
	// ttsCost names character-cost on a tts answer when >= 0; a tts call
	// adds ttsUsed to the count.
	ttsCost int
	ttsUsed int
	// onMusic runs while a music call is answered.
	onMusic func()
}

func (s *elevenAPI) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, l := range s.seen {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func newElevenAPI(t *testing.T) *elevenAPI {
	s := &elevenAPI{tier: "starter", used: 1240, musicCost: 450, costHeader: true, ttsCost: -1}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.seen = append(s.seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+string(b))
		if r.Header.Get("xi-api-key") != fakeXIKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"detail":{"status":"invalid_api_key","message":"bad key `+r.Header.Get("xi-api-key")+`"}}`)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/subscription":
			if s.failing {
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"detail":"busy"}`)
				return
			}
			if s.pending > 0 {
				s.pending--
				json.NewEncoder(w).Encode(map[string]any{"tier": s.tier, "status": "active", "character_count": s.used - s.musicCost - s.ttsUsed, "character_limit": 30000})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"tier": s.tier, "status": "active", "character_count": s.used, "character_limit": 30000, "next_character_count_reset_unix": 1796342400})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/shared-voices":
			rate := 2.0
			json.NewEncoder(w).Encode(map[string]any{"has_more": false, "voices": []map[string]any{
				{"voice_id": "PLV1", "name": "Ania", "accent": "standard", "gender": "female", "age": "young", "use_case": "narrative_story", "language": "pl", "preview_url": "https://example.invalid/ania.mp3"},
				{"voice_id": "PLV2", "name": "Marta", "accent": "standard", "gender": "female", "use_case": "advertisement", "language": "pl", "preview_url": "https://example.invalid/marta.mp3", "rate": rate},
				{"voice_id": "PLV3", "name": "Ola", "gender": "female", "use_case": "social_media", "language": "pl", "live_moderation_enabled": true},
			}})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/"):
			s.used += s.ttsUsed
			s.pending, s.failing = s.lag, s.subFail
			if s.ttsCost >= 0 {
				w.Header().Set("character-cost", strconv.Itoa(s.ttsCost))
			}
			json.NewEncoder(w).Encode(map[string]any{"audio_base64": base64.StdEncoding.EncodeToString([]byte("ID3-fake-mp3")), "alignment": json.RawMessage(ttsAlignment)})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/music":
			s.used += s.musicCost
			s.pending, s.failing = s.lag, s.subFail
			if s.onMusic != nil {
				s.onMusic()
			}
			if s.costHeader {
				w.Header().Set("character-cost", "450")
			}
			w.Header().Set("song-id", "song-77")
			w.Header().Set("Content-Type", "audio/mpeg")
			io.WriteString(w, "ID3-fake-music")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// elevenFixture is meteredFixture with the elevenlabs cap at cap.
func elevenFixture(t *testing.T, api *elevenAPI, cap string) string {
	root, _, _ := meteredFixture(t, api.URL)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, strings.Replace(string(b), "spend: {meter: elevenlabs, unit: characters, cap: 20}", "spend: {meter: elevenlabs, unit: characters, cap: "+cap+"}", 1))
	return root
}

func noKeyIn(t *testing.T, where string, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if strings.Contains(s, fakeXIKey) {
			t.Errorf("the key reached %s", where)
		}
	}
}

// TestElevenLabsCheckReadsTheSubscription: the elevenlabs connector has a
// built-in check (no check: in projects.yaml) that reads the subscription
// with the key and prints the plan and the credits used and left; it
// records nothing in the spend log, runs at the cap, never prints the key,
// and a refused or missing key fails or is missing.
func TestElevenLabsCheckReadsTheSubscription(t *testing.T) {
	api := newElevenAPI(t)
	root := elevenFixture(t, api, "20")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	// At the cap: the check is free.
	writeLedger(t, root, `{"connector":"video/elevenlabs","date":"2026-11-01T10:00:00Z","month":"`+monthNow()+`","unit":"characters","amount":20,"call":"tts","exit":0}`+"\n")
	out, err := runCLI(t, "connector", "check", "video/elevenlabs")
	if err != nil {
		t.Fatalf("check: %s", out)
	}
	assertContains(t, out, "✓ video/elevenlabs ok")
	assertContains(t, out, "  starter plan, active: 1240 used, 28760 left of 30000 characters (credits) this period, until 20")
	if api.count("GET /v1/user/subscription") != 1 {
		t.Errorf("calls: %v", api.seen)
	}
	if recs := spendLines(t, root); len(recs) != 1 {
		t.Errorf("the check was metered: %+v", recs)
	}
	checks, _ := os.ReadFile(filepath.Join(root, "issues", ".connectors", "checks.jsonl"))
	assertContains(t, string(checks), `"detail":"starter plan`)
	noKeyIn(t, "the check's output or log", out, string(checks))
	out, _ = runCLI(t, "connectors", "video")
	assertContains(t, out, "check ok just now")

	t.Setenv("ACME_XI", "wrong-key-5d1")
	out, err = runCLI(t, "connector", "check", "video/elevenlabs")
	if processExitCode(err) != 1 {
		t.Fatalf("a refused key: %v %s", err, out)
	}
	assertContains(t, out, "✗ video/elevenlabs failing")
	assertContains(t, out, "ElevenLabs refused the key (401)")
	if strings.Contains(out, "wrong-key-5d1") {
		t.Error("the refused key was printed")
	}
	t.Setenv("ACME_XI", "")
	out, _ = runCLI(t, "connector", "check", "video/elevenlabs")
	assertContains(t, out, "✗ video/elevenlabs missing")
}

// TestElevenLabsVoicesSearchIsFree: voices search reads the shared library
// with the filters as query parameters, prints voice_id, name, accent, use
// case and preview_url, takes --json, filters --commercial to voices with
// no extra terms, and runs at the cap recording nothing.
func TestElevenLabsVoicesSearchIsFree(t *testing.T) {
	api := newElevenAPI(t)
	root := elevenFixture(t, api, "20")
	writeLedger(t, root, `{"connector":"video/elevenlabs","date":"2026-11-01T10:00:00Z","month":"`+monthNow()+`","unit":"characters","amount":20,"call":"tts","exit":0}`+"\n")
	out, err := runCLI(t, "connector", "exec", "video/elevenlabs", "--", "voices", "search", "--language", "pl", "--gender", "female", "--use-case", "narrative_story")
	if err != nil {
		t.Fatalf("search: %s", out)
	}
	assertContains(t, out, "PLV1 · Ania · standard · narrative_story · female young")
	assertContains(t, out, "  preview https://example.invalid/ania.mp3")
	assertContains(t, out, "3 voices; listen to the previews, then the owner adds one to video/elevenlabs's voices: in projects.yaml")
	q := api.seen[len(api.seen)-1]
	for _, want := range []string{"GET /v1/shared-voices?", "language=pl", "gender=female", "use_cases=narrative_story", "page_size=20"} {
		assertContains(t, q, want)
	}

	out, err = runCLI(t, "connector", "exec", "video/elevenlabs", "--", "voices", "search", "--commercial", "--json")
	if err != nil {
		t.Fatalf("--json: %s", out)
	}
	var vs []map[string]any
	if err := json.Unmarshal([]byte(out), &vs); err != nil || len(vs) != 1 || vs[0]["voice_id"] != "PLV1" || vs[0]["preview_url"] != "https://example.invalid/ania.mp3" {
		t.Fatalf("--commercial --json = %s (%v)", out, err)
	}
	assertContains(t, api.seen[len(api.seen)-1], "include_custom_rates=false")
	if recs := spendLines(t, root); len(recs) != 1 {
		t.Errorf("a search was metered: %+v", recs)
	}
	noKeyIn(t, "the search's output", out)
}

// TestElevenLabsMusicIsCapped: music posts the prompt and length, writes
// the MP3 and a licence file beside it, prints the storyboard block and
// the legal lines, and records the character-cost (credits); with no
// header it records the subscription's change; with neither the next
// billing call is held. No cap, the cap reached, an estimate past the
// room, or a free plan send no music request.
func TestElevenLabsMusicIsCapped(t *testing.T) {
	defer shortSubscriptionSettle()()
	api := newElevenAPI(t)
	root := elevenFixture(t, api, "2000")
	dir := filepath.Join(t.TempDir(), "reel")
	music := func(name string, ms string) (string, error) {
		return runCLI(t, "connector", "exec", "video/elevenlabs", "--", "music", "--prompt", "warm acoustic, 100 bpm", "--length-ms", ms, "--instrumental", "--out", filepath.Join(dir, name+".mp3"))
	}
	out, err := music("bed", "30000")
	if err != nil {
		t.Fatalf("music: %s", out)
	}
	assertContains(t, out, "✓ video/elevenlabs: music → "+filepath.Join(dir, "bed.mp3")+" (30000 ms), 450 characters (the answer's character-cost)")
	assertContains(t, out, `storyboard: "music": {"path": "bed.mp3", "licence": {"source": "ElevenLabs Music", "ref": "song song-77, starter plan (paid), commercial use, `)
	assertContains(t, out, "music_licence: ElevenLabs Music, song song-77, starter plan (paid), commercial use")
	if b, _ := os.ReadFile(filepath.Join(dir, "bed.mp3")); string(b) != "ID3-fake-music" {
		t.Errorf("mp3 = %q", b)
	}
	lic, _ := os.ReadFile(filepath.Join(dir, "bed.licence.json"))
	assertContains(t, string(lic), `"source": "ElevenLabs Music"`)
	post := api.seen[len(api.seen)-1]
	for _, want := range []string{"POST /v1/music?output_format=mp3_44100_128", `"prompt":"warm acoustic, 100 bpm"`, `"music_length_ms":30000`, `"force_instrumental":true`} {
		assertContains(t, post, want)
	}
	recs := spendLines(t, root)
	if len(recs) != 1 || recs[0].Amount != 450 || recs[0].Call != "music" || recs[0].Unit != "characters" || recs[0].By != "reels/clip" {
		t.Fatalf("spend log = %+v", recs)
	}
	ledger, _ := os.ReadFile(filepath.Join(root, "issues", ".connectors", "spend.jsonl"))
	if strings.Contains(string(ledger), "acoustic") {
		t.Error("the prompt reached the spend log")
	}
	noKeyIn(t, "music's output or the ledger", out, string(ledger), string(lic))

	// No header: the subscription's count before and after.
	api.costHeader = false
	out, err = music("bed2", "30000")
	if err != nil {
		t.Fatalf("no header: %s", out)
	}
	assertContains(t, out, "450 characters (the subscription's count, 1690 → 2140)")

	// Neither: unknown, and the next billing call is held.
	api.musicCost = 0
	out, err = music("bed3", "30000")
	if err != nil {
		t.Fatalf("unknown: %s", out)
	}
	assertContains(t, out, "its cost is unknown")
	posts := api.count("POST /v1/music")
	if out, err := music("bed4", "30000"); err == nil || !strings.Contains(out, "is held: its last billing call's cost is unknown") {
		t.Errorf("after an unknown cost: %s", out)
	}
	if out, err := runCLI(t, "connector", "exec", "video/elevenlabs", "--", "tts", "--placement", "library", "--text", "x", "--out", filepath.Join(dir, "x.mp3")); err == nil || !strings.Contains(out, "is held") {
		t.Errorf("tts after an unknown music cost: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if out, err := runCLI(t, "connector", "spend", "add", "video/elevenlabs", "450", "--note", "music bed3"); err != nil {
		t.Fatalf("spend add: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")

	// 1350 / 2000 spent: 60 s is about 900, past the 650 left.
	if out, err := music("long", "60000"); err == nil || !strings.Contains(out, "60000 ms of music is about 900 characters (ElevenLabs bills 900 credits a minute) and the month has 650 left") {
		t.Errorf("estimate past the room: %s", out)
	}
	// A free plan: refused after the free subscription read.
	api.tier = "free"
	if out, err := music("free", "10000"); err == nil || !strings.Contains(out, "on the free plan: the Music API is for paid plans") {
		t.Errorf("free plan: %s", out)
	}
	if api.count("POST /v1/music") != posts {
		t.Errorf("a refused music call reached the API: %v", api.seen)
	}
	if out, err := music("short", "1000"); err == nil || !strings.Contains(out, "--length-ms is 3000 to 300000") {
		t.Errorf("too short: %s", out)
	}
	if out, err := runCLI(t, "connector", "exec", "video/elevenlabs", "--"); err == nil || !strings.Contains(out, "voices search [--language pl]") || !strings.Contains(out, "music (--prompt") {
		t.Errorf("usage: %s", out)
	}
}

// TestElevenLabsMusicWithoutRoom: with no cap the music call is refused
// before any request, and at the cap too.
func TestElevenLabsMusicWithoutRoom(t *testing.T) {
	api := newElevenAPI(t)
	root := elevenFixture(t, api, "0")
	dir := t.TempDir()
	args := []string{"connector", "exec", "video/elevenlabs", "--", "music", "--prompt", "p", "--length-ms", "5000", "--out", filepath.Join(dir, "m.mp3")}
	if out, err := runCLI(t, args...); err == nil || !strings.Contains(out, "spends money and has no cap") {
		t.Errorf("no cap: %s", out)
	}
	elevenFixtureCap(t, "100")
	writeLedger(t, root, `{"connector":"video/elevenlabs","date":"2026-11-01T10:00:00Z","month":"`+monthNow()+`","unit":"characters","amount":100,"call":"tts","exit":0}`+"\n")
	if out, err := runCLI(t, args...); err == nil || !strings.Contains(out, "is at its cap: 100 / 100 characters") {
		t.Errorf("at the cap: %s", out)
	}
	if len(api.seen) != 0 {
		t.Errorf("a refused call reached the API: %v", api.seen)
	}
}

func elevenFixtureCap(t *testing.T, cap string) {
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	s := string(b)
	i := strings.Index(s, "spend: {meter: elevenlabs, unit: characters, cap: ")
	j := strings.Index(s[i:], "}")
	mustWrite(t, cfg, s[:i]+"spend: {meter: elevenlabs, unit: characters, cap: "+cap+s[i+j:])
}

func monthNow() string { return connectors.Month(time.Now()) }

// writeLedger writes the spend log.
func writeLedger(t *testing.T, root, body string) {
	t.Helper()
	os.MkdirAll(filepath.Join(root, "issues", ".connectors"), 0755)
	mustWrite(t, filepath.Join(root, "issues", ".connectors", "spend.jsonl"), body)
}

// TestElevenLabsMusicPricedFromALaggingBalance: a 44-second song
// (cli/videoelevenlabs-music-…): the answer names no character-cost and
// ElevenLabs' character_count moves from 198 to 803 only some reads after
// the call. The call records 605 with source: balance and holds nothing;
// a balance that can't be read after the call holds the next one.
func TestElevenLabsMusicPricedFromALaggingBalance(t *testing.T) {
	defer shortSubscriptionSettle()()
	api := newElevenAPI(t)
	api.used, api.musicCost, api.costHeader, api.lag = 198, 605, false, 2
	root := elevenFixture(t, api, "5000")
	dir := t.TempDir()
	music := func(name string) (string, error) {
		return runCLI(t, "connector", "exec", "video/elevenlabs", "--", "music", "--prompt", "warm", "--length-ms", "44000", "--out", filepath.Join(dir, name+".mp3"))
	}
	out, err := music("reel1")
	if err != nil {
		t.Fatalf("music: %s", out)
	}
	assertContains(t, out, "605 characters (the subscription's count, 198 → 803)")
	recs := spendLines(t, root)
	if len(recs) != 1 || recs[0].Amount != 605 || recs[0].Unknown || recs[0].Source != connectors.SourceBalance || recs[0].Ref != "song-77" {
		t.Fatalf("spend log = %+v", recs)
	}
	if out, err := music("reel2"); err != nil {
		t.Fatalf("a second call is not held: %s", out)
	}

	// Another entry reaches the spend log during the call (a call that
	// skipped the lock): the count may hold both, so unknown.
	api.onMusic = func() {
		connectors.AppendSpend(&tracker.Project{IssueDir: filepath.Join(root, "issues")}, connectors.SpendRecord{Connector: "video/elevenlabs", Month: monthNow(), Unit: "characters", Amount: 1, Manual: true, Outside: true})
	}
	out, err = music("between")
	if err != nil {
		t.Fatalf("between: %s", out)
	}
	assertContains(t, out, "another entry reached the spend log during the call")
	api.onMusic = nil
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if out, err := runCLI(t, "connector", "spend", "add", "video/elevenlabs", "605"); err != nil {
		t.Fatalf("spend add: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")

	// The balance can't be read after the call: unknown, and held.
	api.subFail = true
	out, err = music("reel3")
	if err != nil {
		t.Fatalf("unreadable balance: %s", out)
	}
	assertContains(t, out, "its cost is unknown")
	assertContains(t, out, "the subscription after it could not be read")
	if recs := spendLines(t, root); !recs[len(recs)-1].Unknown {
		t.Errorf("unreadable balance recorded %+v", recs[len(recs)-1])
	}
	if out, err := music("reel4"); err == nil || !strings.Contains(out, "is held") {
		t.Errorf("after an unreadable balance: %s", out)
	}
}

// TestElevenLabsTTSWithoutCostReadsTheBalance: tts keeps the answer's
// character-cost; with none it is priced from the subscription's count
// before and after, and with that unreadable from the text's characters.
func TestElevenLabsTTSWithoutCostReadsTheBalance(t *testing.T) {
	defer shortSubscriptionSettle()()
	api := newElevenAPI(t)
	api.ttsCost, api.ttsUsed, api.lag = 7, 9, 1
	root := elevenFixture(t, api, "5000")
	dir := t.TempDir()
	tts := func(name string) (string, error) {
		return runCLI(t, "connector", "exec", "video/elevenlabs", "--", "tts", "--placement", "library", "--text", "Hello", "--out", filepath.Join(dir, name+".mp3"))
	}
	if out, err := tts("a"); err != nil {
		t.Fatalf("tts: %s", out)
	}
	api.ttsCost = -1
	if out, err := tts("b"); err != nil {
		t.Fatalf("tts without cost: %s", out)
	}
	api.subFail = true
	if out, err := tts("c"); err != nil {
		t.Fatalf("tts without cost or balance: %s", out)
	}
	recs := spendLines(t, root)
	if len(recs) != 3 || recs[0].Amount != 7 || recs[0].Source != "" || recs[1].Amount != 9 || recs[1].Source != connectors.SourceBalance || recs[2].Amount != 5 || recs[2].Unknown {
		t.Fatalf("spend log = %+v", recs)
	}
}

// shortSubscriptionSettle makes the wait for the subscription's count to
// move after a call short; the returned func puts it back.
func shortSubscriptionSettle() func() {
	w, p := subscriptionSettle, subscriptionPoll
	subscriptionSettle, subscriptionPoll = 2*time.Second, 10*time.Millisecond
	return func() { subscriptionSettle, subscriptionPoll = w, p }
}
