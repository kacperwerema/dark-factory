package viewer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry/share"
)

// sandboxBatches stores batches from three made-up installs into a local
// collector store, as the collector would.
func sandboxBatches(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st := share.Store{Dir: dir}
	today := time.Now().UTC()
	vers := []string{"v0.55.0", "v0.55.0", "v0.54.2"}
	for i, ver := range vers {
		for d := 1; d <= 3; d++ {
			b := share.Batch{V: 1, Install: strings.Repeat(fmt.Sprintf("%02x", i+1), 16), Day: today.AddDate(0, 0, -d).Format("2006-01-02"),
				Ver: ver, OS: "linux", Arch: "amd64",
				Commands: []share.Command{
					{Cmd: "list", Calls: 20, P50MS: 3, Flags: map[string]int{"status": 4}},
					{Cmd: "transition", Calls: 5, Fails: 2, P50MS: 40, Errors: map[string]int{"validation": 2}},
				},
				Templates: []string{"development"}, Features: []string{"local-ci"}, Counts: share.Counts{Projects: 1, Issues: 30}}
			if i == 2 {
				b.Features = append(b.Features, "yolo")
				b.Commands = append(b.Commands, share.Command{Cmd: "workflow", Sub: "upgrade", Calls: 1})
			}
			if err := st.Append(b); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func getTelemetry(t *testing.T, data, query string) string {
	t.Helper()
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.telemetryData = data
	req := httptest.NewRequest(http.MethodGet, "/telemetry"+query, nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /telemetry = %d\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestTelemetryPageShowsCollectorData(t *testing.T) {
	body := getTelemetry(t, sandboxBatches(t), "")
	for _, want := range []string{
		`data-testid="telemetry.page"`,
		"3 installs · 9 daily batches",
		`data-testid="stats.chart.installs"`,
		`data-testid="stats.chart.versions"`, "v0.55.0", "v0.54.2",
		`data-testid="stats.chart.commands"`, "transition",
		`data-testid="stats.chart.features"`, "local-ci", "yolo",
		`data-testid="stats.chart.templates"`, "development",
		`data-testid="stats.table_card.telemetry-errors"`, "40.0%", "validation",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `data-testid="telemetry.empty"`) {
		t.Error("empty state shown with data")
	}
}

func TestTelemetryPageEmptyStates(t *testing.T) {
	body := getTelemetry(t, "", "")
	if !strings.Contains(body, `data-testid="telemetry.empty"`) || !strings.Contains(body, "go run ./cmd/telemetry-collector -data") || !strings.Contains(body, "telemetry_data:") {
		t.Errorf("unconfigured page doesn't say how to fill it:\n%s", body)
	}
	body = getTelemetry(t, t.TempDir(), "")
	if !strings.Contains(body, "No batches in") || !strings.Contains(body, "ISSUE_CLI_TELEMETRY_URL=") {
		t.Errorf("empty store page:\n%s", body)
	}
}
