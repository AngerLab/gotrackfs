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
// the best matching charset label using a multi-hypothesis scoring engine.
func DetectEncoding(sample []byte) string {
	// 1. Fast path: ASCII or valid UTF-8
	if len(sample) == 0 || utf8.Valid(sample) {
		return "utf-8"
	}

	// 2. Score candidate hypotheses
	cyrillicName, cyrillicScore := scoreCyrillicHypothesis(sample)
	latinName, latinScore := scoreLatinHypothesis(sample)
	cjkName, cjkScore := scoreCJKHypothesis(sample)

	type candidate struct {
		name  string
		score float64
	}
	candidates := []candidate{
		{cyrillicName, cyrillicScore},
		{latinName, latinScore},
		{cjkName, cjkScore},
	}

	bestName := ""
	bestScore := 0.0
	for _, c := range candidates {
		if c.score > bestScore {
			bestScore = c.score
			bestName = c.name
		}
	}

	if bestScore > 0 {
		return bestName
	}

	// 3. Statistical Fallback via Mozilla ICU (chardet)
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

	if latinScore > cyrillicScore && latinScore > 0 {
		return "windows-1252"
	}
	if cyrillicName != "" {
		return cyrillicName
	}
	return "windows-1251"
}

// scoreCyrillicHypothesis discriminates between Windows-1251 and KOI8-R.
func scoreCyrillicHypothesis(sample []byte) (string, float64) {
	s1251, err1 := decodeWith(charmap.Windows1251, sample)
	skoi8, err2 := decodeWith(charmap.KOI8R, sample)

	score1251 := evaluateCyrillicText(s1251, err1 != nil)
	scoreKOI8 := evaluateCyrillicText(skoi8, err2 != nil)

	if scoreKOI8 > score1251 && scoreKOI8 > 0 {
		return "koi8-r", scoreKOI8
	}
	return "windows-1251", score1251
}

func evaluateCyrillicText(text string, hasErr bool) float64 {
	if hasErr || len(text) == 0 {
		return -1000.0
	}

	russianVowels := "аеёиоуыэюяАЕЁИОУЫЭЮЯ"
	var cyrillicCount, vowels, casePenalties, alienPenalties int
	runes := []rune(text)

	for i, r := range runes {
		if unicode.Is(unicode.Cyrillic, r) {
			cyrillicCount++
			if strings.ContainsRune(russianVowels, r) {
				vowels++
			}
			// Penalty for alternating case (e.g. дФЕМРК in bad KOI8 decode)
			if i > 0 && unicode.IsLower(runes[i-1]) && unicode.IsUpper(r) {
				casePenalties += 20
			}
		} else if !unicode.Is(unicode.Latin, r) && !unicode.IsSpace(r) && !unicode.IsPunct(r) && !unicode.IsDigit(r) {
			// Alien symbols (like ®, ©, ¼, box drawings) inside supposed Cyrillic text
			alienPenalties += 40
		}
	}

	if cyrillicCount == 0 {
		return -100.0
	}

	score := float64(cyrillicCount) * 2.0
	vowelRatio := float64(vowels) / float64(cyrillicCount)
	if vowelRatio >= 0.20 && vowelRatio <= 0.60 {
		score += 50.0 // Strong natural vowel bonus
	} else if vowelRatio < 0.10 {
		score -= 50.0 // Consonant soup penalty
	}

	// Lexical & orthographical validation (critical for KOI8-R ALL CAPS vs CP1251)
	textLower := strings.ToLower(text)
	words := strings.Fields(textLower)

	for _, w := range words {
		wClean := strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r)
		})
		if len(wClean) == 0 {
			continue
		}
		firstRune := []rune(wClean)[0]
		// In Russian/Ukrainian/Belarusian, no word starts with ъ, ь, or ы
		if firstRune == 'ъ' || firstRune == 'ь' || firstRune == 'ы' {
			score -= 100.0
		}
		// Common Russian short words / prepositions bonus
		switch wClean {
		case "в", "и", "по", "на", "с", "не", "за", "от", "из", "к", "до", "о", "он", "мы", "ты", "во", "со":
			score += 25.0
		}
	}

	// Hard sign (ъ) grammar validation:
	// In Russian, ъ can only appear between a consonant prefix and vowels е, ё, ю, я.
	// It can NEVER be followed by a consonant or appear at the end of a word.
	runesLower := []rune(textLower)
	for i, r := range runesLower {
		if r == 'ъ' {
			score -= 20.0
			if i+1 < len(runesLower) {
				next := runesLower[i+1]
				if next != 'е' && next != 'ё' && next != 'ю' && next != 'я' {
					score -= 50.0
				}
			} else {
				score -= 50.0
			}
		}
	}

	// Frequent Russian letters frequency bonus: о, е, а, и, н, т, с, р, в, л
	for _, r := range runesLower {
		switch r {
		case 'о', 'е', 'ё', 'а', 'и':
			score += 4.0
		case 'н', 'т', 'с':
			score += 3.0
		case 'р', 'в', 'л':
			score += 2.0
		}
	}

	return score - float64(casePenalties) - float64(alienPenalties)
}

// scoreLatinHypothesis tests Windows-1252 / ISO-8859-1 for Western European text.
func scoreLatinHypothesis(sample []byte) (string, float64) {
	decoded, err := decodeWith(charmap.Windows1252, sample)
	if err != nil {
		return "", -1000.0
	}

	var asciiLetters, accentedLetters, nonLatin, alienSymbols int
	for _, r := range decoded {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			asciiLetters++
		case (r >= 0xC0 && r <= 0xFF && r != 0xD7 && r != 0xF7) ||
			r == 0x0152 || r == 0x0153 || r == 0x0160 || r == 0x0161 || r == 0x0178 || r == 0x017D || r == 0x017E:
			// Latin-1 supplement letters (excluding multiplication × and division ÷), plus Œ, œ, Š, š, Ÿ, Ž, ž
			accentedLetters++
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsDigit(r):
			continue
		case r == 0xD7 || r == 0xF7 || (r >= 0xA0 && r <= 0xBF):
			// Math symbols (×, ÷) or miscellaneous symbols (§, ©, ®, ±)
			alienSymbols++
		default:
			nonLatin++
		}
	}

	if accentedLetters == 0 {
		return "windows-1252", 0.0 // Plain ASCII or no Latin-specific accents
	}
	if nonLatin > 0 {
		return "windows-1252", -100.0 * float64(nonLatin)
	}

	totalLetters := asciiLetters + accentedLetters
	if totalLetters == 0 {
		return "windows-1252", -100.0
	}

	score := 0.0
	ratio := float64(accentedLetters) / float64(totalLetters)
	// In natural Western European languages (German, French, Spanish, etc.),
	// accented letters are 2% - 45% of the text.
	if asciiLetters > 0 && ratio >= 0.02 && ratio <= 0.45 {
		score += 50.0 + float64(accentedLetters)*10.0
	} else if ratio > 0.75 {
		// Cyrillic/CJK decoded as Windows-1252 produces 80%+ accented characters
		score -= 100.0
	}

	if alienSymbols > 0 {
		score -= float64(alienSymbols) * 30.0
	}

	return "windows-1252", score
}

// scoreCJKHypothesis tests East Asian encodings (Shift-JIS, EUC-KR, GB18030, Big5).
func scoreCJKHypothesis(sample []byte) (string, float64) {
	candidates := []struct {
		name string
		enc  encoding.Encoding
	}{
		{"shift_jis", japanese.ShiftJIS},
		{"euc-kr", korean.EUCKR},
		{"gb18030", simplifiedchinese.GB18030},
		{"big5", traditionalchinese.Big5},
	}

	bestName := ""
	bestScore := 0.0

	for _, c := range candidates {
		decoded, err := decodeWith(c.enc, sample)
		if err != nil {
			continue
		}

		score := evaluateCJKText(decoded, c.name)
		if score > bestScore {
			bestScore = score
			bestName = c.name
		}
	}

	return bestName, bestScore
}

func evaluateCJKText(text string, encName string) float64 {
	var kana, hangul, han, latin, replacement int
	var halfWidthKatakana int
	var gluedHan int
	runes := []rune(text)

	for i, r := range runes {
		switch {
		case r == utf8.RuneError:
			replacement++
		case r >= 0xFF61 && r <= 0xFF9F: // Half-width Katakana (frequent mojibake artifact)
			halfWidthKatakana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF): // Full-width Hiragana & Katakana
			kana++
		case unicode.Is(unicode.Han, r):
			han++
			// Check if Han character is glued to Latin letters (e.g. "Sch鰊e")
			if i > 0 && unicode.Is(unicode.Latin, runes[i-1]) {
				gluedHan++
			}
			if i+1 < len(runes) && unicode.Is(unicode.Latin, runes[i+1]) {
				gluedHan++
			}
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}

	if replacement > 0 {
		return -100.0 * float64(replacement)
	}

	score := 0.0
	switch encName {
	case "shift_jis":
		// Real Japanese text contains standard Kana and/or Han, without half-width katakana soup
		if halfWidthKatakana > 0 {
			score -= float64(halfWidthKatakana) * 20.0
		}
		score += float64(kana)*10.0 + float64(han)*5.0

	case "euc-kr":
		score += float64(hangul) * 10.0

	case "gb18030", "big5":
		// Chinese text has Han characters and no Kana/Hangul.
		// Strict gate: Han characters must not be glued inside Latin words (mojibake).
		if kana == 0 && hangul == 0 && halfWidthKatakana == 0 && han > 0 {
			if gluedHan > 0 {
				return -100.0 * float64(gluedHan)
			}
			if latin > 0 {
				ratio := float64(han) / float64(latin+han)
				if ratio < 0.25 || han < 2 {
					return -100.0
				}
			}
			score += float64(han) * 8.0
		}
	}

	return score
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
