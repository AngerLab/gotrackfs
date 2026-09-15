package cue

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ParseFile reads a CUE sheet from disk, auto-detects encoding, and parses it.
func ParseFile(path string) (*Sheet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cue file: %w", err)
	}
	defer f.Close()

	return Parse(f)
}

// Parse reads CUE sheet data from an io.Reader, handles encoding detection, and parses it.
func Parse(r io.Reader) (*Sheet, error) {
	text, err := DecodeReader(r)
	if err != nil {
		return nil, fmt.Errorf("decode cue: %w", err)
	}

	return ParseString(text)
}

// ParseString parses decoded UTF-8 string into a Sheet structure.
func ParseString(text string) (*Sheet, error) {
	sheet := &Sheet{}
	var curFile *File
	var curTrack *Track

	sc := bufio.NewScanner(strings.NewReader(text))
	lineNum := 0

	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if len(line) == 0 {
			continue
		}

		cmd, params, err := parseCommand(line)
		if err != nil {
			// Tolerant: continue on line parse quirks
			continue
		}
		if cmd == "" {
			continue
		}

		switch strings.ToUpper(cmd) {
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

				// If TRACK appears before any FILE, create a default implicit file entry
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
			// Postel's Law: ignore unknown commands without dying
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan cue: %w", err)
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
				// The end of the current track is the next track's start or pregap
				if tracks[ti+1].PreGap > 0 && tracks[ti+1].PreGap > tracks[ti].Start {
					tracks[ti].End = tracks[ti+1].PreGap
				} else {
					tracks[ti].End = tracks[ti+1].Start
				}
			}
		}
	}
}
