package metrics

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSnapshotCounters(t *testing.T) {
	start := time.Unix(1000, 0)
	r := New(start)
	r.C.SessionsCreated.Add(3)
	r.C.SessionsActive.Store(2)
	r.C.AuthFailures.Add(1)
	r.C.DNSQueries.Add(10)
	r.SetSlaveStatFunc(func() []SlaveStat {
		return []SlaveStat{{SlaveID: "slave01", Connected: true, Sessions: 2, DataPlaneUp: true}}
	})

	snap := r.Snapshot(start.Add(30 * time.Second))
	if snap.UptimeSeconds != 30 {
		t.Errorf("uptime = %d, want 30", snap.UptimeSeconds)
	}
	if snap.SessionsCreated != 3 || snap.SessionsActive != 2 {
		t.Errorf("session counts wrong: %+v", snap)
	}
	if snap.AuthFailures != 1 || snap.DNSQueries != 10 {
		t.Errorf("counter values wrong: %+v", snap)
	}
	if len(snap.Slaves) != 1 || snap.Slaves[0].SlaveID != "slave01" {
		t.Errorf("slave stats wrong: %+v", snap.Slaves)
	}
}

func TestHandlerJSON(t *testing.T) {
	r := New(time.Unix(0, 0))
	r.C.SessionsCreated.Add(5)
	h := r.Handler(func() time.Time { return time.Unix(60, 0) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if snap.SessionsCreated != 5 || snap.UptimeSeconds != 60 {
		t.Errorf("decoded snapshot wrong: %+v", snap)
	}
}
