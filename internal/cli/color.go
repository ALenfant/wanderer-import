package cli

import (
	"fmt"
	"io"
)

const (
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiReset  = "\x1b[0m"
)

func writeWarning(w io.Writer, format string, args ...any) {
	writeColored(w, ansiYellow, format, args...)
}

func writeError(w io.Writer, format string, args ...any) {
	writeColored(w, ansiRed, format, args...)
}

func writeColored(w io.Writer, color, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprint(w, color)
	_, _ = fmt.Fprintf(w, format, args...)
	_, _ = fmt.Fprint(w, ansiReset)
}
