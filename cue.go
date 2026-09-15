package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/net/html/charset"
)

var (
	trackRE = regexp.MustCompile(`^\s*TRACK\s+(\d+)\s+AUDIO`)
	indexRE = regexp.MustCompile(`^\s*INDEX\s+01\s+(\d+):(\d+):(\d+)`)
)

// decodeCue detects character encoding and converts any legacy charset
// (Shift-JIS, Windows-1251, GB18030, ISO-8859-1, etc.) into UTF-8.
func decodeCue(b []byte) (string, error) {
	// Fast path: already valid UTF-8
	if utf8.Valid(b) {
		return string(b), nil
	}

	// Statistical charset detection via Mozilla ICU algorithm
	detector := chardet.NewTextDetector()
	result, err := detector.DetectBest(b)
	if err == nil && result.Charset != "" {
		reader, err := charset.NewReaderLabel(result.Charset, bytes.NewReader(b))
		if err == nil {
			decoded, err := io.ReadAll(reader)
			if err == nil {
				return string(decoded), nil
			}
		}
	}

	// Fallback to raw string if all else fails
	return string(b), nil
}

// cueValue trims spaces and surrounding quotes from CUE values.
func cueValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// findCover searches for common album art files in the given directory.
func findCover(dir string) string {
	candidates := []string{
		"folder.jpg", "cover.jpg", "Cover.jpg",
		"folder.png", "cover.png", "Cover.png",
	}
	for _, name := range candidates {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// resolveSource locates the referenced audio file, handling case insensitivity,
// extension discrepancies (e.g. CUE points to .wav but on disk it is .flac),
// and single-image directories.
func resolveSource(base, name string) string {
	dir := filepath.Dir(base)

	// 1. Direct path check
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		return p
	}

	// 2. Basename in the CUE's directory
	p = filepath.Join(dir, filepath.Base(name))
	if _, err := os.Stat(p); err == nil {
		return p
	}

	// 3. Common audio extensions
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for _, e := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3"} {
		candidate := stem + e
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// 4. Case-insensitive search in directory
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), filepath.Base(p)) {
			return filepath.Join(dir, entry.Name())
		}
		if strings.EqualFold(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), filepath.Base(stem)) {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3":
				return filepath.Join(dir, entry.Name())
			}
		}
	}

	// 5. Fallback: single audio file in directory
	var audioFiles []string
	for _, entry := range entries {
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3":
			audioFiles = append(audioFiles, filepath.Join(dir, entry.Name()))
		}
	}
	if len(audioFiles) == 1 {
		return audioFiles[0]
	}

	return p
}

// parseCue reads and parses a CUE sheet file into an Album with Tracks.
func parseCue(path string) (*Album, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cue file: %w", err)
	}

	text, err := decodeCue(b)
	if err != nil {
		return nil, fmt.Errorf("decode cue file: %w", err)
	}

	var audioFile string
	album := &Album{}
	var curTrack *Track

	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// Album-level tags before first TRACK
		if curTrack == nil {
			switch fields[0] {
			case "TITLE":
				album.Title = cueValue(strings.TrimSpace(line[len("TITLE"):]))
			case "PERFORMER":
				album.Artist = cueValue(strings.TrimSpace(line[len("PERFORMER"):]))
			case "DATE":
				album.Year = cueValue(strings.TrimSpace(line[len("DATE"):]))
			case "GENRE":
				album.Genre = cueValue(strings.TrimSpace(line[len("GENRE"):]))
			case "REM":
				if len(fields) >= 3 {
					switch fields[1] {
					case "DATE":
						album.Year = cueValue(strings.TrimSpace(line[strings.Index(line, fields[2]):]))
					case "GENRE":
						album.Genre = cueValue(strings.TrimSpace(line[strings.Index(line, fields[2]):]))
					}
				}
			}
		}

		if fields[0] == "FILE" {
			v := strings.TrimSpace(line[len("FILE"):])
			if strings.HasPrefix(v, "\"") {
				if end := strings.Index(v[1:], "\""); end >= 0 {
					audioFile = v[1 : end+1]
				}
			} else {
				audioFile = strings.Fields(v)[0]
			}
		}

		if m := trackRE.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			album.Tracks = append(album.Tracks, Track{Num: n})
			curTrack = &album.Tracks[len(album.Tracks)-1]
		}

		if curTrack != nil {
			if fields[0] == "TITLE" {
				curTrack.Title = cueValue(strings.TrimSpace(line[len("TITLE"):]))
			}
			if fields[0] == "PERFORMER" {
				curTrack.Artist = cueValue(strings.TrimSpace(line[len("PERFORMER"):]))
			}
			if m := indexRE.FindStringSubmatch(line); m != nil {
				mm, _ := strconv.ParseFloat(m[1], 64)
				ss, _ := strconv.ParseFloat(m[2], 64)
				ff, _ := strconv.ParseFloat(m[3], 64)
				curTrack.Start = mm*60 + ss + ff/75
				curTrack.HasIndex = true
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan cue: %w", err)
	}
	if len(album.Tracks) == 0 {
		return nil, fmt.Errorf("no tracks found in %s", path)
	}

	album.CoverPath = findCover(filepath.Dir(path))
	album.SourcePath = resolveSource(path, audioFile)

	// Set sequential track boundaries
	for i := range album.Tracks {
		if !album.Tracks[i].HasIndex {
			album.Tracks[i].Start = 0
		}
		if i+1 < len(album.Tracks) {
			album.Tracks[i].End = album.Tracks[i+1].Start
		}
	}

	return album, nil
}
