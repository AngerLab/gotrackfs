package main

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
)

func TestDecodeCue_UTF8(t *testing.T) {
	input := `TITLE "Random Access Memories"
PERFORMER "Daft Punk"`
	decoded, err := decodeCue([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != input {
		t.Fatalf("expected %q, got %q", input, decoded)
	}
}

func TestDecodeCue_Windows1251(t *testing.T) {
	russianText := `TITLE "Звезда по имени Солнце"
PERFORMER "Кино"
REM GENRE "Пост-панк"
TRACK 01 AUDIO
  TITLE "Песня без слов"`

	// Encode to Windows-1251 bytes
	encoder := charmap.Windows1251.NewEncoder()
	encodedBytes, err := encoder.Bytes([]byte(russianText))
	if err != nil {
		t.Fatalf("failed to encode to windows-1251: %v", err)
	}

	decoded, err := decodeCue(encodedBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(decoded, "Звезда по имени Солнце") || !strings.Contains(decoded, "Кино") {
		t.Fatalf("failed to decode Windows-1251 properly, got: %s", decoded)
	}
}

func TestDecodeCue_ShiftJIS(t *testing.T) {
	japaneseText := `TITLE "プラスティック・ラブ"
PERFORMER "竹内まりや"
REM GENRE "シティ・ポップ"
TRACK 01 AUDIO
  TITLE "Plastic Love"`

	// Encode to Shift-JIS bytes
	encoder := japanese.ShiftJIS.NewEncoder()
	encodedBytes, err := encoder.Bytes([]byte(japaneseText))
	if err != nil {
		t.Fatalf("failed to encode to Shift-JIS: %v", err)
	}

	decoded, err := decodeCue(encodedBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(decoded, "プラスティック・ラブ") || !strings.Contains(decoded, "竹内まりや") {
		t.Fatalf("failed to decode Shift-JIS properly, got: %s", decoded)
	}
}

func TestCueValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"Hello World"`, "Hello World"},
		{`Hello World`, "Hello World"},
		{`  "Quoted"  `, "Quoted"},
		{`""`, ""},
		{``, ""},
	}
	for _, tc := range tests {
		got := cueValue(tc.input)
		if got != tc.expected {
			t.Errorf("cueValue(%q) = %q; expected %q", tc.input, got, tc.expected)
		}
	}
}
