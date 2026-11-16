package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/raff/meshgore"
)

// watchRecord is one reading in -watch mode; with -json each is one line.
type watchRecord struct {
	Time   time.Time                `json:"time"`
	OK     bool                     `json:"ok"`
	Error  string                   `json:"error,omitempty"`
	Status *meshgore.RepeaterStatus `json:"status,omitempty"`
	Alerts []string                 `json:"alerts,omitempty"`
}

var csvHeader = []string{"time", "battery_v", "uptime_s", "noise_floor_dbm", "last_rssi_dbm", "last_snr_db",
	"tx_queue_len", "packets_received", "packets_sent", "receive_errors", "tx_airtime_s", "rx_airtime_s", "alerts"}

func csvRow(r watchRecord) []string {
	s := r.Status
	recvErrs := ""
	if s.HasRecvErrs {
		recvErrs = strconv.FormatUint(uint64(s.RecvErrors), 10)
	}
	return []string{
		r.Time.Format(time.RFC3339),
		strconv.FormatFloat(float64(s.BatteryMV)/1000, 'f', 3, 64),
		strconv.FormatInt(int64(s.Uptime/time.Second), 10),
		strconv.Itoa(int(s.NoiseFloor)),
		strconv.Itoa(int(s.LastRSSI)),
		strconv.FormatFloat(s.LastSNR, 'f', 2, 64),
		strconv.Itoa(int(s.TxQueueLen)),
		strconv.FormatUint(uint64(s.Received), 10),
		strconv.FormatUint(uint64(s.Sent), 10),
		recvErrs,
		strconv.FormatInt(int64(s.TxAirtime/time.Second), 10),
		strconv.FormatInt(int64(s.RxAirtime/time.Second), 10),
		strings.Join(r.Alerts, "; "),
	}
}

// openCSV opens path for appending and writes the header if the file is new.
func openCSV(path string) (*csv.Writer, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	w := csv.NewWriter(f)
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		w.Write(csvHeader)
		w.Flush()
	}
	return w, f, nil
}

// watchRepeater polls the repeater's status every interval until ctx is
// cancelled, surviving dropped Bluetooth connections and a repeater that
// forgets the login. It returns the process exit code.
func watchRepeater(ctx context.Context, l *meshgore.Link, ct *meshgore.Contact, o repeaterOpts) int {
	var csvw *csv.Writer
	if o.csvPath != "" {
		w, f, err := openCSV(o.csvPath)
		if err != nil {
			fmt.Println("error:", err)
			return exitFail
		}
		defer f.Close()
		csvw = w
		fmt.Println("Logging readings to", o.csvPath)
	}
	enc := json.NewEncoder(o.out) // compact: one JSON object per line

	fmt.Printf("Watching %s every %s (Ctrl-C to stop)\n", ct.Name, o.watch)
	defer func() {
		if l.Alive() {
			l.Client.Logout(ct.PublicKey)
		}
	}()

	loggedIn := true // runRepeater logged in before calling us
	for first := true; ; first = false {
		if !first {
			select {
			case <-ctx.Done():
				return exitOK
			case <-time.After(o.watch):
			}
		}

		rec := watchRecord{Time: time.Now()}
		if !l.Alive() {
			fmt.Println("connection to the companion lost; reconnecting...")
			if err := l.Reconnect(ctx); err != nil {
				return exitOK // cancelled while reconnecting
			}
			loggedIn = false
		}
		if !loggedIn {
			if err := l.Client.Login(ct.PublicKey, o.password); err != nil {
				rec.Error = err.Error()
			} else {
				loggedIn = true
			}
		}
		if loggedIn {
			st, err := l.Client.RequestStatus(ct.PublicKey)
			if err != nil {
				rec.Error = err.Error()
				loggedIn = false // the repeater may have rebooted; log in again next time
			} else {
				rec.OK, rec.Status = true, st
				rec.Alerts = st.Check(o.limits)
			}
		}

		if o.json {
			enc.Encode(rec)
		} else if rec.OK {
			s := rec.Status
			fmt.Printf("%s  %.2f V  noise %d dBm  rssi %d dBm  snr %.2f dB  rx %d  tx %d  up %s\n",
				rec.Time.Format(time.DateTime), float64(s.BatteryMV)/1000, s.NoiseFloor, s.LastRSSI,
				s.LastSNR, s.Received, s.Sent, s.Uptime.Round(time.Second))
		} else {
			fmt.Printf("%s  error: %s\n", rec.Time.Format(time.DateTime), rec.Error)
		}
		for _, a := range rec.Alerts {
			fmt.Println("ALERT:", a)
		}
		if csvw != nil && rec.OK {
			csvw.Write(csvRow(rec))
			csvw.Flush()
		}
	}
}

// companionRecord is one reading in -watch mode for the companion itself.
type companionRecord struct {
	Time   time.Time                `json:"time"`
	OK     bool                     `json:"ok"`
	Error  string                   `json:"error,omitempty"`
	Status *meshgore.CompanionStats `json:"status,omitempty"`
	Alerts []string                 `json:"alerts,omitempty"`
}

// companionCSVRow uses the same columns as the repeater's (see csvHeader); a
// value the companion's stats lack is left empty.
func companionCSVRow(r companionRecord) []string {
	s := r.Status
	n := func(ok bool, v string) string {
		if ok {
			return v
		}
		return ""
	}
	i := func(v int64) string { return strconv.FormatInt(v, 10) }
	return []string{
		r.Time.Format(time.RFC3339),
		n(s.HasCore || s.HasStorage, strconv.FormatFloat(float64(s.BatteryMV)/1000, 'f', 3, 64)),
		n(s.HasCore, i(int64(s.Uptime/time.Second))),
		n(s.HasRadio, i(int64(s.NoiseFloor))),
		n(s.HasRadio, i(int64(s.LastRSSI))),
		n(s.HasRadio, strconv.FormatFloat(s.LastSNR, 'f', 2, 64)),
		n(s.HasCore, i(int64(s.QueueLen))),
		n(s.HasPackets, i(int64(s.Received))),
		n(s.HasPackets, i(int64(s.Sent))),
		n(s.HasErrors, i(int64(s.RecvErrors))),
		n(s.HasRadio, i(int64(s.TxAirtime/time.Second))),
		n(s.HasRadio, i(int64(s.RxAirtime/time.Second))),
		strings.Join(r.Alerts, "; "),
	}
}

// statsSource is a node whose own statistics can be read: a companion, or a
// repeater's serial console.
type statsSource interface {
	Stats() (*meshgore.CompanionStats, error)
	Alive() bool
	Reconnect(ctx context.Context) error
	Close()
	Name() string
}

// linkSource reads a companion's statistics over its link.
type linkSource struct{ l *meshgore.Link }

func (s linkSource) Stats() (*meshgore.CompanionStats, error) { return s.l.Client.Stats() }
func (s linkSource) Alive() bool                              { return s.l.Alive() }
func (s linkSource) Reconnect(ctx context.Context) error      { return s.l.Reconnect(ctx) }
func (s linkSource) Close()                                   { s.l.Close() }
func (s linkSource) Name() string                             { return "the companion" }

// consoleSource reads a repeater's statistics over its serial console.
type consoleSource struct {
	c       *meshgore.Console
	timeout time.Duration
}

func (s *consoleSource) Stats() (*meshgore.CompanionStats, error) { return s.c.Stats(s.timeout) }
func (s *consoleSource) Alive() bool                              { return s.c.Alive() }
func (s *consoleSource) Close()                                   { s.c.Close() }
func (s *consoleSource) Name() string                             { return "the node" }
func (s *consoleSource) Reconnect(ctx context.Context) error {
	n, err := s.c.Reopen(ctx)
	if err == nil {
		s.c = n
	}
	return err
}

// watchCompanion reads a node's own statistics every interval until ctx is
// cancelled, surviving dropped connections.
func watchCompanion(ctx context.Context, src statsSource, interval time.Duration, csvPath string, limits meshgore.Thresholds, asJSON bool, out *os.File) int {
	defer src.Close()
	var csvw *csv.Writer
	if csvPath != "" {
		w, f, err := openCSV(csvPath)
		if err != nil {
			fmt.Println("error:", err)
			return exitFail
		}
		defer f.Close()
		csvw = w
		fmt.Println("Logging readings to", csvPath)
	}
	enc := json.NewEncoder(out)
	fmt.Printf("Watching %s every %s (Ctrl-C to stop)\n", src.Name(), interval)

	for first := true; ; first = false {
		if !first {
			select {
			case <-ctx.Done():
				return exitOK
			case <-time.After(interval):
			}
		}
		rec := companionRecord{Time: time.Now()}
		if !src.Alive() {
			fmt.Printf("connection to %s lost; reconnecting...\n", src.Name())
			if err := src.Reconnect(ctx); err != nil {
				return exitOK // cancelled while reconnecting
			}
		}
		if st, err := src.Stats(); err != nil {
			rec.Error = err.Error()
		} else {
			rec.OK, rec.Status = true, st
			rec.Alerts = st.Check(limits)
		}

		switch {
		case asJSON:
			enc.Encode(rec)
		case rec.OK:
			s := rec.Status
			fmt.Printf("%s  %.2f V  noise %d dBm  rssi %d dBm  snr %.2f dB  rx %d  tx %d  up %s\n",
				rec.Time.Format(time.DateTime), float64(s.BatteryMV)/1000, s.NoiseFloor, s.LastRSSI,
				s.LastSNR, s.Received, s.Sent, s.Uptime.Round(time.Second))
		default:
			fmt.Printf("%s  error: %s\n", rec.Time.Format(time.DateTime), rec.Error)
		}
		for _, a := range rec.Alerts {
			fmt.Println("ALERT:", a)
		}
		if csvw != nil && rec.OK {
			csvw.Write(companionCSVRow(rec))
			csvw.Flush()
		}
	}
}
