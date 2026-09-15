package cue

import (
	"path/filepath"
	"strings"
	"testing"
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
