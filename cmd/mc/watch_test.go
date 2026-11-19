package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/raff/meshgore"
)

func TestWatchCSV(t *testing.T) {
	path := t.TempDir() + "/sub/readings.csv"
	s := &meshgore.RepeaterStatus{
		BatteryMV: 4130, NoiseFloor: -112, LastRSSI: -33, LastSNR: 12.25,
		Received: 652, Sent: 643, Uptime: 5959 * time.Second,
		RecvErrors: 37, HasRecvErrs: true,
	}
	rec := watchRecord{OK: true, Status: s, Alerts: []string{"a", "b"}}
	for i := 0; i < 2; i++ { // reopening must not repeat the header
		w, f, err := openCSV(path, csvHeader)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(csvRow(rec))
		w.Flush()
		f.Close()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "time,battery_v") {
		t.Fatalf("want header + 2 rows, got:\n%s", data)
	}
	if len(csvRow(rec)) != len(csvHeader) {
		t.Fatalf("row has %d columns, header %d", len(csvRow(rec)), len(csvHeader))
	}
	if !strings.Contains(lines[1], ",4.130,5959,-112,-33,12.25,") || !strings.HasSuffix(lines[1], ",a; b") {
		t.Errorf("unexpected row: %s", lines[1])
	}
}
