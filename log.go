package meshgore

import (
	"encoding/hex"
	"strings"
)

// Debug makes the library log every raw protocol frame ("->" sent, "<-"
// received) through Logf.
var Debug bool

// Logf receives the library's progress and diagnostic messages (scan results,
// pairing prompts, retries, raw frames when Debug is set). Messages have no
// trailing newline. It discards everything by default; set it to see them:
//
//	meshgore.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
var Logf = func(format string, args ...any) {}

// logf is Logf with a guard against a nil function.
func logf(format string, args ...any) {
	if Logf != nil {
		Logf(format, args...)
	}
}

// logFrame logs a raw frame the way "hexdump -C" shows it: the offset, 16 bytes in hex
// and the same bytes as ASCII ('.' for what is not printable), so a name inside a
// frame can be read. dir is "->" or "<-"; later lines are indented under the first.
func logFrame(dir string, frame []byte) {
	lines := strings.Split(strings.TrimRight(hex.Dump(frame), "\n"), "\n")
	for i, line := range lines {
		if i == 0 {
			logf("%s %s", dir, line)
		} else {
			logf("%s %s", strings.Repeat(" ", len(dir)), line)
		}
	}
}
