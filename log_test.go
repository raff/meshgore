package meshgore

import (
	"fmt"
	"strings"
	"testing"
)

func TestLogFrame(t *testing.T) {
	var got []string
	old := Logf
	Logf = func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }
	defer func() { Logf = old }()

	logFrame("<-", append([]byte{0x12, 0x01, 0x23}, "KO6IYC-SC: hello world, this is long"...))
	if len(got) != 3 {
		t.Fatalf("got %d lines: %q", len(got), got)
	}
	if !strings.HasPrefix(got[0], "<- 00000000  12 01 23 4b 4f 36 49 59  43 2d 53 43 3a 20 68 65  ") || !strings.HasSuffix(got[0], "|..#KO6IYC-SC: he|") {
		t.Errorf("first line %q", got[0])
	}
	if !strings.HasPrefix(got[1], "   00000010  ") {
		t.Errorf("continuation %q", got[1])
	}
	got = nil
	logFrame("->", []byte{0x0a})
	if len(got) != 1 || !strings.HasPrefix(got[0], "-> 00000000  0a ") || !strings.HasSuffix(got[0], "|.|") {
		t.Errorf("one byte: %q", got)
	}
}
