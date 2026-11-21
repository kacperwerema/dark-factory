package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// spendStdoutMax is how much of a metered command's stdout is kept to read
// its cost from; the rest still passes through.
const spendStdoutMax = 8 << 20

// spendBy is how the spend log names caller.
func spendBy(caller connectors.Caller) string {
	switch {
	case caller.Issue != "":
		return caller.Issue
	case caller.Lead && caller.Department != "":
		return "lead:" + tracker.DepartmentKey(caller.Department)
	case caller.Lead:
		return "lead"
	}
	return "owner"
}

// spendRoom refuses a billing call of c when this month has no room.
func spendRoom(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, caller connectors.Caller) error {
	st := connectors.SpendFor(connectors.LoadSpend(ctx.Project), addr, c, ctx.now())
	return connectors.SpendError(addr, dept.Name, *st, caller.Lead || caller.Owner)
}

// runConnectorMetered is exec for a connector with spend:.
func runConnectorMetered(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, child []string, caller connectors.Caller) error {
	switch c.Spend.Meter {
	case tracker.MeterJSON:
		return runMeteredCommand(ctx, dept, addr, c, child, caller)
	case tracker.MeterElevenLabs:
		return runMeteredTTS(ctx, dept, addr, c, child, caller)
	}
	return fmt.Errorf("connector %s: spend: meter: %q is not %s (issue-cli health lint --check departments)", addr, c.Spend.Meter, strings.Join(tracker.SpendMeters, " or "))
}

// runMeteredCommand runs a meter: json connector's command; a billing call
// only with room under the cap, its cost read from its stdout after.
func runMeteredCommand(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, child []string, caller connectors.Caller) error {
	call, err := connectors.SpendCommand(addr, c, child)
	if err != nil {
		return err
	}
	// Refused before any secret is read; read again under the lock below.
	if call.Billing {
		if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
			return err
		}
	}
	env, err := connectors.Env(c, os.Environ())
	if err != nil {
		return err
	}
	if !call.Billing {
		code, _, err := runMeteredChild(ctx, call.Argv, env)
		if err != nil {
			return err
		}
		if code != 0 {
			return &exitCodeError{Code: code}
		}
		return nil
	}
	// One billing call at a time per connector: the room is read after
	// the call before it is priced, and a balance read before and after
	// this call prices only it.
	unlock, err := connectors.LockBilling(ctx.Project, addr, func() {
		fmt.Fprintf(ctx.Stderr, "connector %s: another billing call is running: waiting for it to finish\n", addr)
	})
	if err != nil {
		return fmt.Errorf("connector %s: the billing lock: %v", addr, err)
	}
	defer unlock()
	if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
		return err
	}
	balSub, balField := connectors.BalanceRead(c)
	var before float64
	var beforeErr error
	logLen := 0
	if balSub != "" {
		before, beforeErr = readBalance(c, env, ctx.Project.Root(), balSub, balField)
		logLen = len(connectors.LoadSpend(ctx.Project))
	}
	code, out, err := runMeteredChild(ctx, call.Argv, env)
	if err != nil {
		return err
	}
	rec := connectors.SpendRecord{Connector: addr, Date: ctx.now().UTC(), Month: connectors.Month(ctx.now()), Unit: c.Spend.UnitWords(),
		Call: call.Sub, By: spendBy(caller), Exit: code, Ref: connectors.RefFromJSON(out)}
	cost, ok := connectors.CostFromJSON(out, c.Spend.Field)
	why := ""
	switch {
	case ok:
		rec.Amount = cost
	case code != 0:
		// A failed call that named no cost records 0 and holds nothing.
	case balSub == "":
		why = "the connector has no balance read (spend: balance:)"
	case beforeErr != nil:
		why = "the balance before it could not be read: " + beforeErr.Error()
	default:
		after, afterErr := readBalance(c, env, ctx.Project.Root(), balSub, balField)
		switch {
		case afterErr != nil:
			why = "the balance after it could not be read: " + afterErr.Error()
		case after > before:
			why = fmt.Sprintf("the balance went up (%s → %s), so it was topped up or refunded meanwhile", connectors.Num(before), connectors.Num(after))
		case len(connectors.LoadSpend(ctx.Project)) != logLen:
			why = "another entry reached the spend log during the call, so the balance may count more than this one"
		default:
			rec.Amount, rec.Source = before-after, connectors.SourceBalance
		}
	}
	if why != "" {
		// A call that worked and named no cost may have billed: hold the
		// next one until someone records what it cost.
		rec.Unknown = true
	}
	id, err := connectors.AppendSpendID(ctx.Project, rec)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "connector %s: the call's cost was not recorded: %v\n", addr, err)
	}
	switch {
	case rec.Unknown:
		fmt.Fprintf(ctx.Stderr, "connector %s: the output named no %s and %s, so this call's cost is unknown: the next billing call is held until it is recorded (issue-cli connector spend add %s <amount> --for %s)\n", addr, c.Spend.Field, why, addr, id)
	case rec.Source == connectors.SourceBalance:
		fmt.Fprintf(ctx.Stderr, "connector %s: the output named no %s: priced from the balance, %s → %s %s: %s %s (entry %s)\n", addr, c.Spend.Field, connectors.Num(before), connectors.Num(before-rec.Amount), c.Spend.UnitWords(), connectors.Num(rec.Amount), c.Spend.UnitWords(), id)
	}
	if code != 0 {
		return &exitCodeError{Code: code}
	}
	return nil
}

// runMeteredChild runs a metered connector's command with stdout streamed
// through, returning its exit code and the first spendStdoutMax bytes of
// its stdout. err is a command that could not run.
func runMeteredChild(ctx *Context, argv, env []string) (code int, out []byte, err error) {
	run, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	cmd := exec.CommandContext(run, argv[0], argv[1:]...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	cmd.Dir = ctx.Project.Root()
	kept := &connectors.CappedBuffer{Max: spendStdoutMax}
	cmd.Stdin, cmd.Stderr = ctx.Stdin, ctx.Stderr
	cmd.Stdout = io.MultiWriter(ctx.Stdout, kept)
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	default:
		return 0, nil, runErr
	}
	return code, kept.Bytes(), nil
}

// balanceTimeout bounds one balance read.
const balanceTimeout = 60 * time.Second

// readBalance runs c's free balance subcommand and reads field from its
// JSON stdout; its output is kept to itself.
func readBalance(c *tracker.Connector, env []string, dir, sub, field string) (float64, error) {
	run, cancel := context.WithTimeout(context.Background(), balanceTimeout)
	defer cancel()
	argv := append(append([]string{strings.TrimSpace(c.Command)}, c.Args...), sub)
	cmd := exec.CommandContext(run, argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
	kept := &connectors.CappedBuffer{Max: 1 << 20}
	cmd.Stdout = kept
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%s %s: %v", filepath.Base(argv[0]), sub, err)
	}
	n, err := connectors.BalanceFromJSON(kept.Bytes(), field)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %v", filepath.Base(argv[0]), sub, err)
	}
	return n, nil
}

// runMeteredTTS is exec for a meter: elevenlabs connector: tts or voices.
func runMeteredTTS(ctx *Context, dept *tracker.Department, addr string, c *tracker.Connector, child []string, caller connectors.Caller) error {
	usage := elevenUsage(addr)
	if len(child) == 0 {
		return &usageError{msg: usage}
	}
	switch child[0] {
	case "voices":
		if len(child) > 1 && child[1] == "search" {
			return runVoicesSearch(ctx, addr, c, child[2:])
		}
		if len(child) > 1 {
			return &usageError{msg: usage}
		}
		return printVoices(ctx, addr, c)
	case "music":
		return runMeteredMusic(ctx, dept, addr, c, child[1:], caller)
	case "tts":
	default:
		return &usageError{msg: usage}
	}
	fs := newFlagSet("connector exec "+addr+" -- tts", ctx)
	placement := fs.String("placement", "", "the voice's placement in the connector's voices:")
	textFile := fs.String("text-file", "", "the text to speak, a UTF-8 file")
	text := fs.String("text", "", "the text to speak (instead of --text-file)")
	out := fs.String("out", "", "where the MP3 goes")
	alignment := fs.String("alignment", "", "where the character timings go (ElevenLabs' alignment JSON)")
	model := fs.String("model", "", "the model id (default: the voice's model:, else ElevenLabs' default)")
	if err := parseFlagsArgs(ctx, fs, child[1:]); err != nil {
		return err
	}
	if *placement == "" || *out == "" || (*textFile == "") == (*text == "") {
		return &usageError{msg: usage}
	}
	v := c.VoiceFor(*placement)
	if v == nil {
		var names []string
		for _, x := range c.Voices {
			names = append(names, x.Placement)
		}
		have := "none: add voices: to the connector in projects.yaml"
		if len(names) > 0 {
			have = strings.Join(names, ", ")
		}
		return fmt.Errorf("connector %s has no voice at placement %q (it has: %s)", addr, *placement, have)
	}
	if strings.TrimSpace(v.VoiceID) == "" {
		return fmt.Errorf("connector %s: the voice at %s has no voice_id: in projects.yaml", addr, v.Placement)
	}
	if v.NeedsConsent() {
		return fmt.Errorf("connector %s: the voice at %s is %s's, a real person's, and no consent: is noted for it; the owner notes where their consent is kept (consent: on the voice in projects.yaml) before it is used", addr, v.Placement, v.RealPerson)
	}
	words := *text
	if *textFile != "" {
		b, err := os.ReadFile(*textFile)
		if err != nil {
			return fmt.Errorf("--text-file: %v", err)
		}
		words = string(b)
	}
	if strings.TrimSpace(words) == "" {
		return fmt.Errorf("connector %s: the text is empty", addr)
	}
	if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
		return err
	}
	base, h, err := elevenAccess(addr, c)
	if err != nil {
		return err
	}
	m := *model
	if m == "" {
		m = v.Model
	}
	unlock, err := lockEleven(ctx, addr)
	if err != nil {
		return err
	}
	defer unlock()
	if err := spendRoom(ctx, dept, addr, c, caller); err != nil {
		return err
	}
	// Read before the call: it prices one whose answer has no
	// character-cost. A failed read only costs that fallback.
	bal := readElevenBalance(ctx, base, h)
	res, err := connectors.TTS(context.Background(), connectors.TTSClient(), connectors.TTSRequest{BaseURL: base, Header: h, VoiceID: v.VoiceID, Model: m, Text: words})
	if err != nil {
		return fmt.Errorf("connector %s: %v", addr, err)
	}
	rec := connectors.SpendRecord{Connector: addr, Date: ctx.now().UTC(), Month: connectors.Month(ctx.now()), Unit: c.Spend.UnitWords(),
		Amount: float64(res.Characters), Call: "tts", By: spendBy(caller)}
	if !res.CostKnown {
		if amount, _, why := bal.price(ctx); why == "" {
			rec.Amount, rec.Source = amount, connectors.SourceBalance
		} else {
			fmt.Fprintf(ctx.Stderr, "connector %s: the answer named no character-cost and %s: recorded the text's %d characters\n", addr, why, res.Characters)
		}
	}
	if err := connectors.AppendSpend(ctx.Project, rec); err != nil {
		fmt.Fprintf(ctx.Stderr, "connector %s: the call's cost was not recorded: %v\n", addr, err)
	}
	if err := writeOutFile(*out, res.Audio); err != nil {
		return err
	}
	if *alignment != "" && len(res.Alignment) > 0 {
		if err := writeOutFile(*alignment, append(res.Alignment, '\n')); err != nil {
			return err
		}
	}
	fmt.Fprintf(ctx.Stdout, "✓ %s: %s (%s) → %s, %s %s", addr, v.Placement, voiceName(*v), *out, connectors.Num(rec.Amount), rec.Unit)
	if *alignment != "" {
		fmt.Fprintf(ctx.Stdout, ", timings → %s", *alignment)
	}
	fmt.Fprintln(ctx.Stdout)
	return nil
}

func writeOutFile(path string, b []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0644)
}

func voiceName(v tracker.Voice) string {
	if v.Name != "" {
		return v.Name
	}
	return v.VoiceID
}

// printVoices lists c's voices, each real person's consent noted or not.
func printVoices(ctx *Context, addr string, c *tracker.Connector) error {
	if len(c.Voices) == 0 {
		fmt.Fprintf(ctx.Stdout, "connector %s has no voices: (placement, voice_id, name, real_person, consent) in projects.yaml\n", addr)
		return nil
	}
	for _, v := range c.Voices {
		fmt.Fprintln(ctx.Stdout, voiceLine(v))
	}
	return nil
}

// voiceLine is one voice for a listing.
func voiceLine(v tracker.Voice) string {
	line := fmt.Sprintf("voice %s: %s", v.Placement, voiceName(v))
	switch {
	case v.NeedsConsent():
		line += " · " + v.RealPerson + "'s voice, consent missing (refused until noted)"
	case strings.TrimSpace(v.RealPerson) != "":
		line += " · " + v.RealPerson + "'s voice, consent: " + v.Consent
	}
	return line
}

// connectorSpendUsage is connector spend's forms.
const connectorSpendUsage = `usage: issue-cli connector spend [<dept>[/<name>]] [--json]
       issue-cli connector spend add <dept>/<name> <amount> [--for <entry-id> | --ref <generation id> | --outside] [--note "..."]
       issue-cli connector spend remove <entry-id> --reason "..."`

// runConnectorSpendCmd is connector spend: the month's entries, add or
// remove.
func runConnectorSpendCmd(ctx *Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return runConnectorSpendAdd(ctx, args[1:])
		case "remove":
			return runConnectorSpendRemove(ctx, args[1:])
		}
	}
	return runConnectorSpendList(ctx, args)
}

// spendWriter refuses a hand record on dept's connector from anyone but
// the owner or dept's lead.
func spendWriter(p *tracker.Project, dept *tracker.Department, verb string) (connectors.Caller, error) {
	caller := connectorCaller(p, true)
	if caller.Agent && !(caller.Lead && strings.EqualFold(caller.Department, dept.Name)) {
		return caller, fmt.Errorf("connector spend %s is for the owner or %s's lead (%s)", verb, dept.Name, tracker.DepartmentLeadName(p, dept.Name))
	}
	return caller, nil
}

// spendEntryWords is one entry for a line: "s-1a2b3c 2026-11-21 18:04
// video/kling text_to_video 15 credits by reels/clip (from the balance)".
func spendEntryWords(r connectors.SpendRecord) string {
	w := r.ID + "  " + r.Date.Local().Format("2006-01-02 15:04") + "  " + r.Connector + "  "
	switch {
	case r.Manual:
		w += "by hand"
	case r.Call != "":
		w += r.Call
	default:
		w += "call"
	}
	w += "  "
	if r.Unknown {
		w += "cost unknown"
	} else {
		w += connectors.Num(r.Amount) + " " + r.Unit
	}
	if r.By != "" {
		w += "  by " + r.By
	}
	var notes []string
	if r.Source == connectors.SourceBalance {
		notes = append(notes, "from the balance")
	}
	if r.Outside {
		notes = append(notes, "outside the engine")
	}
	if r.Exit != 0 {
		notes = append(notes, fmt.Sprintf("exit %d", r.Exit))
	}
	if r.For != "" {
		notes = append(notes, "for "+r.For)
	}
	if r.Ref != "" {
		notes = append(notes, "ref "+r.Ref)
	}
	if r.Note != "" {
		notes = append(notes, r.Note)
	}
	if len(notes) > 0 {
		w += " (" + strings.Join(notes, "; ") + ")"
	}
	return w
}

// runConnectorSpendList is connector spend [<dept>[/<name>]]: this month's
// entries with their ids, removed ones marked.
func runConnectorSpendList(ctx *Context, args []string) error {
	fs := newFlagSet("connector spend", ctx)
	asJSON := fs.Bool("json", false, "print the entries as JSON")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return &usageError{msg: connectorSpendUsage}
	}
	p := ctx.Project
	match := func(string) bool { return true }
	if fs.NArg() == 1 {
		want := fs.Arg(0)
		if strings.Contains(want, "/") {
			dept, c, err := connectors.Find(p, want)
			if err != nil {
				return err
			}
			addr := tracker.ConnectorAddress(dept.Name, c.Name)
			match = func(a string) bool { return a == addr }
		} else {
			d := p.DepartmentByKey(want)
			if d == nil {
				return fmt.Errorf("no department %q (departments: %s)", want, strings.Join(p.DepartmentKeys(), ", "))
			}
			prefix := tracker.DepartmentKey(d.Name) + "/"
			match = func(a string) bool { return strings.HasPrefix(a, prefix) }
		}
	}
	month := connectors.Month(ctx.now())
	all := connectors.LoadSpend(p)
	gone := connectors.Removed(all)
	type entry struct {
		connectors.SpendRecord
		RemovedBy  string `json:"removed_by,omitempty"`
		RemovedWhy string `json:"removed_why,omitempty"`
	}
	var list []entry
	for _, r := range all {
		if r.Removes != "" || r.Month != month || !match(r.Connector) {
			continue
		}
		e := entry{SpendRecord: r}
		if rm, ok := gone[r.ID]; ok {
			e.RemovedBy, e.RemovedWhy = rm.ID, rm.Note
		}
		list = append(list, e)
	}
	if *asJSON {
		if list == nil {
			list = []entry{}
		}
		return writeJSON(ctx.Stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintf(ctx.Stdout, "no spend entries in %s\n", month)
		return nil
	}
	for _, e := range list {
		line := spendEntryWords(e.SpendRecord)
		if e.RemovedBy != "" {
			line += "  removed (" + e.RemovedBy + ": " + e.RemovedWhy + ")"
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
	return nil
}

// runConnectorSpendAdd is connector spend add: an amount recorded by hand,
// for one call (the held one by default) or spent outside the engine.
func runConnectorSpendAdd(ctx *Context, args []string) error {
	fs := newFlagSet("connector spend add", ctx)
	note := fs.String("note", "", "why, in a few words")
	forID := fs.String("for", "", "the entry id of the call this prices (connector spend lists them)")
	ref := fs.String("ref", "", "the vendor's id of the call this prices (Kling's generationId)")
	outside := fs.Bool("outside", false, "spend made outside the engine, for no call it ran")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return &usageError{msg: connectorSpendUsage}
	}
	if *outside && (*forID != "" || *ref != "") {
		return &usageError{msg: "--outside is spend for no call: leave --for and --ref out\n" + connectorSpendUsage}
	}
	p := ctx.Project
	dept, c, err := connectors.Find(p, fs.Arg(0))
	if err != nil {
		return err
	}
	addr := tracker.ConnectorAddress(dept.Name, c.Name)
	if c.Spend == nil {
		return fmt.Errorf("connector %s has no spend: block: nothing to record", addr)
	}
	amount, err := strconv.ParseFloat(fs.Arg(1), 64)
	if err != nil || amount < 0 {
		return fmt.Errorf("the amount is a number of %s, 0 or more", c.Spend.UnitWords())
	}
	caller, err := spendWriter(p, dept, "add")
	if err != nil {
		return err
	}
	recs := connectors.LoadSpend(p)
	rec := connectors.SpendRecord{Connector: addr, Date: ctx.now().UTC(), Month: connectors.Month(ctx.now()), Unit: c.Spend.UnitWords(),
		Amount: amount, By: spendBy(caller), Manual: true, Note: *note, For: *forID, Ref: strings.TrimSpace(*ref), Outside: *outside}
	if !*outside {
		call, err := spendCallPriced(recs, addr, rec)
		if err != nil {
			return err
		}
		if call != nil {
			rec.For = call.ID
			if rec.Ref == "" {
				rec.Ref = call.Ref
			}
		}
	}
	id, err := connectors.AppendSpendID(p, rec)
	if err != nil {
		return err
	}
	st := connectors.SpendFor(connectors.LoadSpend(p), addr, c, ctx.now())
	what := ""
	if rec.For != "" {
		what = " for " + rec.For
	}
	fmt.Fprintf(ctx.Stdout, "✓ recorded %s %s on %s%s (entry %s): %s\n", connectors.Num(amount), c.Spend.UnitWords(), addr, what, id, st.Words())
	return nil
}

// spendCallPriced is the billing entry a hand record prices: the one --for
// names, the one with --ref's id, else the held unknown call; nil for a
// --ref no call carries, or no call to price. A call that already has a
// price, or a cost of its own, is refused naming it.
func spendCallPriced(recs []connectors.SpendRecord, addr string, rec connectors.SpendRecord) (*connectors.SpendRecord, error) {
	live := connectors.Live(recs)
	var call *connectors.SpendRecord
	switch {
	case rec.For != "":
		call = connectors.FindSpend(live, rec.For)
		if call == nil || call.Connector != addr || call.Manual {
			return nil, fmt.Errorf("no billing call %s of %s (issue-cli connector spend %s lists the entries)", rec.For, addr, addr)
		}
	case rec.Ref != "":
		for i := len(live) - 1; i >= 0; i-- {
			if r := live[i]; r.Connector == addr && !r.Manual && r.Ref == rec.Ref {
				call = &live[i]
				break
			}
		}
		if call == nil {
			// A call the engine didn't run: one hand record per ref.
			if prev := connectors.PricedBy(recs, rec); prev != nil {
				return nil, spendDuplicate(addr, rec.Ref, *prev)
			}
			return nil, nil
		}
	default:
		// The newest billing call, if its cost is unknown.
		for i := len(live) - 1; i >= 0; i-- {
			if r := live[i]; r.Connector == addr && !r.Manual {
				if r.Unknown {
					call = &live[i]
				}
				break
			}
		}
		if call == nil {
			return nil, nil
		}
		if prev := connectors.PricedBy(recs, *call); prev != nil {
			return nil, fmt.Errorf("the last billing call of %s (%s) is already priced: %s; to record spend made outside the engine, pass --outside (issue-cli connector spend remove %s --reason \"...\" takes the first one back)", addr, call.ID, spendEntryWords(*prev), prev.ID)
		}
		return call, nil
	}
	if !call.Unknown {
		return nil, fmt.Errorf("billing call %s of %s already has its cost: %s; nothing to add (issue-cli connector spend remove %s --reason \"...\" takes it back)", call.ID, addr, spendEntryWords(*call), call.ID)
	}
	if prev := connectors.PricedBy(recs, *call); prev != nil {
		return nil, spendDuplicate(addr, call.ID, *prev)
	}
	return call, nil
}

func spendDuplicate(addr, what string, prev connectors.SpendRecord) error {
	return fmt.Errorf("%s on %s is already priced: %s; a second entry would count it twice (issue-cli connector spend remove %s --reason \"...\" takes the first one back)", what, addr, spendEntryWords(prev), prev.ID)
}

// runConnectorSpendRemove is connector spend remove: a removal line that
// takes one entry off the month's total and its hold.
func runConnectorSpendRemove(ctx *Context, args []string) error {
	fs := newFlagSet("connector spend remove", ctx)
	reason := fs.String("reason", "", "why the entry is wrong, in a few words")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*reason) == "" {
		return &usageError{msg: connectorSpendUsage}
	}
	p := ctx.Project
	recs := connectors.LoadSpend(p)
	id := fs.Arg(0)
	r := connectors.FindSpend(recs, id)
	switch {
	case r == nil:
		return fmt.Errorf("no spend entry %s (issue-cli connector spend lists them)", id)
	case r.Removes != "":
		return fmt.Errorf("%s is a removal (of %s), not an entry to remove", id, r.Removes)
	}
	if rm, ok := connectors.Removed(recs)[id]; ok {
		return fmt.Errorf("%s is already removed by %s (%s)", id, rm.ID, rm.Note)
	}
	dept, c, err := connectors.Find(p, r.Connector)
	if err != nil {
		return err
	}
	caller, err := spendWriter(p, dept, "remove")
	if err != nil {
		return err
	}
	rm := connectors.SpendRecord{Connector: r.Connector, Date: ctx.now().UTC(), Month: r.Month, Unit: r.Unit,
		By: spendBy(caller), Manual: true, Removes: id, Note: strings.TrimSpace(*reason)}
	rmID, err := connectors.AppendSpendID(p, rm)
	if err != nil {
		return err
	}
	msg := "✓ removed " + spendEntryWords(*r) + " (" + rmID + ")"
	if st := connectors.SpendFor(connectors.LoadSpend(p), r.Connector, c, ctx.now()); st != nil {
		msg += ": " + st.Words()
	}
	fmt.Fprintln(ctx.Stdout, msg)
	return nil
}
