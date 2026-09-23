package cue

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	textunicode "golang.org/x/text/encoding/unicode"
)

// SniffAndPreprocess detects UTF-16 BOMs and converts them to UTF-8 bytes using
// standard x/text/encoding/unicode, preserving surrogate pairs and stripping BOMs.
// Also strips UTF-8 BOM if present.
func SniffAndPreprocess(b []byte) ([]byte, error) {
	// UTF-8 BOM: EF BB BF
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), nil
	}

	// UTF-16LE BOM: FF FE
	if bytes.HasPrefix(b, []byte("\xff\xfe")) {
		dec := textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM).NewDecoder()
		out, err := dec.Bytes(b)
		if err != nil {
			return nil, fmt.Errorf("decode utf-16le: %w", err)
		}
		return out, nil
	}

	// UTF-16BE BOM: FE FF
	if bytes.HasPrefix(b, []byte("\xfe\xff")) {
		dec := textunicode.UTF16(textunicode.BigEndian, textunicode.UseBOM).NewDecoder()
		out, err := dec.Bytes(b)
		if err != nil {
			return nil, fmt.Errorf("decode utf-16be: %w", err)
		}
		return out, nil
	}

	return b, nil
}

// DetectEncoding analyzes a concentrated sample of value bytes and returns
// the best matching charset label.
//
// Single engine philosophy: chardet (Mozilla ICU) covers all remaining legacy
// charsets; only two compact hand-rolled rules preempt it, because chardet
// misidentifies short samples of two common CUE cases:
//   - Cyrillic: CP1251/KOI8-R are reported as ISO-8859-1/8/9 (see detectCyrillic)
//   - CJK: EUC-KR/GB18030 samples are too short for reliable ICU statistics
//
// Both rules were calibrated against the fixtures in testdata and the short
// edge-case samples in parser_test.go.
func DetectEncoding(sample []byte) string {
	// Fast path: ASCII or valid UTF-8
	if len(sample) == 0 || utf8.Valid(sample) {
		return "utf-8"
	}

	// CJK first: Cyrillic bytes never decode cleanly through CJK tables, but
	// CJK bytes can fake Cyrillic through KOI8-R, so the more specific rule wins.
	if name, ok := detectCJK(sample); ok {
		return name
	}

	// Cyrillic disambiguation: Windows-1251 vs KOI8-R.
	if name, ok := detectCyrillic(sample); ok {
		return name
	}

	// Western European: Windows-1252 with a modest share of accented letters.
	if name, ok := detectLatin(sample); ok {
		return name
	}

	// Everything else: single statistical engine.
	detector := chardet.NewTextDetector()
	result, err := detector.DetectBest(sample)
	if err == nil && result.Charset != "" && result.Confidence >= 30 {
		name := strings.ToLower(result.Charset)
		// Domain trade-off: CUE sheets for music collections frequently contain Cyrillic (Windows-1251),
		// where lowercase Cyrillic bytes (0xE0..0xFF) directly overlap Hebrew consonants in ISO-8859-8.
		// Because chardet on short samples often misidentifies CP1251 as ISO-8859-8, we explicitly
		// reject ISO-8859-8 in favor of CP1251. Hebrew CUE sheets without BOM are an acceptable casualty.
		if name != "iso-8859-8-i" && name != "iso-8859-8" {
			return name
		}
	}
	return "windows-1251"
}

// detectCJK scores the four East Asian encodings and returns the label whose
// decode is dominated by exactly one script family (Hangul, kana, or Han with
// no Latin gluing). Only a strict winner is accepted, so mis-decoded Latin or
// Cyrillic text is rejected rather than guessed.
func detectCJK(sample []byte) (string, bool) {
	candidates := []struct {
		name string
		enc  encoding.Encoding
	}{
		{"euc-kr", korean.EUCKR},
		{"gb18030", simplifiedchinese.GB18030},
		{"big5", traditionalchinese.Big5},
		{"shift_jis", japanese.ShiftJIS},
	}

	bestName, bestScore := "", 0
	for _, c := range candidates {
		text, err := decodeWith(c.enc, sample)
		if err != nil {
			continue
		}
		if score, ok := scriptScore(text, c.name); ok && score > bestScore {
			bestName, bestScore = c.name, score
		}
	}
	return bestName, bestName != ""
}

// scriptScore rates a CJK decode. A replacement rune means the byte stream is
// invalid for that encoding, ruling it out immediately.
func scriptScore(text, name string) (int, bool) {
	var kana, hangul, han, latin, halfWidthKatakana, gluedHan int
	runes := []rune(text)
	for i, r := range runes {
		switch {
		case r == utf8.RuneError:
			return 0, false
		case r >= 0xFF61 && r <= 0xFF9F: // Half-width Katakana (frequent mojibake artifact)
			kana++
			halfWidthKatakana++
		case (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF): // Hiragana & Katakana
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Han, r):
			han++
			if (i > 0 && unicode.Is(unicode.Latin, runes[i-1])) || (i+1 < len(runes) && unicode.Is(unicode.Latin, runes[i+1])) {
				gluedHan++
			}
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}

	switch name {
	case "shift_jis":
		if kana+han < 3 {
			return 0, false
		}
		return kana*10 + han*5 - halfWidthKatakana*20, true
	case "euc-kr":
		if hangul == 0 {
			return 0, false
		}
		return hangul * 10, true
	case "gb18030", "big5":
		// Chinese text is pure Han: no kana/hangul, no Han glued to Latin
		// (a mojibake signature), and enough Han to be meaningful.
		if kana != 0 || hangul != 0 || han < 2 || gluedHan != 0 {
			return 0, false
		}
		if latin > 0 && han*4 < latin {
			return 0, false
		}
		return han * 8, true
	}
	return 0, false
}

// detectCyrillic distinguishes Windows-1251 from KOI8-R. The winning hypothesis
// must decode to plausible Russian: most non-ASCII bytes turn into Cyrillic,
// frequent letters dominate, and no orthographic violations (ъ/ь/ы at word
// start, hard sign before a consonant or at end of word). Calibrated on the
// CP1251/KOI8-R fixtures: the right hypothesis scores >= 0.54 with zero
// violations; the wrong one scores <= 0.45 and trips >= 2 violations.
func detectCyrillic(sample []byte) (string, bool) {
	candidates := []struct {
		name string
		enc  *charmap.Charmap
	}{
		{"windows-1251", charmap.Windows1251},
		{"koi8-r", charmap.KOI8R},
	}

	bestName, bestScore := "", 0.0
	for _, c := range candidates {
		text, err := decodeWith(c.enc, sample)
		if err != nil {
			continue
		}
		score, ok := cyrillicPlausibility(text, sample)
		if ok && score > bestScore {
			bestName, bestScore = c.name, score
		}
	}
	return bestName, bestName != ""
}

// cyrillicPlausibility scores a decoded string as plausible Cyrillic text:
// it must contain at least three Cyrillic letters that dominate the non-ASCII
// bytes, with a high share of frequent Russian letters and no impossible
// orthographic sequences. The byte-share check rejects CJK bytes accidentally
// decoded through KOI8-R (box drawings and a handful of Cyrillic lookalikes).
func cyrillicPlausibility(text string, sample []byte) (float64, bool) {
	const frequentLetters = "оеёаинтсрвлОЕЁАИНТСРВЛ"
	var cyrillic, frequent, nonASCII int
	for _, b := range sample {
		if b >= 128 {
			nonASCII++
		}
	}
	for _, r := range text {
		if unicode.Is(unicode.Cyrillic, r) {
			cyrillic++
			if strings.ContainsRune(frequentLetters, r) {
				frequent++
			}
		}
	}
	if cyrillic < 3 || float64(cyrillic)/float64(nonASCII) < 0.7 {
		return 0, false
	}
	ratio := float64(frequent) / float64(cyrillic)
	if ratio < 0.5 || !orthographicallyPlausible(text) {
		return 0, false
	}
	return ratio, true
}

// orthographicallyPlausible rejects decodes with impossible Russian letter
// sequences: no word starts with ъ/ь/ы, and ъ only precedes е, ё, ю, я.
func orthographicallyPlausible(text string) bool {
	lower := strings.ToLower(text)
	for _, w := range strings.Fields(lower) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) })
		if w == "" {
			continue
		}
		switch []rune(w)[0] {
		case 'ъ', 'ь', 'ы':
			return false
		}
	}
	runes := []rune(lower)
	for i, r := range runes {
		if r == 'ъ' && (i+1 >= len(runes) || (runes[i+1] != 'е' && runes[i+1] != 'ё' && runes[i+1] != 'ю' && runes[i+1] != 'я')) {
			return false
		}
	}
	return true
}

// detectLatin accepts Windows-1252 when the sample is mostly ASCII letters
// with a modest share of accented Latin letters — the signature of Western
// European text. Accent-heavy decodes (ratio > 0.75) are mojibake, i.e.
// Cyrillic or CJK bytes misread as Latin, and are rejected; Cyrillic and CJK
// never reach this rule because the earlier detect* steps catch them first.
func detectLatin(sample []byte) (string, bool) {
	decoded, err := decodeWith(charmap.Windows1252, sample)
	if err != nil {
		return "", false
	}
	var asciiLetters, accented, nonLatin int
	for _, r := range decoded {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			asciiLetters++
		case (r >= 0xC0 && r <= 0xFF && r != 0xD7 && r != 0xF7) ||
			r == 0x0152 || r == 0x0153 || r == 0x0160 || r == 0x0161 || r == 0x0178 || r == 0x017D || r == 0x017E:
			// Latin-1 supplement letters (excluding multiplication × and division ÷), plus Œ, œ, Š, š, Ÿ, Ž, ž
			accented++
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsDigit(r):
			// neutral
		default:
			nonLatin++
		}
	}
	if nonLatin > 0 || asciiLetters == 0 || accented == 0 {
		return "", false
	}
	ratio := float64(accented) / float64(asciiLetters+accented)
	if ratio >= 0.02 && ratio <= 0.75 {
		return "windows-1252", true
	}
	return "", false
}

func decodeWith(enc encoding.Encoding, b []byte) (string, error) {
	reader := enc.NewDecoder().Reader(bytes.NewReader(b))
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	// Check for replacement runes
	if bytes.Contains(out, []byte("\ufffd")) {
		return string(out), fmt.Errorf("contains replacement runes")
	}
	return string(out), nil
}

// MakeDecoder returns a decoder function for the given charset label.
func MakeDecoder(label string) func([]byte) string {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" || label == "utf-8" || label == "us-ascii" {
		return func(b []byte) string {
			return strings.TrimPrefix(string(b), "\ufeff")
		}
	}

	return func(b []byte) string {
		if utf8.Valid(b) {
			return strings.TrimPrefix(string(b), "\ufeff")
		}
		reader, err := charset.NewReaderLabel(label, bytes.NewReader(b))
		if err != nil {
			return string(b)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return string(b)
		}
		return strings.TrimPrefix(string(decoded), "\ufeff")
	}
}
