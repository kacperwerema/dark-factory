package connectors

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestBalanceFromJSON(t *testing.T) {
	for _, c := range []struct {
		out  string
		want float64
		ok   bool
	}{
		{`{"data":{"availableRemainCredits":300}}`, 300, true},
		{`{"availableRemainCredits":"285","data":{"availableRemainCredits":285}}`, 285, true},
		{`{"packs":[{"availableRemainCredits":100},{"availableRemainCredits":200}]}`, 0, false},
		{`{"membership":"pro"}`, 0, false},
		{`not json`, 0, false},
	} {
		got, err := BalanceFromJSON([]byte(c.out), "availableRemainCredits")
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("%s: %v %v, want %v ok=%v", c.out, got, err, c.want, c.ok)
		}
	}
	if got := RefFromJSON([]byte(`{"body":{"submission":{"generationId":"g1"}},"taskId":""}`)); got != "g1" {
		t.Errorf("ref = %q", got)
	}
}

func TestBalanceReadDefaultsForKling(t *testing.T) {
	json := &tracker.ConnectorSpend{Meter: tracker.MeterJSON, Field: "creditsConsumed"}
	for _, c := range []struct {
		conn       tracker.Connector
		sub, field string
	}{
		{tracker.Connector{Command: "kling", Spend: json}, "account", "availableRemainCredits"},
		{tracker.Connector{Command: "/opt/bin/kling", Spend: &tracker.ConnectorSpend{Meter: tracker.MeterJSON, Balance: "balance"}}, "balance", "availableRemainCredits"},
		{tracker.Connector{Command: "other", Spend: json}, "", ""},
		{tracker.Connector{Command: "other", Spend: &tracker.ConnectorSpend{Meter: tracker.MeterJSON, Balance: "credits", BalanceField: "left"}}, "credits", "left"},
		{tracker.Connector{Command: "kling", Spend: &tracker.ConnectorSpend{Meter: tracker.MeterElevenLabs}}, "", ""},
	} {
		if sub, field := BalanceRead(&c.conn); sub != c.sub || field != c.field {
			t.Errorf("%s %+v: %q %q, want %q %q", c.conn.Command, *c.conn.Spend, sub, field, c.sub, c.field)
		}
	}
}

// TestSpendEntriesHaveIDsAndRemovalsCancel: a line written before ids gets
// the same id on every read; a removal takes its entry off the total and
// its hold.
func TestSpendEntriesHaveIDsAndRemovalsCancel(t *testing.T) {
	p := &tracker.Project{IssueDir: t.TempDir()}
	now := time.Now()
	os.MkdirAll(filepath.Join(p.IssueDir, ".connectors"), 0755)
	old := `{"connector":"video/kling","month":"` + Month(now) + `","unit":"credits","amount":5,"exit":0}` + "\n"
	os.WriteFile(SpendLogPath(p), []byte(old), 0644)
	a, b := LoadSpend(p), LoadSpend(p)
	if a[0].ID == "" || a[0].ID != b[0].ID {
		t.Fatalf("ids %q %q", a[0].ID, b[0].ID)
	}
	id, _ := AppendSpendID(p, SpendRecord{Connector: "video/kling", Month: Month(now), Amount: 0, Unknown: true})
	c := &tracker.Connector{Spend: &tracker.ConnectorSpend{Meter: tracker.MeterJSON, Cap: 100}}
	if st := SpendFor(LoadSpend(p), "video/kling", c, now); st.Spent != 5 || !st.Unknown {
		t.Fatalf("before: %+v", st)
	}
	AppendSpend(p, SpendRecord{Connector: "video/kling", Month: Month(now), Manual: true, Outside: true, Amount: 1})
	if st := SpendFor(LoadSpend(p), "video/kling", c, now); !st.Unknown {
		t.Error("spend outside the engine lifted the hold")
	}
	AppendSpend(p, SpendRecord{Connector: "video/kling", Month: Month(now), Manual: true, Removes: id, Note: "never billed"})
	AppendSpend(p, SpendRecord{Connector: "video/kling", Month: Month(now), Manual: true, Removes: a[0].ID, Note: "wrong"})
	if st := SpendFor(LoadSpend(p), "video/kling", c, now); st.Spent != 1 || st.Unknown || st.Calls != 0 {
		t.Fatalf("after: %+v", st)
	}
}

// TestLockBillingWaits: a second billing call waits for the first.
func TestLockBillingWaits(t *testing.T) {
	p := &tracker.Project{IssueDir: t.TempDir()}
	unlock, err := LockBilling(p, "video/kling", func() { t.Error("the first call waited") })
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{}, 1)
	got := make(chan struct{})
	go func() {
		u, err := LockBilling(p, "video/kling", func() { waited <- struct{}{} })
		if err == nil {
			u()
		}
		close(got)
	}()
	<-waited
	select {
	case <-got:
		t.Fatal("the second call ran while the first held the lock")
	default:
	}
	unlock()
	<-got
	// Another connector has its own lock.
	u, err := LockBilling(p, "video/other", func() { t.Error("waited on another connector's lock") })
	if err != nil {
		t.Fatal(err)
	}
	u()
}
