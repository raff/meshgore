package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/raff/meshgore"
)

func chanMsg(idx int, text string) *meshgore.Message {
	return &meshgore.Message{Channel: true, ChannelIdx: idx, Text: text}
}

// batches returns a sync function that gives one batch per call, then nothing.
func batches(b ...[]*meshgore.Message) func() ([]*meshgore.Message, error) {
	return func() ([]*meshgore.Message, error) {
		if len(b) == 0 {
			return nil, nil
		}
		out := b[0]
		b = b[1:]
		return out, nil
	}
}

func texts(ms []*meshgore.Message) (out []string) {
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return
}

func TestCollectChannel(t *testing.T) {
	dm := &meshgore.Message{Text: "hello"}
	run := func(w channelWait, sync func() ([]*meshgore.Message, error)) (matched, other []string) {
		w.wait = 60 * time.Millisecond
		var o []*meshgore.Message
		m := collectChannel(context.Background(), nil, 10*time.Millisecond, sync, w,
			func(*meshgore.Message) {}, func(m *meshgore.Message) { o = append(o, m) })
		return texts(m), texts(o)
	}

	t.Run("only the channel and the match count", func(t *testing.T) {
		m, o := run(channelWait{idx: 2, match: "TEST"}, batches(
			[]*meshgore.Message{chanMsg(2, "A: test ok"), chanMsg(3, "B: test"), dm},
			[]*meshgore.Message{chanMsg(2, "C: chatter"), chanMsg(2, "D: Test 2")}))
		if want := []string{"A: test ok", "D: Test 2"}; !reflect.DeepEqual(m, want) {
			t.Errorf("matched %q, want %q", m, want)
		}
		if want := []string{"B: test", "hello", "C: chatter"}; !reflect.DeepEqual(o, want) {
			t.Errorf("other %q, want %q", o, want)
		}
	})
	t.Run("no match text counts every message on the channel", func(t *testing.T) {
		m, _ := run(channelWait{idx: 2}, batches([]*meshgore.Message{chanMsg(2, "x"), chanMsg(2, "y")}))
		if len(m) != 2 {
			t.Errorf("matched %q", m)
		}
	})
	t.Run("first stops early and passes the rest on", func(t *testing.T) {
		start := time.Now()
		w := channelWait{idx: 2, match: "ok", first: true, wait: time.Minute}
		var o []*meshgore.Message
		m := collectChannel(context.Background(), nil, 5*time.Millisecond,
			batches([]*meshgore.Message{chanMsg(2, "A: ok"), chanMsg(2, "B: ok"), dm}), w,
			func(*meshgore.Message) {}, func(m *meshgore.Message) { o = append(o, m) })
		if len(m) != 1 || time.Since(start) > 5*time.Second {
			t.Errorf("matched %q after %s", texts(m), time.Since(start))
		}
		if want := []string{"B: ok", "hello"}; !reflect.DeepEqual(texts(o), want) {
			t.Errorf("rest %q, want %q", texts(o), want)
		}
	})
	t.Run("nothing arrives", func(t *testing.T) {
		if m, _ := run(channelWait{idx: 2, match: "x"}, batches()); len(m) != 0 {
			t.Errorf("matched %q", m)
		}
	})
	t.Run("a message waiting at the deadline is still read", func(t *testing.T) {
		// the ticker is far slower than the wait: only the final sync can see it
		w := channelWait{idx: 2, wait: 30 * time.Millisecond}
		m := collectChannel(context.Background(), nil, time.Hour, batches([]*meshgore.Message{chanMsg(2, "late")}), w,
			func(*meshgore.Message) {}, func(*meshgore.Message) {})
		if len(m) != 1 {
			t.Errorf("matched %q", texts(m))
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := collectChannel(ctx, nil, time.Hour, batches(), channelWait{idx: 2, wait: time.Minute},
			func(*meshgore.Message) {}, func(*meshgore.Message) {})
		if len(m) != 0 {
			t.Errorf("matched %q", texts(m))
		}
	})
}

func TestSummarizeReplies(t *testing.T) {
	got := summarizeReplies([]*meshgore.Message{chanMsg(1, "Ann: test"), chanMsg(1, "Bob: ok"), chanMsg(1, "Ann: again"), chanMsg(1, "no sender")})
	if want := "4 matching message(s) from Ann, Bob"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := summarizeReplies([]*meshgore.Message{chanMsg(1, "plain")}); got != "1 matching message(s)" {
		t.Errorf("got %q", got)
	}
}
