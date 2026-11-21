package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
)

// klingBalance makes the stand-in kling's account answer balance (in a
// file under root) and each billing call take charge off it.
func klingBalance(t *testing.T, root, balance, charge string) string {
	t.Helper()
	path := filepath.Join(root, "kling.balance")
	if balance != "" {
		mustWrite(t, path, balance+"\n")
	}
	t.Setenv("KLING_BAL", path)
	t.Setenv("KLING_CHARGE", charge)
	return path
}

// TestKlingCallWithoutACostIsPricedFromTheBalance: a text_to_video call
// that exits 0 with no creditsConsumed, with the balance 300 before and
// 285 after, records 15 from the balance with the call's generationId and
// holds nothing: the next billing call runs.
func TestKlingCallWithoutACostIsPricedFromTheBalance(t *testing.T) {
	root, klingLog, klingOut := meteredFixture(t, "http://127.0.0.1:1")
	bal := klingBalance(t, root, "300", "15")
	answer := `{"generationId":"g-k1c","status":"submitted"}` + "\n"
	mustWrite(t, klingOut, answer)
	out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "--model", "kling-video-v2_6", "a prompt")
	if err != nil {
		t.Fatalf("billing call: %v %s", err, out)
	}
	if !strings.HasPrefix(out, answer) {
		t.Errorf("stdout = %q, want kling's own answer first (the balance reads print nothing)", out)
	}
	recs := spendLines(t, root)
	if len(recs) != 1 || recs[0].Amount != 15 || recs[0].Unknown || recs[0].Source != connectors.SourceBalance || recs[0].Ref != "g-k1c" || recs[0].ID == "" {
		t.Fatalf("spend log = %+v", recs)
	}
	assertContains(t, out, "priced from the balance, 300 → 285 credits: 15 credits (entry "+recs[0].ID+")")
	if b, _ := os.ReadFile(bal); strings.TrimSpace(string(b)) != "285" {
		t.Errorf("balance = %s", b)
	}
	if got := standinLog(klingLog); got != "account\ntext_to_video --model kling-video-v2_6 a prompt\naccount\n" {
		t.Errorf("kling calls = %q", got)
	}
	// Not held: the next one runs, and a cost the output names wins.
	mustWrite(t, klingOut, `{"creditsConsumed":20}`+"\n")
	t.Setenv("KLING_CHARGE", "20")
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); err != nil {
		t.Fatalf("next call: %s", out)
	}
	recs = spendLines(t, root)
	if len(recs) != 2 || recs[1].Amount != 20 || recs[1].Source != "" {
		t.Fatalf("spend log = %+v", recs)
	}
	out, _ = runCLI(t, "connectors", "video")
	assertContains(t, out, "spend 35 / 100 credits this month")
}

// TestKlingCallWithAnUnreadableBalanceHolds: with no balance to read
// (account fails), or one that went up meanwhile, a call with no cost
// stays unknown and holds the next.
func TestKlingCallWithAnUnreadableBalanceHolds(t *testing.T) {
	root, klingLog, klingOut := meteredFixture(t, "http://127.0.0.1:1")
	klingBalance(t, root, "", "15") // no balance file: account exits 3
	mustWrite(t, klingOut, `{"works":[{"url":"u"}]}`+"\n")
	out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x")
	if err != nil {
		t.Fatalf("call: %v %s", err, out)
	}
	assertContains(t, out, "the balance before it could not be read: kling account: exit status 3")
	recs := spendLines(t, root)
	if len(recs) != 1 || !recs[0].Unknown || recs[0].Amount != 0 {
		t.Fatalf("spend log = %+v", recs)
	}
	assertContains(t, out, "--for "+recs[0].ID)
	before := standinLog(klingLog)
	if out, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); err == nil || !strings.Contains(out, "is held: its last billing call's cost is unknown") {
		t.Fatalf("held: %s", out)
	}
	if standinLog(klingLog) != before {
		t.Error("kling ran while held")
	}

	// A balance that goes up (a top-up meanwhile) can't price the call.
	root, _, klingOut = meteredFixture(t, "http://127.0.0.1:1")
	klingBalance(t, root, "300", "-100")
	mustWrite(t, klingOut, `{"works":[{"url":"u"}]}`+"\n")
	out, _ = runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x")
	assertContains(t, out, "the balance went up (300 → 400)")
	if recs := spendLines(t, root); len(recs) != 1 || !recs[0].Unknown {
		t.Fatalf("spend log = %+v", recs)
	}
}

// TestSpendAddRefusesASecondPriceAndRemoveCancels: the owner's spend add
// prices the held call; the lead's for the same call, by default, by
// --for or by --ref, is refused naming the first. remove appends a
// removal that takes the entry off the month's total; connector spend
// lists the ids.
func TestSpendAddRefusesASecondPriceAndRemoveCancels(t *testing.T) {
	root, _, klingOut := meteredFixture(t, "http://127.0.0.1:1")
	mustWrite(t, klingOut, `{"generationId":"g-k3"}`+"\n")
	if _, err := runCLI(t, "connector", "exec", "video/kling", "--", "text_to_video", "x"); err != nil {
		t.Fatal(err)
	}
	call := spendLines(t, root)[0]
	if !call.Unknown || call.Ref != "g-k3" {
		t.Fatalf("call = %+v", call)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	out, err := runCLI(t, "connector", "spend", "add", "video/kling", "15", "--note", "balance 300 to 285")
	if err != nil {
		t.Fatalf("owner: %s", out)
	}
	first := spendLines(t, root)[1]
	if first.For != call.ID || first.Ref != "g-k3" || !first.Manual {
		t.Fatalf("first = %+v", first)
	}
	assertContains(t, out, "✓ recorded 15 credits on video/kling for "+call.ID+" (entry "+first.ID+"): 15 / 100 credits this month")

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:video")
	for _, args := range [][]string{nil, {"--for", call.ID}, {"--ref", "g-k3"}} {
		out, err := runCLI(t, append([]string{"connector", "spend", "add", "video/kling", "15"}, args...)...)
		if err == nil || !strings.Contains(out, "already priced") || !strings.Contains(out, first.ID) {
			t.Errorf("lead %v: %v %s", args, err, out)
		}
	}
	if n := len(spendLines(t, root)); n != 2 {
		t.Fatalf("%d lines after the refused adds", n)
	}
	// Spend outside the engine is still recorded, said so.
	if out, err := runCLI(t, "connector", "spend", "add", "video/kling", "30", "--outside", "--note", "used in the app"); err != nil {
		t.Fatalf("outside: %s", out)
	}
	out, _ = runCLI(t, "connectors", "video")
	assertContains(t, out, "spend 45 / 100 credits this month")

	// A worker may not remove; the lead removes the owner's entry.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "reels/clip")
	if out, err := runCLI(t, "connector", "spend", "remove", first.ID, "--reason", "x"); err == nil || !strings.Contains(out, "for the owner or Video's lead") {
		t.Errorf("worker remove: %s", out)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:video")
	if out, err := runCLI(t, "connector", "spend", "remove", first.ID); err == nil {
		t.Errorf("remove without a reason: %s", out)
	}
	ledger := filepath.Join(root, "issues", ".connectors", "spend.jsonl")
	was, _ := os.ReadFile(ledger)
	out, err = runCLI(t, "connector", "spend", "remove", first.ID, "--reason", "counted in the app record")
	if err != nil {
		t.Fatalf("remove: %s", out)
	}
	assertContains(t, out, ": 30 / 100 credits this month ("+connectors.Month(time.Now())+"), last call's cost unknown")
	now, _ := os.ReadFile(ledger)
	if !strings.HasPrefix(string(now), string(was)) {
		t.Error("remove edited the ledger in place")
	}
	recs := spendLines(t, root)
	rm := recs[len(recs)-1]
	if rm.Removes != first.ID || rm.Note != "counted in the app record" || rm.By != "lead:video" {
		t.Fatalf("removal = %+v", rm)
	}
	if out, err := runCLI(t, "connector", "spend", "remove", first.ID, "--reason", "again"); err == nil || !strings.Contains(out, "already removed by "+rm.ID) {
		t.Errorf("second remove: %s", out)
	}
	// The call is unpriced again: the lead may now price it.
	if out, err := runCLI(t, "connector", "spend", "add", "video/kling", "15", "--ref", "g-k3"); err != nil {
		t.Fatalf("price again: %s", out)
	}

	out, err = runCLI(t, "connector", "spend", "video")
	if err != nil {
		t.Fatal(out)
	}
	for _, want := range []string{call.ID + "  ", "text_to_video  cost unknown  by reels/clip (ref g-k3)", first.ID + "  ", "removed (" + rm.ID + ": counted in the app record)", "by hand  30 credits  by lead:video (outside the engine; used in the app)"} {
		assertContains(t, out, want)
	}
	if strings.Contains(out, rm.ID+"  ") {
		t.Error("the removal line is listed as an entry")
	}
	var list []map[string]any
	js, _ := runCLI(t, "connector", "spend", "video/kling", "--json")
	if err := json.NewDecoder(strings.NewReader(js)).Decode(&list); err != nil || len(list) != 4 || list[1]["removed_by"] != rm.ID {
		t.Fatalf("json: %v %s", err, js)
	}
}
