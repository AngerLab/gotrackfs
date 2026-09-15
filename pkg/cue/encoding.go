package cue

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// DecodeBytes detects the character encoding of the given byte slice
// and transcodes it into a valid UTF-8 string without Byte Order Marks (BOM).
func DecodeBytes(b []byte) (string, error) {
	// Strip UTF-8 BOM prefix if present
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))

	// 1. Fast path: already valid UTF-8
	if utf8.Valid(b) {
		return cleanBOM(string(b)), nil
	}

	// 2. High-precision heuristic for Cyrillic (Windows-1251):
	// In CUE sheets, English keywords (TRACK, FILE, INDEX) dominate the byte count,
	// confusing generic n-gram detectors into guessing ISO-8859-1 (French/German)
	// with ~25% confidence.
	// In Windows-1251, Russian letters reside strictly in 0xC0..0xFF, 0xA8, 0xB8.
	// If the overwhelming majority of non-ASCII bytes are in this range, it's Windows-1251.
	if isLikelyWindows1251(b) {
		decoded, _, err := transform.String(charmap.Windows1251.NewDecoder(), string(b))
		if err == nil && utf8.ValidString(decoded) {
			return cleanBOM(decoded), nil
		}
	}

	// 3. Statistical charset detection (Shift-JIS, EUC-JP, GB-18030, Big5, ISO-8859-*, etc.)
	detector := chardet.NewTextDetector()
	result, err := detector.DetectBest(b)
	if err == nil && result.Charset != "" {
		reader, err := charset.NewReaderLabel(result.Charset, bytes.NewReader(b))
		if err == nil {
			decoded, err := io.ReadAll(reader)
			if err == nil {
				return cleanBOM(string(decoded)), nil
			}
		}
	}

	// 4. Fallback to raw string
	return cleanBOM(string(b)), nil
}

// cleanBOM strips leading zero-width BOM rune if present in decoded string.
func cleanBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}

// isLikelyWindows1251 checks if the non-ASCII bytes strongly correlate with CP1251 Cyrillic.
func isLikelyWindows1251(b []byte) bool {
	nonASCII := 0
	c1251Letters := 0

	for _, char := range b {
		if char >= 128 {
			nonASCII++
			if (char >= 0xC0 && char <= 0xFF) || char == 0xA8 || char == 0xB8 {
				c1251Letters++
			}
		}
	}

	if nonASCII == 0 {
		return false
	}

	// If at least 80% of non-ASCII bytes are Cyrillic letters in CP1251:
	return float64(c1251Letters)/float64(nonASCII) >= 0.80
}

// DecodeReader reads all bytes from r and converts them into UTF-8 without BOM.
func DecodeReader(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return DecodeBytes(b)
}
