package cue

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

// parseCommand splits a CUE sheet line into command name and arguments.
// Supports double quotes ("..."), single quotes ('...'), escaped characters (\", \\),
// and unicode runes without corrupting multi-byte characters.
func parseCommand(line string) (cmd string, params []string, err error) {
	line = strings.TrimSpace(line)
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
// CD audio frames are 1/75th of a second.
func parseTime(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format %q, expected mm:ss:ff", s)
	}

	var mm, ss, ff float64
	var err error
	if mm, err = parseFloat(parts[0]); err != nil {
		return 0, fmt.Errorf("invalid minutes in %q: %w", s, err)
	}
	if ss, err = parseFloat(parts[1]); err != nil {
		return 0, fmt.Errorf("invalid seconds in %q: %w", s, err)
	}
	if ff, err = parseFloat(parts[2]); err != nil {
		return 0, fmt.Errorf("invalid frames in %q: %w", s, err)
	}

	return mm*60 + ss + ff/75.0, nil
}

func parseFloat(s string) (float64, error) {
	var val float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &val)
	return val, err
}
