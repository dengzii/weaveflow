//go:build !cgo

package memory

import "unicode"

func segmentText(text string) []string {
	segments := make([]string, 0, len(text)/2)
	buffer := make([]rune, 0, len(text))

	flushBuffer := func() {
		if len(buffer) == 0 {
			return
		}
		segments = append(segments, string(buffer))
		buffer = buffer[:0]
	}

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushBuffer()
			segments = append(segments, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			buffer = append(buffer, r)
		default:
			flushBuffer()
		}
	}
	flushBuffer()

	return segments
}
