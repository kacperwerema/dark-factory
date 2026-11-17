package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/fseventsd"
)

func stubMachineReading(t *testing.T, r fseventsd.Reading, err error) {
	t.Helper()
	prev := machineReading
	machineReading = func(time.Time) (fseventsd.Reading, error) { return r, err }
	t.Cleanup(func() { machineReading = prev })
}

// The daily snapshot records fseventsd's CPU and physical footprint.
func TestMachineFamilyRecordsFseventsd(t *testing.T) {
	stubMachineReading(t, fseventsd.Reading{PID: 312, CPU: 99.6, FootprintMB: 13516.84}, nil)
	ms, err := machineFamily(context.Background(), Env{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Metric{}
	for _, m := range ms {
		got[m.Name] = m
	}
	if m := got["machine.fseventsd_cpu"]; m.Value != 99.6 || m.Unit != UnitPercent {
		t.Errorf("cpu = %+v", m)
	}
	if m := got["machine.fseventsd_footprint_mb"]; m.Value != 13516.8 || m.Unit != UnitMB {
		t.Errorf("footprint = %+v", m)
	}
	if _, err := machineFamily(context.Background(), Env{Backfill: true}); !errors.Is(err, ErrNoPast) {
		t.Errorf("backfill err = %v", err)
	}
	// No fseventsd (Linux): no metric, no error.
	stubMachineReading(t, fseventsd.Reading{}, fseventsd.ErrNone)
	if ms, err := machineFamily(context.Background(), Env{Now: time.Now()}); err != nil || len(ms) != 0 {
		t.Errorf("no daemon = %+v, %v", ms, err)
	}
}
