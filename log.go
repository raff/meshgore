package meshgore

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
