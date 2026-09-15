package cue

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// parseCommand splits a CUE sheet line into command name and arguments.
// Supports double quotes ("..."), single quotes ('...'), escaped characters (\", \\),
// and unicode runes without corrupting multi-byte characters.
func parseCommand(line string) (cmd string, params []string, err error) {
	line = strings.TrimSpace(line)
	// Strip zero-width BOM rune if it leaked into the line
	line = strings.TrimPrefix(line, "\ufeff")
	if len(line) == 0 {
		return "", nil, nil
	}

	runes := []rune(line)
	l := len(runes)

	// Extract command (first space-delimited word)
	i := 0
	for i < l && !unicode.IsSpace(runes[i]) {
		i++
	}
	cmd = string(runes[:i])

	// Skip spaces between command and parameters
	for i < l && unicode.IsSpace(runes[i]) {
		i++
	}

	params = make([]string, 0)
	var quoteChar rune = 0
	var buf bytes.Buffer

	for ; i < l; i++ {
		r := runes[i]

		if quoteChar == 0 {
			if r == '"' || r == '\'' {
				if buf.Len() != 0 {
					buf.WriteRune(r)
				} else {
					quoteChar = r
				}
			} else if unicode.IsSpace(r) {
				if buf.Len() > 0 {
					params = append(params, buf.String())
					buf.Reset()
				}
			} else if r == '\\' {
				if i+1 < l {
					next := runes[i+1]
					switch next {
					case '"', '\'', '\\':
						buf.WriteRune(next)
						i++
					case 'n':
						buf.WriteRune('\n')
						i++
					case 't':
						buf.WriteRune('\t')
						i++
					default:
						buf.WriteRune(r)
					}
				} else {
					buf.WriteRune(r)
				}
			} else {
				buf.WriteRune(r)
			}
		} else {
			if r == quoteChar {
				quoteChar = 0
				params = append(params, buf.String())
				buf.Reset()
			} else if r == '\\' {
				if i+1 < l {
					next := runes[i+1]
					switch next {
					case '"', '\'', '\\':
						buf.WriteRune(next)
						i++
					case 'n':
						buf.WriteRune('\n')
						i++
					case 't':
						buf.WriteRune('\t')
						i++
					default:
						buf.WriteRune(r)
					}
				} else {
					buf.WriteRune(r)
				}
			} else {
				buf.WriteRune(r)
			}
		}
	}

	if buf.Len() > 0 || quoteChar != 0 {
		params = append(params, buf.String())
	}

	return cmd, params, nil
}

// parseTime converts mm:ss:ff timestamp string into seconds (float64).
// Validates boundaries according to Red Book Audio CD specifications:
// minutes >= 0, 0 <= seconds <= 59, 0 <= frames <= 74 (75 frames per second).
func parseTime(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format %q, expected mm:ss:ff", s)
	}

	min, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || min < 0 {
		return 0, fmt.Errorf("invalid minutes %q in %q", parts[0], s)
	}

	sec, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || sec < 0 || sec > 59 {
		return 0, fmt.Errorf("invalid seconds %q in %q (must be 0..59)", parts[1], s)
	}

	frames, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || frames < 0 || frames > 74 {
		return 0, fmt.Errorf("invalid frames %q in %q (must be 0..74)", parts[2], s)
	}

	return float64(min)*60.0 + float64(sec) + float64(frames)/75.0, nil
}
