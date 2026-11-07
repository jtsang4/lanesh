package runner

import (
	"strings"
	"unicode/utf8"
)

// toText converts process output to valid UTF-8. On Windows, output that is not
// UTF-8 is first decoded with the system ANSI code page.
func toText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	if s, ok := decodeANSI(b); ok {
		return s
	}
	return strings.ToValidUTF8(string(b), "�")
}

// cutRuneBoundary trims a trailing partial UTF-8 sequence.
func cutRuneBoundary(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}
