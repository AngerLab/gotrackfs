package cue

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type rawCommand struct {
	cmd    string
	params [][]byte
}

// ParseFile reads a CUE sheet from disk, performs two-pass parsing and encoding detection.
func ParseFile(path string) (*Sheet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cue file: %w", err)
	}
	defer f.Close()

	return Parse(f)
}

// Parse reads CUE sheet data from an io.Reader and parses it using a two-pass architecture.
func Parse(r io.Reader) (*Sheet, error) {
	rawBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read cue input: %w", err)
	}
	return ParseBytes(rawBytes)
}

// ParseString wraps string input into ParseBytes.
func ParseString(text string) (*Sheet, error) {
	return ParseBytes([]byte(text))
}

// ParseBytes parses CUE sheet bytes in two passes with automatic encoding detection and BOM/UTF-16 sniffing.
func ParseBytes(data []byte) (*Sheet, error) {
	preprocessed, err := SniffAndPreprocess(data)
	if err != nil {
		return nil, err
	}
	return parsePreprocessed(preprocessed)
}

func parsePreprocessed(data []byte) (*Sheet, error) {
	var rawCmds []rawCommand
	var sampleBuf bytes.Buffer

	// Pass 1: Byte-level tokenization & sample collection
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Bytes()
		cmd, params, err := parseCommandBytes(line)
		if err != nil || cmd == "" {
			continue
		}

		rawCmds = append(rawCmds, rawCommand{cmd: cmd, params: params})

		// Collect text fields that might contain non-ASCII characters
		switch cmd {
		case "TITLE", "PERFORMER", "SONGWRITER", "FILE", "REM":
			for _, p := range params {
				for _, b := range p {
					if b >= 128 {
						sampleBuf.Write(p)
						sampleBuf.WriteByte('\n')
						break
					}
				}
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan cue bytes: %w", err)
	}

	// Detect encoding from concentrated text payload
	charset := DetectEncoding(sampleBuf.Bytes())
	decode := MakeDecoder(charset)

	// Pass 2: Populate Sheet domain model with decoded values
	sheet := &Sheet{}
	var curFile *File
	var curTrack *Track

	for _, rc := range rawCmds {
		// Decode parameters to UTF-8
		params := make([]string, len(rc.params))
		for i, p := range rc.params {
			params[i] = decode(p)
		}

		switch rc.cmd {
		case "TITLE":
			if len(params) > 0 {
				val := params[0]
				if curTrack != nil {
					curTrack.Title = val
				} else {
					sheet.Title = val
				}
			}

		case "PERFORMER":
			if len(params) > 0 {
				val := params[0]
				if curTrack != nil {
					curTrack.Performer = val
				} else {
					sheet.Performer = val
				}
			}

		case "SONGWRITER":
			if len(params) > 0 {
				val := params[0]
				if curTrack != nil {
					curTrack.Songwriter = val
				} else {
					sheet.Songwriter = val
				}
			}

		case "FILE":
			if len(params) > 0 {
				fileType := "WAVE"
				if len(params) > 1 {
					fileType = params[1]
				}
				sheet.Files = append(sheet.Files, File{
					Name: params[0],
					Type: fileType,
				})
				curFile = &sheet.Files[len(sheet.Files)-1]
				curTrack = nil
			}

		case "TRACK":
			if len(params) > 0 {
				num, _ := strconv.Atoi(params[0])
				dataType := "AUDIO"
				if len(params) > 1 {
					dataType = params[1]
				}

				if curFile == nil {
					sheet.Files = append(sheet.Files, File{
						Name: "",
						Type: "WAVE",
					})
					curFile = &sheet.Files[len(sheet.Files)-1]
				}

				curFile.Tracks = append(curFile.Tracks, Track{
					Num:      num,
					DataType: dataType,
				})
				curTrack = &curFile.Tracks[len(curFile.Tracks)-1]
			}

		case "INDEX":
			if len(params) >= 2 && curTrack != nil {
				idxNum := params[0]
				offset, err := parseTime(params[1])
				if err == nil {
					switch idxNum {
					case "00":
						curTrack.PreGap = offset
					case "01":
						curTrack.Start = offset
						curTrack.HasIndex = true
					}
				}
			}

		case "PREGAP":
			if len(params) > 0 && curTrack != nil {
				offset, err := parseTime(params[0])
				if err == nil {
					curTrack.PreGap = offset
				}
			}

		case "CATALOG":
			if len(params) > 0 {
				sheet.Catalog = params[0]
			}

		case "CDTEXTFILE":
			if len(params) > 0 {
				sheet.CdTextFile = params[0]
			}

		case "ISRC":
			if len(params) > 0 && curTrack != nil {
				curTrack.Isrc = params[0]
			}

		case "FLAGS":
			if curTrack != nil {
				curTrack.Flags = append(curTrack.Flags, params...)
			}

		case "REM":
			parseRem(params, sheet)

		default:
			// Postel's Law: ignore unknown commands
		}
	}

	calculateTrackBoundaries(sheet)

	return sheet, nil
}

func parseRem(params []string, sheet *Sheet) {
	if len(params) == 0 {
		return
	}

	subCmd := strings.ToUpper(params[0])
	val := strings.Join(params[1:], " ")
	if len(params) == 2 {
		val = params[1]
	}

	switch subCmd {
	case "DATE", "YEAR":
		if sheet.Date == "" {
			sheet.Date = val
		}
	case "GENRE":
		if sheet.Genre == "" {
			sheet.Genre = val
		}
	case "DISCNUMBER":
		sheet.DiscNumber = val
	case "TOTALDISCS":
		sheet.TotalDiscs = val
	case "CATALOG":
		if sheet.Catalog == "" {
			sheet.Catalog = val
		}
	default:
		sheet.Comments = append(sheet.Comments, strings.Join(params, " "))
	}
}

func calculateTrackBoundaries(sheet *Sheet) {
	for fi := range sheet.Files {
		tracks := sheet.Files[fi].Tracks
		for ti := range tracks {
			if !tracks[ti].HasIndex {
				tracks[ti].Start = 0
			}
			if ti+1 < len(tracks) {
				if tracks[ti+1].PreGap > 0 && tracks[ti+1].PreGap > tracks[ti].Start {
					tracks[ti].End = tracks[ti+1].PreGap
				} else {
					tracks[ti].End = tracks[ti+1].Start
				}
			}
		}
	}
}
