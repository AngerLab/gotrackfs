package cue

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestTokenizer(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantCmd    string
		wantParams []string
	}{
		{
			name:       "Simple command",
			line:       `TITLE "OK Computer"`,
			wantCmd:    "TITLE",
			wantParams: []string{"OK Computer"},
		},
		{
			name:       "Unquoted parameters",
			line:       `TRACK 01 AUDIO`,
			wantCmd:    "TRACK",
			wantParams: []string{"01", "AUDIO"},
		},
		{
			name:       "Mixed quoted and unquoted",
			line:       `FILE "CDImage.flac" WAVE`,
			wantCmd:    "FILE",
			wantParams: []string{"CDImage.flac", "WAVE"},
		},
		{
			name:       "Single quotes and escaped characters",
			line:       `TITLE 'Rock\'n\'Roll "Live"'`,
			wantCmd:    "TITLE",
			wantParams: []string{`Rock'n'Roll "Live"`},
		},
		{
			name:       "Unicode characters in quotes",
			line:       `PERFORMER "Борис Гребенщиков"`,
			wantCmd:    "PERFORMER",
			wantParams: []string{"Борис Гребенщиков"},
		},
		{
			name:       "Japanese characters",
			line:       `TITLE "プラスティック・ラブ"`,
			wantCmd:    "TITLE",
			wantParams: []string{"プラスティック・ラブ"},
		},
		{
			name:       "Empty line",
			line:       `   `,
			wantCmd:    "",
			wantParams: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, params, err := parseCommand(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if len(params) != len(tt.wantParams) {
				t.Fatalf("params len = %d, want %d (%v)", len(params), len(tt.wantParams), params)
			}
			for i := range params {
				if params[i] != tt.wantParams[i] {
					t.Errorf("param[%d] = %q, want %q", i, params[i], tt.wantParams[i])
				}
			}
		})
	}
}

func TestParseRealCUEFiles(t *testing.T) {
	testdataDir := filepath.Join("..", "..", "testdata")

	tests := []struct {
		name          string
		filename      string
		wantPerformer string
		wantTitle     string
		wantDate      string
		wantGenre     string
		wantTracks    int
	}{
		{
			name:          "Fleur - Storm Warning (Windows-1251, EAC)",
			filename:      "fleur.cue",
			wantPerformer: "Fleur",
			wantTitle:     "Штормовое предупреждение",
			wantDate:      "2014",
			wantGenre:     "Rock",
			wantTracks:    11,
		},
		{
			name:          "Max Raabe - Super Hits (UTF-8, German umlauts)",
			filename:      "max_raabe.cue",
			wantPerformer: "Palast Orchester mit seinem Sänger Max Raabe",
			wantTitle:     "Super Hits Nummer 2",
			wantDate:      "",
			wantGenre:     "",
			wantTracks:    12,
		},
		{
			name:          "Boris Grebenshchikov - Sign of Fire (Windows-1251)",
			filename:      "bg_znak_ognya.cue",
			wantPerformer: "Борис Гребенщиков",
			wantTitle:     "Знак Огня",
			wantDate:      "2020",
			wantGenre:     "",
			wantTracks:    13,
		},
		{
			name:          "Pavel Pikovski - Hugo's Tales (Complex REM tags, vinyl catalog)",
			filename:      "pikovski.cue",
			wantPerformer: "Павел Пиковский",
			wantTitle:     "Сказки Хьюго",
			wantDate:      "2014",
			wantGenre:     "",
			wantTracks:    20,
		},
		{
			name:          "Wind Rose - Wintersaga (WavPack, INDEX 00 pre-gaps)",
			filename:      "wind_rose.cue",
			wantPerformer: "Wind Rose",
			wantTitle:     "Wintersaga",
			wantDate:      "2019",
			wantGenre:     "Epic Folk Metal",
			wantTracks:    9,
		},
		{
			name:          "Hamilton CD1 (Multi-file Broadway cast, cross-file INDEX 00)",
			filename:      "hamilton.cue",
			wantPerformer: "Original Broadway Cast",
			wantTitle:     "Hamilton CD1",
			wantDate:      "2015",
			wantGenre:     "Musical",
			wantTracks:    23,
		},
		{
			name:          "Piknik - 45 CD1 (2025 release, Windows-1251 with Russian filenames)",
			filename:      "piknik_45.cue",
			wantPerformer: "Пикник",
			wantTitle:     "45 CD1",
			wantDate:      "2025",
			wantGenre:     "Psychedelic Rock",
			wantTracks:    15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(testdataDir, tt.filename)
			sheet, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(%s) failed: %v", tt.filename, err)
			}

			if tt.wantPerformer != "" && !strings.Contains(sheet.Performer, tt.wantPerformer) {
				t.Errorf("Performer = %q, want to contain %q", sheet.Performer, tt.wantPerformer)
			}

			if tt.wantTitle != "" && !strings.Contains(sheet.Title, tt.wantTitle) {
				t.Errorf("Title = %q, want to contain %q", sheet.Title, tt.wantTitle)
			}

			if tt.wantDate != "" && sheet.Date != tt.wantDate {
				t.Errorf("Date = %q, want %q", sheet.Date, tt.wantDate)
			}

			if tt.wantGenre != "" && sheet.Genre != tt.wantGenre {
				t.Errorf("Genre = %q, want %q", sheet.Genre, tt.wantGenre)
			}

			totalTracks := sheet.TotalTracks()
			if totalTracks != tt.wantTracks {
				t.Errorf("TotalTracks = %d, want exactly %d", totalTracks, tt.wantTracks)
			}

			// Validate that every track has a title and sequential start times
			tracks := sheet.AllTracks()
			for i, tr := range tracks {
				if tr.Title == "" {
					t.Errorf("Track %d has empty title", tr.Num)
				}
				if i > 0 && tr.Start < tracks[i-1].Start {
					t.Errorf("Track %d start (%f) is before previous track (%f)", tr.Num, tr.Start, tracks[i-1].Start)
				}
			}
		})
	}
}

func TestParseTime_Validation(t *testing.T) {
	tests := []struct {
		input   string
		wantSec float64
		wantErr bool
	}{
		{"00:00:00", 0.0, false},
		{"01:30:37", 90.0 + 37.0/75.0, false},
		{"74:59:74", 74*60.0 + 59.0 + 74.0/75.0, false},
		// Invalid seconds (>= 60)
		{"05:80:00", 0, true},
		{"00:60:00", 0, true},
		// Invalid frames (>= 75)
		{"05:00:75", 0, true},
		{"05:00:99", 0, true},
		// Negative values
		{"-01:00:00", 0, true},
		{"01:-05:00", 0, true},
		{"01:00:-10", 0, true},
		// Malformed format
		{"01:00", 0, true},
		{"abc:00:00", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseTime(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && (got-tt.wantSec > 0.0001 || tt.wantSec-got > 0.0001) {
				t.Errorf("parseTime(%q) = %f, want %f", tt.input, got, tt.wantSec)
			}
		})
	}
}

func TestParse_BOMStripping(t *testing.T) {
	cueWithBOM := "\xef\xbb\xbfTITLE \"Album With BOM\"\nPERFORMER \"BOM Artist\"\nFILE \"test.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"Track 1\"\n    INDEX 01 00:00:00"
	sheet, err := Parse(strings.NewReader(cueWithBOM))
	if err != nil {
		t.Fatalf("unexpected error parsing CUE with BOM: %v", err)
	}

	if sheet.Title != "Album With BOM" {
		t.Errorf("Title = %q, want %q (BOM might not have been stripped)", sheet.Title, "Album With BOM")
	}
	if sheet.Performer != "BOM Artist" {
		t.Errorf("Performer = %q, want %q", sheet.Performer, "BOM Artist")
	}
}

func TestParse_UTF16(t *testing.T) {
	// Create UTF-16LE bytes with BOM (FF FE)
	cueText := `TITLE "UTF-16 Album"
PERFORMER "UTF-16 Artist"
FILE "track.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`

	var buf bytes.Buffer
	buf.Write([]byte{0xff, 0xfe}) // UTF-16LE BOM
	for _, r := range cueText {
		buf.WriteByte(byte(r))
		buf.WriteByte(byte(r >> 8))
	}

	sheet, err := Parse(&buf)
	if err != nil {
		t.Fatalf("unexpected error parsing UTF-16 CUE: %v", err)
	}

	if sheet.Title != "UTF-16 Album" {
		t.Errorf("Title = %q, want %q", sheet.Title, "UTF-16 Album")
	}
	if sheet.Performer != "UTF-16 Artist" {
		t.Errorf("Performer = %q, want %q", sheet.Performer, "UTF-16 Artist")
	}
}

func TestParse_ShortEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		title        string
		performer    string
		encoder      encoding.Encoding
		wantCyrillic bool
	}{
		{
			name:         "Short Cyrillic (Gentlemen) in CP1251",
			title:        "Джентльмены",
			performer:    "Меладзе",
			encoder:      charmap.Windows1251,
			wantCyrillic: true,
		},
		{
			name:         "Short Cyrillic in KOI8-R",
			title:        "Звезда по имени Солнце",
			performer:    "Кино",
			encoder:      charmap.KOI8R,
			wantCyrillic: true,
		},
		{
			name:         "ALL CAPS Cyrillic in KOI8-R",
			title:        "ЗВЕЗДА ПО ИМЕНИ СОЛНЦЕ",
			performer:    "КИНО",
			encoder:      charmap.KOI8R,
			wantCyrillic: true,
		},
		{
			name:      "Western European in Windows-1252",
			title:     "Die Schöne Müllerin",
			performer: "Franz Schubert",
			encoder:   charmap.Windows1252,
		},
		{
			name:      "French in Windows-1252",
			title:     "La Vie en rose",
			performer: "Édith Piaf",
			encoder:   charmap.Windows1252,
		},
		{
			name:      "Korean in EUC-KR",
			title:     "봄날",
			performer: "방탄소년단",
			encoder:   korean.EUCKR,
		},
		{
			name:      "Chinese in GB18030",
			title:     "十年",
			performer: "陈奕迅",
			encoder:   simplifiedchinese.GB18030,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawCue := "TITLE \"" + tt.title + "\"\nPERFORMER \"" + tt.performer + "\"\nFILE \"01.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"" + tt.title + "\"\n    INDEX 01 00:00:00"
			enc := tt.encoder.NewEncoder()
			encoded, err := enc.Bytes([]byte(rawCue))
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}

			sheet, err := ParseBytes(encoded)
			if err != nil {
				t.Fatalf("ParseBytes failed: %v", err)
			}

			if sheet.Title != tt.title {
				t.Errorf("Title = %q, want %q (got mojibake)", sheet.Title, tt.title)
			}
			if sheet.Performer != tt.performer {
				t.Errorf("Performer = %q, want %q", sheet.Performer, tt.performer)
			}
		})
	}
}

func TestParseBytes_DirectUTF16(t *testing.T) {
	// Directly test ParseBytes with raw UTF-16LE including surrogate pair (musical G clef 𝄞 U+1D11E)
	cueText := "TITLE \"Clef 𝄞 Symphony\"\nPERFORMER \"Artist\"\nFILE \"a.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"T1\"\n    INDEX 01 00:00:00"
	var buf bytes.Buffer
	buf.Write([]byte{0xff, 0xfe}) // UTF-16LE BOM

	for _, r := range cueText {
		if r > 0xFFFF {
			// Encode surrogate pair
			r -= 0x10000
			high := uint16(0xD800 + (r >> 10))
			low := uint16(0xDC00 + (r & 0x3FF))
			buf.WriteByte(byte(high))
			buf.WriteByte(byte(high >> 8))
			buf.WriteByte(byte(low))
			buf.WriteByte(byte(low >> 8))
		} else {
			buf.WriteByte(byte(r))
			buf.WriteByte(byte(r >> 8))
		}
	}

	sheet, err := ParseBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseBytes with UTF-16 failed: %v", err)
	}
	if sheet.Title != "Clef 𝄞 Symphony" {
		t.Errorf("Title = %q, want %q", sheet.Title, "Clef 𝄞 Symphony")
	}
}
