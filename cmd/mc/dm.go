package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/raff/meshgore"
)

type dmResult struct {
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	To         string  `json:"to,omitempty"`
	Key        string  `json:"key,omitempty"`
	Acked      bool    `json:"acked"`
	Attempts   int     `json:"attempts"`
	Flood      bool    `json:"flood"`
	RoundTripS float64 `json:"round_trip_s,omitempty"`
	NodeTimed  bool    `json:"round_trip_from_node,omitempty"`
}

// runDM sends text to a contact and waits for its acknowledgement. It returns
// the process exit code: 0 if the recipient acknowledged, 1 otherwise. A
// message that was sent but not acknowledged may still have arrived; the radio
// gives no certainty either way.
func runDM(ctx context.Context, l *meshgore.Link, node, target, text string, jsonOut bool, out io.Writer) int {
	var res dmResult
	defer func() {
		if jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			enc.Encode(res)
		}
	}()
	fail := func(err error) int {
		fmt.Println("error:", err)
		res.Error = err.Error()
		return exitFail
	}

	ct, err := resolveContact(l.Client, node, target)
	if err != nil {
		return fail(err)
	}
	res.To, res.Key = ct.Name, hex.EncodeToString(ct.PublicKey[:])
	fmt.Printf("To: %s (%s, key %x)\n", ct.Name, ct.TypeName(), ct.PublicKey[:6])

	d, err := l.Client.SendMessageWithRetry(ctx, ct.PublicKey, text)
	if d != nil {
		res.Acked, res.Attempts, res.Flood = d.Acked, d.Attempts, d.Flood
		res.RoundTripS, res.NodeTimed = d.RoundTrip.Seconds(), d.NodeReported
	}
	if err != nil {
		return fail(err)
	}
	if !d.Acked {
		return fail(fmt.Errorf("sent %d time(s) but no acknowledgement arrived; the message may still have been delivered", d.Attempts))
	}
	res.OK = true
	source := "measured here"
	if d.NodeReported {
		source = "timed by the node"
	}
	fmt.Printf("Delivered: acknowledged after %s (%s; %d attempt(s), %s)\n",
		d.RoundTrip.Round(time.Millisecond), source, d.Attempts, map[bool]string{true: "flood", false: "direct"}[d.Flood])
	return exitOK
}
