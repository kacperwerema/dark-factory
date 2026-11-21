package connectors

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A connector with spend: is a paid tool. connector exec records what each
// billing call cost in <issues>/.connectors/spend.jsonl, next to the run
// and check logs, and refuses a billing call while this calendar month's
// spend (local time) is at or over the connector's cap:, while no cap is
// set, or while the last call's cost is unknown. The cost of a call is
// known only after it, so the call that crosses the cap runs; the next one
// is refused. Like the rest of connector exec this guards against
// mistakes and is not a security boundary.

// SpendRecord is one line of the spend log: what one call cost, or an
// amount recorded by hand (Manual). It never holds a prompt, a text or a
// secret.
type SpendRecord struct {
	// ID names the entry (s-1a2b3c) for spend add --for and spend remove.
	// A line written before ids has none in the file: LoadSpend gives it
	// one from a hash of the line, the same on every read.
	ID        string    `json:"id,omitempty"`
	Connector string    `json:"connector"`
	Date      time.Time `json:"date"`
	// Month is the calendar month the call counts in, local time: 2026-11.
	Month  string  `json:"month"`
	Unit   string  `json:"unit"`
	Amount float64 `json:"amount"`
	// Call is the subcommand (text_to_video, tts).
	Call string `json:"call,omitempty"`
	// By is the caller: its issue's slug, lead:<dept> or owner.
	By   string `json:"by,omitempty"`
	Exit int    `json:"exit"`
	// Unknown is a billing call whose output named no cost: the next
	// billing call is held until an amount is recorded by hand.
	Unknown bool   `json:"unknown,omitempty"`
	Manual  bool   `json:"manual,omitempty"`
	Note    string `json:"note,omitempty"`
	// Source is how a billing call's amount was read: "" from its output,
	// "balance" from the account balance before and after it.
	Source string `json:"source,omitempty"`
	// Ref is the vendor's id for the call (Kling's generationId) on a
	// billing entry, or the call a hand record prices (--ref).
	Ref string `json:"ref,omitempty"`
	// For is the entry a hand record prices (an unknown call's id).
	For string `json:"for,omitempty"`
	// Outside is a hand record of spend made outside the engine: it prices
	// no call, so it lifts no hold.
	Outside bool `json:"outside,omitempty"`
	// Removes is a removal line: it takes the entry with this id off the
	// month's total and its hold. Note says why. Entries are never edited
	// in place.
	Removes string `json:"removes,omitempty"`
}

// SourceBalance is a billing call priced from the balance before and after.
const SourceBalance = "balance"

// newSpendID is a fresh entry id.
func newSpendID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return "s-" + hex.EncodeToString(b)
}

// lineID is the id of a line written before ids.
func lineID(line []byte) string {
	h := sha256.Sum256(bytes.TrimSpace(line))
	return "s-" + hex.EncodeToString(h[:3])
}

// SpendLogPath is the spend log, with the work files.
func SpendLogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".connectors", "spend.jsonl")
}

// Month is t's calendar month in local time, as the log keys it.
func Month(t time.Time) string {
	return t.Local().Format("2006-01")
}

// AppendSpend adds r to the spend log, giving it an id when it has none.
func AppendSpend(p *tracker.Project, r SpendRecord) error {
	_, err := AppendSpendID(p, r)
	return err
}

// AppendSpendID is AppendSpend returning the entry's id.
func AppendSpendID(p *tracker.Project, r SpendRecord) (string, error) {
	if r.ID == "" {
		r.ID = newSpendID()
	}
	return r.ID, appendSpend(p, r)
}

func appendSpend(p *tracker.Project, r SpendRecord) error {
	path := SpendLogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadSpend is the spend log in file order. Lines that don't parse are
// skipped.
func LoadSpend(p *tracker.Project) []SpendRecord {
	f, err := os.Open(SpendLogPath(p))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []SpendRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r SpendRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Connector != "" {
			if r.ID == "" {
				r.ID = lineID(sc.Bytes())
			}
			out = append(out, r)
		}
	}
	return out
}

// Removed is the removal line of each removed entry, by the entry's id.
func Removed(recs []SpendRecord) map[string]SpendRecord {
	out := map[string]SpendRecord{}
	for _, r := range recs {
		if r.Removes != "" {
			if _, seen := out[r.Removes]; !seen {
				out[r.Removes] = r
			}
		}
	}
	return out
}

// Live is recs without removal lines and the entries they remove.
func Live(recs []SpendRecord) []SpendRecord {
	gone := Removed(recs)
	var out []SpendRecord
	for _, r := range recs {
		if r.Removes != "" {
			continue
		}
		if _, ok := gone[r.ID]; ok {
			continue
		}
		out = append(out, r)
	}
	return out
}

// FindSpend is the entry with id in recs, nil when there is none.
func FindSpend(recs []SpendRecord, id string) *SpendRecord {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

// PricedBy is the live hand record that already prices call (an entry
// of the same connector whose For is call's id, or whose Ref is call's
// Ref), nil when none does.
func PricedBy(recs []SpendRecord, call SpendRecord) *SpendRecord {
	live := Live(recs)
	for i, r := range live {
		if !r.Manual || r.Connector != call.Connector {
			continue
		}
		if r.For == call.ID || (call.Ref != "" && r.Ref == call.Ref) {
			return &live[i]
		}
	}
	return nil
}

// SpendState is a metered connector this month.
type SpendState struct {
	Month string  `json:"month"`
	Spent float64 `json:"spent"`
	Cap   float64 `json:"cap"`
	Unit  string  `json:"unit"`
	Calls int     `json:"calls"`
	// Unknown is set while the newest call's cost is unknown and no amount
	// was recorded after it.
	Unknown bool `json:"unknown,omitempty"`
}

// SpendFor is connector addr's spend this month, from recs; nil when c
// has no spend:.
func SpendFor(recs []SpendRecord, addr string, c *tracker.Connector, now time.Time) *SpendState {
	if c.Spend == nil {
		return nil
	}
	st := &SpendState{Month: Month(now), Cap: c.Spend.Cap, Unit: c.Spend.UnitWords()}
	for _, r := range Live(recs) {
		if r.Connector != addr {
			continue
		}
		if r.Month == st.Month {
			st.Spent += r.Amount
			if !r.Manual {
				st.Calls++
			}
		}
		switch {
		case r.Manual && !r.Outside:
			st.Unknown = false
		case r.Manual:
		case r.Unknown:
			st.Unknown = true
		case r.Exit == 0:
			st.Unknown = false
		}
	}
	return st
}

// Num prints an amount without a needless fraction: 51, 0.5.
func Num(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Words is the state for a line: "120 / 500 credits this month (2026-11)".
func (s SpendState) Words() string {
	w := Num(s.Spent)
	if s.Cap > 0 {
		w += " / " + Num(s.Cap)
	}
	w += " " + s.Unit + " this month (" + s.Month + ")"
	if s.Cap <= 0 {
		w += ", no cap set"
	}
	if s.Unknown {
		w += ", last call's cost unknown"
	}
	return w
}

// AtCap is a state whose billing calls are refused.
func (s SpendState) AtCap() bool {
	return s.Cap <= 0 || s.Spent >= s.Cap || s.Unknown
}

// Problem is the state's trouble in a few words, "" when it has room.
func (s SpendState) Problem() string {
	switch {
	case s.Cap <= 0:
		return "no spend cap set"
	case s.Unknown:
		return "last call's cost unknown"
	case s.Spent >= s.Cap:
		return fmt.Sprintf("at its spend cap (%s / %s %s)", Num(s.Spent), Num(s.Cap), s.Unit)
	}
	return ""
}

// SpendError refuses a billing call of connector addr in department dept
// when st leaves no room: nil when it has some. lead is the caller being
// the department's lead (who then asks the owner, not their lead).
func SpendError(addr, dept string, st SpendState, lead bool) error {
	who := "ask your lead to ask the owner"
	if lead {
		who = "ask the owner"
	}
	key := strings.SplitN(addr, "/", 2)[0]
	switch {
	case st.Cap <= 0:
		return fmt.Errorf("connector %s spends money and has no cap: nothing billed is run until the owner writes spend: cap: (%s a month) on it in projects.yaml, %s's entry; %s (docs/departments.md#spend-caps)", addr, st.Unit, dept, who)
	case st.Unknown:
		return fmt.Errorf("connector %s is held: its last billing call's cost is unknown (the output named none), so the cap can't be counted; read the cost (kling account, or the vendor's usage page) and record it: issue-cli connector spend add %s <amount> --note \"...\" (the owner or %s's lead); %s", addr, addr, dept, who)
	case st.Spent >= st.Cap:
		return fmt.Errorf("connector %s is at its cap: %s / %s %s spent in %s; nothing billed is run until next month or until the owner raises spend: cap: in projects.yaml (%s's entry); %s. Free calls still run (issue-cli connectors %s)", addr, Num(st.Spent), Num(st.Cap), st.Unit, st.Month, dept, who, key)
	}
	return nil
}

// SpendCall is one call of a meter: json connector: the command run, its
// subcommand, and whether it bills.
type SpendCall struct {
	Argv    []string
	Sub     string
	Billing bool
}

// SpendCommand is what exec runs for a meter: json connector given args
// (the words after --): always c's own command, args being its arguments;
// a first word naming the command itself is dropped. A refuse:d
// subcommand is an error; --help or --version, or a free: subcommand,
// doesn't bill. Any other subcommand does, so one the config doesn't know
// is capped.
func SpendCommand(addr string, c *tracker.Connector, args []string) (SpendCall, error) {
	name := strings.TrimSpace(c.Command)
	if name == "" {
		return SpendCall{}, fmt.Errorf("connector %s has no command: in projects.yaml: a metered connector runs only its own command", addr)
	}
	if len(args) > 0 && (args[0] == name || filepath.Base(args[0]) == filepath.Base(name)) {
		args = args[1:]
	}
	call := SpendCall{Argv: append(append([]string{name}, c.Args...), args...)}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			call.Sub = a
			break
		}
	}
	help := slices.ContainsFunc(args, func(a string) bool {
		return a == "--help" || a == "-h" || a == "--version" || a == "-v"
	})
	switch {
	case call.Sub != "" && slices.Contains(c.Spend.Refuse, call.Sub):
		return SpendCall{}, fmt.Errorf("connector %s does not run %s: it is the owner's, in their own shell (spend: refuse:)", addr, call.Sub)
	case help, call.Sub == "", slices.Contains(c.Spend.Free, call.Sub):
		call.Billing = false
	default:
		call.Billing = true
	}
	if call.Sub == "" && !help {
		return SpendCall{}, fmt.Errorf("connector %s: pass %s's subcommand and arguments after -- (issue-cli connector exec %s -- --help)", addr, filepath.Base(name), addr)
	}
	return call, nil
}

// BalanceRead is the subcommand and key a meter: json connector's balance
// is read with: spend: balance: and balance_field:, else account and
// availableRemainCredits for the Kling CLI; "" when it has none.
func BalanceRead(c *tracker.Connector) (sub, field string) {
	if c.Spend == nil || c.Spend.Meter != tracker.MeterJSON {
		return "", ""
	}
	sub, field = strings.TrimSpace(c.Spend.Balance), strings.TrimSpace(c.Spend.BalanceField)
	if filepath.Base(strings.TrimSpace(c.Command)) == "kling" {
		if sub == "" {
			sub = "account"
		}
		if field == "" {
			field = "availableRemainCredits"
		}
	}
	if sub == "" || field == "" {
		return "", ""
	}
	return sub, field
}

// BalanceFromJSON is the balance an account call's stdout names under
// field: one number, however often it repeats. Several different numbers
// (a balance per pack, say) or none is an error: the difference of either
// could misprice the call.
func BalanceFromJSON(out []byte, field string) (float64, error) {
	var vals []float64
	walkJSON(out, func(k string, v any) bool {
		if k != field {
			return false
		}
		n, ok := number(v)
		if ok && !slices.Contains(vals, n) {
			vals = append(vals, n)
		}
		return ok
	})
	switch len(vals) {
	case 0:
		return 0, fmt.Errorf("no %s in its output", field)
	case 1:
		return vals[0], nil
	}
	return 0, fmt.Errorf("%d different %s values in its output", len(vals), field)
}

// refFields are the keys a call's vendor id is under (Kling's generationId).
var refFields = []string{"generationId", "taskId", "task_id"}

// RefFromJSON is the first vendor id a call's stdout names, "" when none.
func RefFromJSON(out []byte) string {
	ref := ""
	walkJSON(out, func(k string, v any) bool {
		if s, ok := v.(string); ok && ref == "" && slices.Contains(refFields, k) && strings.TrimSpace(s) != "" {
			ref = strings.TrimSpace(s)
			return true
		}
		return false
	})
	return ref
}

// jsonDocs is stdout as one JSON value, else its lines that parse.
func jsonDocs(out []byte) []any {
	var docs []any
	var v any
	if json.Unmarshal(bytes.TrimSpace(out), &v) == nil {
		return append(docs, v)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		var lv any
		if json.Unmarshal(bytes.TrimSpace(line), &lv) == nil {
			docs = append(docs, lv)
		}
	}
	return docs
}

// walkJSON calls see on every key and value in stdout's JSON, in key
// order; a value see returns true for is not walked into.
func walkJSON(out []byte, see func(k string, v any) bool) {
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if !see(k, t[k]) {
					walk(t[k])
				}
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	for _, d := range jsonDocs(out) {
		walk(d)
	}
}

// LockBilling takes connector addr's billing lock in this project, so two
// billing calls never overlap and a balance read before and after one
// prices only it. It waits for a call holding it, calling waiting once
// first; unlock releases it.
func LockBilling(p *tracker.Project, addr string, waiting func()) (unlock func(), err error) {
	dir := filepath.Join(p.IssueDir, ".connectors")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	name := strings.NewReplacer("/", "-", string(filepath.Separator), "-").Replace(addr)
	f, err := os.OpenFile(filepath.Join(dir, name+".billing.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if waiting != nil {
			waiting()
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// CostFromJSON is the cost a call's stdout names: the largest number under
// a key named field anywhere in its JSON (a poll's final answer may repeat
// the submission's, so values are not added up). A stdout that is not one
// JSON value is read line by line. ok is false when no such number is in it.
func CostFromJSON(out []byte, field string) (cost float64, ok bool) {
	if strings.TrimSpace(field) == "" {
		return 0, false
	}
	walkJSON(out, func(k string, v any) bool {
		if k != field {
			return false
		}
		n, isNum := number(v)
		if isNum && (!ok || n > cost) {
			cost, ok = n, true
		}
		return isNum
	})
	return cost, ok
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return n, err == nil
	}
	return 0, false
}

// CappedBuffer keeps the first Max bytes written to it and drops the rest,
// so a long stdout passes through without being held whole.
type CappedBuffer struct {
	Max int
	buf bytes.Buffer
}

func (b *CappedBuffer) Write(p []byte) (int, error) {
	if room := b.Max - b.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return len(p), nil
}

// Bytes is what was kept.
func (b *CappedBuffer) Bytes() []byte { return b.buf.Bytes() }
