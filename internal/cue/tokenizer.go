package cue

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// parseCommand is a string wrapper around parseCommandBytes for tests and convenience.
func parseCommand(line string) (cmd string, params []string, err error) {
	cmd, rawParams, err := parseCommandBytes([]byte(line))
	if err != nil {
		return "", nil, err
	}
	params = make([]string, len(rawParams))
	for i, p := range rawParams {
		params[i] = string(p)
	}
	return cmd, params, nil
}

// parseCommandBytes splits a CUE sheet line (in ASCII-compatible bytes)
// into a command string and a slice of raw parameter byte slices.
func parseCommandBytes(line []byte) (cmd string, params [][]byte, err error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	l := len(line)
	i := 0

	// Extract command (first ASCII word before whitespace)
	for i < l && line[i] > ' ' {
		i++
	}
	cmd = strings.ToUpper(string(line[:i]))

	// Skip spaces after command
	for i < l && line[i] <= ' ' {
		i++
	}

	params = make([][]byte, 0)
	var quoteChar byte = 0
	var buf bytes.Buffer

	for ; i < l; i++ {
		b := line[i]

		if quoteChar == 0 {
			if b == '"' || b == '\'' {
				if buf.Len() != 0 {
					buf.WriteByte(b)
				} else {
					quoteChar = b
				}
			} else if b <= ' ' { // ASCII whitespace
				if buf.Len() > 0 {
					params = append(params, bytes.Clone(buf.Bytes()))
					buf.Reset()
				}
			} else if b == '\\' {
				if i+1 < l {
					next := line[i+1]
					switch next {
					case '"', '\'', '\\':
						buf.WriteByte(next)
						i++
					case 'n':
						buf.WriteByte('\n')
						i++
					case 't':
						buf.WriteByte('\t')
						i++
					default:
						buf.WriteByte(b)
					}
				} else {
					buf.WriteByte(b)
				}
			} else {
				buf.WriteByte(b)
			}
		} else {
			if b == quoteChar {
				quoteChar = 0
				params = append(params, bytes.Clone(buf.Bytes()))
				buf.Reset()
			} else if b == '\\' {
				if i+1 < l {
					next := line[i+1]
					switch next {
					case '"', '\'', '\\':
						buf.WriteByte(next)
						i++
					case 'n':
						buf.WriteByte('\n')
						i++
					case 't':
						buf.WriteByte('\t')
						i++
					default:
						buf.WriteByte(b)
					}
				} else {
					buf.WriteByte(b)
				}
			} else {
				buf.WriteByte(b)
			}
		}
	}

	if buf.Len() > 0 || quoteChar != 0 {
		params = append(params, bytes.Clone(buf.Bytes()))
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
