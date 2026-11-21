package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// elevenUsage names every call a meter: elevenlabs connector takes.
func elevenUsage(addr string) string {
	return fmt.Sprintf(`usage: issue-cli connector exec %[1]s -- <call>
  tts --placement <p> (--text-file <f> | --text "…") --out <x.mp3> [--alignment <x.json>] [--model <id>]   speech, capped
  voices                                       the placements in voices:
  voices search [--language pl] [--gender female] [--use-case narration] [--accent <a>] [--search "<words>"] [--commercial] [--limit N] [--json]   the shared library, free
  music (--prompt "…" | --prompt-file <f>) --length-ms N --out <x.mp3> [--instrumental] [--model <id>]   a music bed, capped
The account's plan and credits left: issue-cli connector check %[1]s (free)`, addr)
}

// elevenAccess is the base URL and the key's headers of a meter:
// elevenlabs connector.
func elevenAccess(addr string, c *tracker.Connector) (string, http.Header, error) {
	base := c.URL
	if strings.TrimSpace(base) == "" {
		base = connectors.DefaultElevenLabsURL
	}
	if err := tracker.CheckConnectorURL(base); err != nil {
		return "", nil, fmt.Errorf("connector %s: %w", addr, err)
	}
	h, err := connectors.Headers(c)
	if err != nil {
		return "", nil, fmt.Errorf("connector %s: %w", addr, err)
	}
	return base, h, nil
}

// elevenErr is an ElevenLabs error for connector addr, a refused key named
// by its reference.
func elevenErr(addr string, c *tracker.Connector, err error) error {
	var ee *connectors.ElevenLabsError
	if errors.As(err, &ee) && ee.Refused() {
		return fmt.Errorf("connector %s: %v (the key in %s)", addr, err, restKeyRefs(c))
	}
	return fmt.Errorf("connector %s: %v", addr, err)
}

// runVoicesSearch is -- voices search: the shared voice library, free.
func runVoicesSearch(ctx *Context, addr string, c *tracker.Connector, args []string) error {
	fs := newFlagSet("connector exec "+addr+" -- voices search", ctx)
	var q connectors.VoiceQuery
	fs.StringVar(&q.Language, "language", "", "a language code (pl, en)")
	fs.StringVar(&q.Gender, "gender", "", "female, male or neutral")
	fs.StringVar(&q.UseCase, "use-case", "", "narration, conversational, social_media, advertisement, …")
	fs.StringVar(&q.Accent, "accent", "", "an accent")
	fs.StringVar(&q.Search, "search", "", "words in the voice's name or description")
	fs.BoolVar(&q.Commercial, "commercial", false, "only voices with no terms beyond the plan's (no custom rate, no live moderation)")
	fs.IntVar(&q.Limit, "limit", 20, "how many voices, at most 100")
	asJSON := fs.Bool("json", false, "print the voices as JSON")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &usageError{msg: elevenUsage(addr)}
	}
	base, h, err := elevenAccess(addr, c)
	if err != nil {
		return err
	}
	run, cancel := context.WithTimeout(context.Background(), connectors.CheckTimeout)
	defer cancel()
	vs, err := connectors.SharedVoices(run, connectors.RestClient(), base, h, q)
	if err != nil {
		return elevenErr(addr, c, err)
	}
	if *asJSON || ctx.JSONOutput {
		if vs == nil {
			vs = []connectors.SharedVoice{}
		}
		return writeJSON(ctx.Stdout, vs)
	}
	if len(vs) == 0 {
		fmt.Fprintf(ctx.Stdout, "no shared voice matches: try fewer filters (issue-cli connector exec %s -- voices search --help)\n", addr)
		return nil
	}
	for _, v := range vs {
		parts := []string{v.VoiceID, v.Name}
		for _, x := range []string{v.Accent, v.UseCase, strings.Trim(v.Gender+" "+v.Age, " ")} {
			if strings.TrimSpace(x) != "" {
				parts = append(parts, x)
			}
		}
		fmt.Fprintln(ctx.Stdout, strings.Join(parts, " · "))
		if v.PreviewURL != "" {
			fmt.Fprintf(ctx.Stdout, "  preview %s\n", v.PreviewURL)
		}
	}
	fmt.Fprintf(ctx.Stdout, "%d voices; listen to the previews, then the owner adds one to %s's voices: in projects.yaml (placement, voice_id, name). Free: nothing billed.\n", len(vs), addr)
	if q.Commercial {
		fmt.Fprintln(ctx.Stdout, "Commercial use of a library voice needs a paid ElevenLabs plan (issue-cli connector check "+addr+" shows the plan).")
	}
	return nil
}

// runMeteredMusic is -- music: one POST /v1/music, capped like tts.
func runMeteredMusic(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, args []string, caller connectors.Caller) error {
	fs := newFlagSet("connector exec "+addr+" -- music", ctx)
	prompt := fs.String("prompt", "", "what the music is (style, mood, tempo, instruments)")
	promptFile := fs.String("prompt-file", "", "the prompt, a UTF-8 file (instead of --prompt)")
	lengthMS := fs.Int("length-ms", 0, fmt.Sprintf("the length in milliseconds, %d to %d", connectors.MusicMinMS, connectors.MusicMaxMS))
	out := fs.String("out", "", "where the MP3 goes: the reel's folder")
	instrumental := fs.Bool("instrumental", false, "no vocals")
	model := fs.String("model", "", "the model id (default: ElevenLabs' default)")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if *out == "" || *lengthMS == 0 || (*prompt == "") == (*promptFile == "") || fs.NArg() > 0 {
		return &usageError{msg: elevenUsage(addr)}
	}
	if *lengthMS < connectors.MusicMinMS || *lengthMS > connectors.MusicMaxMS {
		return fmt.Errorf("--length-ms is %d to %d (3 s to 5 min)", connectors.MusicMinMS, connectors.MusicMaxMS)
	}
	words := *prompt
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return fmt.Errorf("--prompt-file: %v", err)
		}
		words = string(b)
	}
	if strings.TrimSpace(words) == "" {
		return fmt.Errorf("connector %s: the prompt is empty", addr)
	}
	if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
		return err
	}
	st := connectors.SpendFor(connectors.LoadSpend(ctx.Project), addr, c, ctx.now())
	est := connectors.MusicEstimate(*lengthMS)
	if room := st.Cap - st.Spent; float64(est) > room {
		return fmt.Errorf("connector %s: %d ms of music is about %d %s (ElevenLabs bills %d credits a minute) and the month has %s left of its cap (%s); ask for a shorter track, or the owner raises spend: cap: in projects.yaml", addr, *lengthMS, est, st.Unit, connectors.MusicCreditsPerMinute, connectors.Num(room), st.Words())
	}
	base, h, err := elevenAccess(addr, c)
	if err != nil {
		return err
	}
	unlock, err := lockEleven(ctx, addr)
	if err != nil {
		return err
	}
	defer unlock()
	if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
		return err
	}
	bal := readElevenBalance(ctx, base, h)
	if bal.err != nil {
		return elevenErr(addr, c, bal.err)
	}
	before := bal.before
	if before.Free() {
		return fmt.Errorf("connector %s: the account is on the %s plan: the Music API is for paid plans, and free-plan music is not cleared for commercial use; nothing was sent", addr, orWord(before.Tier, "free"))
	}
	run, cancel := context.WithTimeout(context.Background(), connectors.MusicTimeout)
	defer cancel()
	res, err := connectors.ComposeMusic(run, connectors.MusicClient(), connectors.MusicRequest{BaseURL: base, Header: h, Prompt: words, LengthMS: *lengthMS, Model: *model, Instrumental: *instrumental})
	if err != nil {
		// A refused or failed call bills nothing: no spend line.
		return elevenErr(addr, c, err)
	}
	rec := connectors.SpendRecord{Connector: addr, Date: ctx.now().UTC(), Month: connectors.Month(ctx.now()), Unit: c.Spend.UnitWords(), Call: "music", By: spendBy(caller), Ref: res.SongID}
	how, why := "the answer's character-cost", ""
	if res.CostKnown {
		rec.Amount = float64(res.Cost)
	} else {
		amount, after, w := bal.price(ctx)
		if w == "" {
			rec.Amount, rec.Source = amount, connectors.SourceBalance
			how = fmt.Sprintf("the subscription's count, %d → %d", before.Used, after)
		} else {
			rec.Unknown, why = true, w
		}
	}
	id, err := connectors.AppendSpendID(ctx.Project, rec)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "connector %s: the call's cost was not recorded: %v\n", addr, err)
	}
	if err := writeOutFile(*out, res.Audio); err != nil {
		return err
	}
	ref := fmt.Sprintf("%s plan (paid), commercial use, %s", before.Tier, ctx.now().Format("2006-01-02"))
	if res.SongID != "" {
		ref = "song " + res.SongID + ", " + ref
	}
	lic := musicLicence{Source: "ElevenLabs Music", Ref: ref}
	licPath := strings.TrimSuffix(*out, filepath.Ext(*out)) + ".licence.json"
	if err := writeOutFile(licPath, mustJSON(lic)); err != nil {
		return err
	}
	if rec.Unknown {
		fmt.Fprintf(ctx.Stdout, "✓ %s: music → %s (%d ms); its cost is unknown (no character-cost, and %s): the next billing call is held until it is recorded (issue-cli connector spend add %s <amount> --for %s; about %d %s)\n", addr, *out, *lengthMS, why, addr, id, est, rec.Unit)
	} else {
		fmt.Fprintf(ctx.Stdout, "✓ %s: music → %s (%d ms), %s %s (%s)\n", addr, *out, *lengthMS, connectors.Num(rec.Amount), rec.Unit, how)
	}
	fmt.Fprintf(ctx.Stdout, "licence → %s\n", licPath)
	fmt.Fprintf(ctx.Stdout, "storyboard: \"music\": {\"path\": %q, \"licence\": {\"source\": %q, \"ref\": %q}, \"duck\": true}\n", filepath.Base(*out), lic.Source, lic.Ref)
	fmt.Fprintf(ctx.Stdout, "legal lines: music: %s\n             music_licence: %s, %s\n", filepath.Base(*out), lic.Source, lic.Ref)
	return nil
}

// musicLicence is the <out>.licence.json beside a music file, the
// storyboard's music licence block.
type musicLicence struct {
	Source string `json:"source"`
	Ref    string `json:"ref"`
}

func mustJSON(v any) []byte {
	var b strings.Builder
	_ = writeJSON(&b, v)
	return []byte(b.String())
}

func orWord(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// ElevenLabs' subscription count moves some seconds after a call's answer:
// a call priced from it re-reads it every subscriptionPoll for up to
// subscriptionSettle.
var subscriptionSettle, subscriptionPoll = 30 * time.Second, 2 * time.Second

// elevenBalance is the subscription read before a billing call, under the
// connector's billing lock: what prices a call whose answer names no cost.
type elevenBalance struct {
	base   string
	header http.Header
	before connectors.Subscription
	err    error
	logLen int
}

// readElevenBalance reads the subscription before a call (free).
func readElevenBalance(ctx *Context, base string, h http.Header) elevenBalance {
	run, cancel := context.WithTimeout(context.Background(), connectors.CheckTimeout)
	defer cancel()
	s, err := connectors.GetSubscription(run, connectors.RestClient(), base, h)
	return elevenBalance{base: base, header: h, before: s, err: err, logLen: len(connectors.LoadSpend(ctx.Project))}
}

// price is the call's cost from the subscription's count before and
// after it; why says why it can't be, "" when amount is the cost.
func (b elevenBalance) price(ctx *Context) (amount float64, after int, why string) {
	if b.err != nil {
		return 0, 0, "the subscription before it could not be read: " + b.err.Error()
	}
	deadline := time.Now().Add(subscriptionSettle)
	var last connectors.Subscription
	var lastErr error
	read := false
	for {
		run, cancel := context.WithTimeout(context.Background(), connectors.CheckTimeout)
		s, err := connectors.GetSubscription(run, connectors.RestClient(), b.base, b.header)
		cancel()
		if err == nil {
			last, lastErr, read = s, nil, true
			if s.Used != b.before.Used {
				break
			}
		} else if !read {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(subscriptionPoll)
	}
	settle := connectors.Num(subscriptionSettle.Seconds()) + " s"
	switch {
	case !read:
		return 0, 0, "the subscription after it could not be read: " + lastErr.Error()
	case last.Used == b.before.Used:
		return 0, last.Used, fmt.Sprintf("the subscription's count did not move from %d within %s", b.before.Used, settle)
	case last.Used < b.before.Used:
		return 0, last.Used, fmt.Sprintf("the subscription's count went down (%d → %d), so its period reset meanwhile", b.before.Used, last.Used)
	case len(connectors.LoadSpend(ctx.Project)) != b.logLen:
		return 0, last.Used, "another entry reached the spend log during the call, so the count may hold more than this one"
	}
	return float64(last.Used - b.before.Used), last.Used, ""
}

// lockEleven takes the connector's billing lock: one billing call at a
// time, so the subscription's change prices only this one.
func lockEleven(ctx *Context, addr string) (func(), error) {
	unlock, err := connectors.LockBilling(ctx.Project, addr, func() {
		fmt.Fprintf(ctx.Stderr, "connector %s: another billing call is running: waiting for it to finish\n", addr)
	})
	if err != nil {
		return nil, fmt.Errorf("connector %s: the billing lock: %v", addr, err)
	}
	return unlock, nil
}
