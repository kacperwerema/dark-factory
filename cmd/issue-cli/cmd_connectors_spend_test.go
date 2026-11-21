package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/standin"
)

// fakeXIKey stands in for the ElevenLabs key: it may reach only the
// stand-in API's requests.
const fakeXIKey = "fake-xi-key-91c2"

// klingStandIn is the stand-in Kling CLI: it logs its arguments to
// $KLING_LOG, prints $KLING_OUT's file and exits $KLING_EXIT. With
// $KLING_BAL set, account prints the balance in that file (exit 3 when
// the file is missing) and any other call takes $KLING_CHARGE off it. It
// never reads ~/.kling.
const klingStandIn = `printf '%s\n' "$*" >> "$KLING_LOG"
if [ "$1" = account ] && [ -n "$KLING_BAL" ]; then
  [ -f "$KLING_BAL" ] || { echo "not logged in" >&2; exit 3; }
  printf '{"data":{"membership":"pro","availableRemainCredits":%s}}\n' "$(cat "$KLING_BAL")"
  exit 0
fi
[ -n "$KLING_OUT" ] && cat "$KLING_OUT"
if [ -n "$KLING_BAL" ] && [ -f "$KLING_BAL" ] && [ -n "$KLING_CHARGE" ]; then
  echo $(( $(cat "$KLING_BAL") - KLING_CHARGE )) > "$KLING_BAL"
fi
exit "${KLING_EXIT:-0}"
`

// meteredFixture is a project whose Video department (system Reels) has the
// bundled template's two reel connectors, with caps: kling at 100 credits,
// elevenlabs at 20 characters against srvURL, plus a kling without a cap.
// The stand-in kling is first on PATH; the worker runs reels/clip.
func meteredFixture(t *testing.T, srvURL string) (root, klingLog, klingOut string) {
	t.Helper()
	f := newCallLogFixture(t, "backlog", "", "")
	root = f.root
	os.MkdirAll(filepath.Join(root, "issues", "Reels"), 0755)
	mustWrite(t, filepath.Join(root, "issues", "Reels", "clip.md"), "---\ntitle: \"Clip\"\nstatus: \"backlog\"\nsystem: \"Reels\"\n---\n\nBody.\n")
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "kling"), klingStandIn)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	klingLog = filepath.Join(root, "kling.log")
	klingOut = filepath.Join(root, "kling.json")
	t.Setenv("KLING_LOG", klingLog)
	t.Setenv("KLING_OUT", klingOut)
	t.Setenv("KLING_EXIT", "0")
	t.Setenv("ACME_XI", fakeXIKey)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`    departments:
      - name: Video
        systems: [Reels]
        connectors:
          - name: kling
            kind: tool
            command: kling
            sends: true
            spend:
              meter: json
              field: creditsConsumed
              unit: credits
              cap: 100
              free: [who_am_i, account, query_tasks]
              refuse: [login, logout]
          - name: kling-nocap
            kind: tool
            command: kling
            spend: {meter: json, field: creditsConsumed, unit: credits}
          - name: elevenlabs
            kind: tool
            url: `+srvURL+`
            headers:
              xi-api-key: env:ACME_XI
            sends: true
            spend: {meter: elevenlabs, unit: characters, cap: 20}
            voices:
              - placement: narrator
                voice_id: VOICE1
                name: "Owner (clone)"
                real_person: "the owner"
                consent: "2026-11-21, own voice"
              - placement: actor-1
                voice_id: VOICE2
                name: "Actor"
                real_person: "an actor"
              - placement: library
                voice_id: VOICE3
`)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	return root, klingLog, klingOut
}

func spendLines(t *testing.T, root string) []connectors.SpendRecord {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, "issues", ".connectors", "spend.jsonl"))
	var out []connectors.SpendRecord
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r connectors.SpendRecord
		if l != "" && json.Unmarshal([]byte(l), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func standinLog(path string) string { b, _ := os.ReadFile(path); return string(b) }

// TestKlingConnectorMetersAndCaps: a billing call through the metered
// connector passes kling's stdout through unchanged and records its
// creditsConsumed once; free calls record nothing and run over the cap;
// login is refused; at the cap, with no cap, or after a call with no cost,
// a billing call is refused before kling runs.
func TestKlingConnectorMetersAndCaps(t *testing.T) {
	root, klingLog, klingOut := meteredFixture(t, "http://127.0.0.1:1")
	month := connectors.Month(time.Now())
	// The poll's final JSON repeats the submission: counted once.
	answer := `{"body":{"submission":{"generationId":"g1","creditsConsumed":51}},"works":[{"url":"https://example.invalid/v.mp4"}],"creditsConsumed":51}` + "\n"
	mustWrite(t, klingOut, answer)

	out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "--model", "m", "a prompt", "--poll", "300")
	if err != nil {
		t.Fatalf("billing call: %s", out)
	}
	if out != answer {
		t.Errorf("stdout = %q, want kling's own %q", out, answer)
	}
	recs := spendLines(t, root)
	if len(recs) != 1 || recs[0].Amount != 51 || recs[0].Call != "text_to_video" || recs[0].By != "reels/clip" || recs[0].Month != month || recs[0].Unit != "credits" {
		t.Fatalf("spend log = %+v", recs)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "issues", ".connectors", "spend.jsonl")); strings.Contains(string(b), "a prompt") {
		t.Error("the prompt reached the spend log")
	}

	// A leading "kling" is the command itself; anything else is kling's
	// argument, never a program of its own.
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "kling", "query_tasks", "g1"); err != nil {
		t.Fatalf("free call: %s", out)
	}
	if len(spendLines(t, root)) != 1 {
		t.Error("a free call was recorded")
	}
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "login"); err == nil || !strings.Contains(out, "does not run login: it is the owner's") {
		t.Errorf("login: %s", out)
	}
	if strings.Contains(standinLog(klingLog), "login") {
		t.Error("kling ran login")
	}
	runCLI(t, "connector", "exec", "video/kling", "--", "sh", "-c", "exit 0")
	if !strings.Contains(standinLog(klingLog), "sh -c exit 0\n") {
		t.Errorf("sh was not handed to kling as its argument: %q", standinLog(klingLog))
	}

	// The owner records 50 by hand: 101 / 100 credits, at the cap.
	if out, err := runCLI(t, "connector", "spend", "add", "video/kling", "50", "--note", "used in the app"); err == nil || !strings.Contains(out, "for the owner or Video's lead") {
		t.Errorf("a worker recorded spend: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	out, err = runCLI(t, "connector", "spend", "add", "video/kling", "50", "--note", "used in the app")
	if err != nil {
		t.Fatalf("owner spend add: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")
	before := standinLog(klingLog)
	out, err = runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "--model", "m", "again")
	if err == nil || !strings.Contains(out, "connector video/kling is at its cap: 152 / 100 credits spent in "+month) || !strings.Contains(out, "the owner raises spend: cap:") {
		t.Fatalf("over the cap: %s", out)
	}
	if standinLog(klingLog) != before {
		t.Error("kling ran over the cap")
	}
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "query_tasks", "g1"); err != nil {
		t.Errorf("a free call over the cap: %s", out)
	}
	out, _ = runCLI(t, "connectors", "video")
	assertContains(t, out, "video/kling (tool) · sends · spend 152 / 100 credits this month ("+month+")")
	var list []connectors.Status
	js, _ := runCLI(t, "connectors", "video", "--json")
	if err := json.Unmarshal([]byte(js), &list); err != nil || list[0].Spend == nil || list[0].Spend.Spent != 152 || list[0].Problem() != "at its spend cap (152 / 100 credits)" {
		t.Fatalf("json: %v %s", err, js)
	}

	// No cap: every billing call refused, a free one runs.
	before = standinLog(klingLog)
	if out, err := runCLI(t, "connector", "exec", "video/kling-nocap", "--", "text_to_video", "x"); err == nil || !strings.Contains(out, "has no cap: nothing billed is run until the owner writes spend: cap:") {
		t.Errorf("no cap: %s", out)
	}
	if standinLog(klingLog) != before {
		t.Error("kling ran with no cap")
	}
}

// TestKlingCallWithoutACostHoldsTheNext: a call that worked and named no
// creditsConsumed is logged unknown and holds the next billing call until
// an amount is recorded; a failed call with no cost passes its exit code
// and holds nothing.
func TestKlingCallWithoutACostHoldsTheNext(t *testing.T) {
	root, klingLog, klingOut := meteredFixture(t, "http://127.0.0.1:1")
	mustWrite(t, klingOut, `{"error":"invalid model"}`+"\n")
	t.Setenv("KLING_EXIT", "1")
	if _, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); processExitCode(err) != 1 {
		t.Fatalf("exit = %d (%v)", processExitCode(err), err)
	}
	t.Setenv("KLING_EXIT", "0")
	mustWrite(t, klingOut, `{"works":[{"url":"u"}]}`+"\n")
	out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x")
	if err != nil || !strings.Contains(out, "this call's cost is unknown") {
		t.Fatalf("no cost: %v %s", err, out)
	}
	if recs := spendLines(t, root); len(recs) != 2 || recs[0].Unknown || recs[0].Exit != 1 || !recs[1].Unknown {
		t.Fatalf("spend log = %+v", recs)
	}
	before := standinLog(klingLog)
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); err == nil || !strings.Contains(out, "is held: its last billing call's cost is unknown") || !strings.Contains(out, "issue-cli connector spend add video/kling <amount>") {
		t.Fatalf("held: %s", out)
	}
	if standinLog(klingLog) != before {
		t.Error("kling ran while held")
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if out, err := runCLI(t, "connector", "spend", "add", "video/kling", "40"); err != nil || !strings.Contains(out, "✓ recorded 40 credits on video/kling for "+spendLines(t, root)[1].ID) || !strings.Contains(out, ": 40 / 100 credits this month") {
		t.Fatalf("spend add: %v %s", err, out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")
	mustWrite(t, klingOut, `{"creditsConsumed":"12"}`+"\n")
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); err != nil {
		t.Fatalf("after the record: %s", out)
	}
	if recs := spendLines(t, root); recs[len(recs)-1].Amount != 12 {
		t.Errorf("a cost given as a string: %+v", recs[len(recs)-1])
	}
}

// ttsAPI stands in for ElevenLabs' with-timestamps endpoint.
type ttsAPI struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string // "<path> <xi-api-key> <text>"
}

func (s *ttsAPI) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.seen) }

const ttsAlignment = `{"characters":["H","i"],"character_start_times_seconds":[0,0.1],"character_end_times_seconds":[0.1,0.2]}`

func newTTSAPI(t *testing.T) *ttsAPI {
	s := &ttsAPI{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/user/subscription" {
			// tts reads it before the call; this stand-in has none.
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Text string `json:"text"`
		}
		json.Unmarshal(b, &body)
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("xi-api-key")+" "+body.Text)
		s.mu.Unlock()
		if r.Header.Get("xi-api-key") != fakeXIKey {
			http.Error(w, `{"detail":"invalid key"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("character-cost", "12")
		json.NewEncoder(w).Encode(map[string]any{"audio_base64": base64.StdEncoding.EncodeToString([]byte("ID3-fake-mp3")), "alignment": json.RawMessage(ttsAlignment)})
	}))
	t.Cleanup(s.Close)
	return s
}

// TestElevenLabsConnectorSpeaksAndCaps: tts for a placement writes the MP3
// and the alignment as ElevenLabs returns it and records the characters
// billed; an unknown placement, a real person's voice without consent, or
// a month at the cap is refused before any request; the key reaches only
// the request.
func TestElevenLabsConnectorSpeaksAndCaps(t *testing.T) {
	api := newTTSAPI(t)
	root, _, _ := meteredFixture(t, api.URL)
	dir := t.TempDir()
	text := "Cześć, tu narrator."
	mustWrite(t, filepath.Join(dir, "s1.txt"), text)
	tts := func(placement, name string) (string, error) {
		return runCLI(t, "connector", "exec", "video/elevenlabs", "--", "tts", "--placement", placement, "--text-file", filepath.Join(dir, "s1.txt"),
			"--out", filepath.Join(dir, name+".mp3"), "--alignment", filepath.Join(dir, name+".json"))
	}
	out, err := tts("narrator", "s1")
	if err != nil {
		t.Fatalf("tts: %s", out)
	}
	assertContains(t, out, "✓ video/elevenlabs: narrator (Owner (clone)) → "+filepath.Join(dir, "s1.mp3")+", 12 characters")
	if b, _ := os.ReadFile(filepath.Join(dir, "s1.mp3")); string(b) != "ID3-fake-mp3" {
		t.Errorf("mp3 = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "s1.json")); strings.TrimSpace(string(b)) != ttsAlignment {
		t.Errorf("alignment = %s", b)
	}
	if got := api.seen[0]; got != "POST /v1/text-to-speech/VOICE1/with-timestamps "+fakeXIKey+" "+text {
		t.Errorf("request = %q", got)
	}
	recs := spendLines(t, root)
	if len(recs) != 1 || recs[0].Amount != 12 || recs[0].Unit != "characters" || recs[0].Call != "tts" {
		t.Fatalf("spend log = %+v", recs)
	}
	ledger, _ := os.ReadFile(filepath.Join(root, "issues", ".connectors", "spend.jsonl"))
	if strings.Contains(string(ledger), fakeXIKey) || strings.Contains(string(ledger), "narrator.") || strings.Contains(out, fakeXIKey) {
		t.Error("the key or the text left the request")
	}

	for _, c := range []struct{ placement, want string }{
		{"host", `no voice at placement "host" (it has: narrator, actor-1, library)`},
		{"actor-1", "the voice at actor-1 is an actor's, a real person's, and no consent: is noted"},
	} {
		if out, err := tts(c.placement, "x"); err == nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: %s", c.placement, out)
		}
	}
	if api.calls() != 1 {
		t.Errorf("a refused call reached the API (%d calls)", api.calls())
	}
	// 12 < 20: the second call runs; 24 ≥ 20: the third is refused.
	if out, err := tts("library", "s2"); err != nil {
		t.Fatalf("second: %s", out)
	}
	if out, err := tts("narrator", "s3"); err == nil || !strings.Contains(out, "connector video/elevenlabs is at its cap: 24 / 20 characters") {
		t.Errorf("over the cap: %s", out)
	}
	if api.calls() != 2 {
		t.Errorf("the API got %d calls, want 2", api.calls())
	}

	out, _ = runCLI(t, "connector", "exec", "video/elevenlabs", "--", "voices")
	assertContains(t, out, "voice narrator: Owner (clone) · the owner's voice, consent: 2026-11-21, own voice")
	assertContains(t, out, "voice actor-1: Actor · an actor's voice, consent missing (refused until noted)")
	assertContains(t, out, "voice library: VOICE3")
	out, _ = runCLI(t, "connectors", "video")
	assertContains(t, out, "video/elevenlabs (tool) · sends · spend 24 / 20 characters this month")
	assertContains(t, out, "  voice actor-1: Actor · an actor's voice, consent missing")
	assertContains(t, out, "  header xi-api-key: env:ACME_XI set")
	if strings.Contains(out, fakeXIKey) {
		t.Error("connectors printed the key")
	}
}
