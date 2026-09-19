package track

import (
	"strings"
	"testing"
	"time"
)

func TestParseQuality(t *testing.T) {
	tests := []struct {
		spec    string
		want    Quality
		wantErr string
	}{
		{spec: "", want: Quality{}},
		{spec: "24/96", want: Quality{Bits: 24, SampleRate: 96000}},
		{spec: "24/96000", want: Quality{Bits: 24, SampleRate: 96000}},
		{spec: "16/44.1", want: Quality{Bits: 16, SampleRate: 44100}},
		{spec: "16/44100", want: Quality{Bits: 16, SampleRate: 44100}},
		{spec: " 20 / 88.2 ", want: Quality{Bits: 20, SampleRate: 88200}},
		{spec: "96", want: Quality{SampleRate: 96000}},
		{spec: "192000", want: Quality{SampleRate: 192000}},
		{spec: "22/96", wantErr: "legal FLAC depth"},
		{spec: "32/96", wantErr: "legal FLAC depth"},
		{spec: "24/48000/2", wantErr: `want "bits/rate"`},
		{spec: "24/", wantErr: `want "bits/rate"`},
		{spec: "/96", wantErr: `want "bits/rate"`},
		{spec: "24/hz", wantErr: "invalid sample rate"},
		{spec: "ab/96", wantErr: "invalid bit depth"},
		{spec: "16/2000000", wantErr: "out of range"},
		{spec: "16/0", wantErr: "out of range"},
	}
	for _, tt := range tests {
		got, err := ParseQuality(tt.spec)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseQuality(%q): error = %v, want error containing %q", tt.spec, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQuality(%q): unexpected error %v", tt.spec, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseQuality(%q) = %+v, want %+v", tt.spec, got, tt.want)
		}
	}
}

func TestQualityUnset(t *testing.T) {
	if !(Quality{}).Unset() {
		t.Error("zero Quality must be Unset")
	}
	if (Quality{SampleRate: 96000}).Unset() {
		t.Error("rate-only Quality must not be Unset")
	}
	if (Quality{Bits: 24}).Unset() {
		t.Error("bits-only Quality must not be Unset")
	}
}

func TestQualityPlan(t *testing.T) {
	tests := []struct {
		name               string
		q                  Quality
		srcRate, srcBits   int
		wantRate, wantBits int
	}{
		{"unset keeps everything", Quality{}, 192000, 24, 0, 0},
		{"24/96 from 24/192 lowers rate only", Quality{Bits: 24, SampleRate: 96000}, 192000, 24, 96000, 0},
		{"24/96 from 24/96 untouched", Quality{Bits: 24, SampleRate: 96000}, 96000, 24, 0, 0},
		{"24/96 from 16/44.1 untouched", Quality{Bits: 24, SampleRate: 96000}, 44100, 16, 0, 0},
		{"16/44.1 from 24/192 lowers both", Quality{Bits: 16, SampleRate: 44100}, 192000, 24, 44100, 16},
		{"cap 20 bits from 24", Quality{Bits: 20}, 96000, 24, 0, 20},
		{"illegal cap rounds down", Quality{Bits: 22}, 96000, 24, 0, 20},
		{"rate-only cap from higher rate", Quality{SampleRate: 48000}, 96000, 16, 48000, 0},
		{"unknown source applies cap blindly (bits)", Quality{Bits: 16, SampleRate: 48000}, 0, 0, 48000, 16},
		{"rate below cap left alone", Quality{Bits: 16, SampleRate: 48000}, 44100, 24, 0, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, b := tt.q.Plan(tt.srcRate, tt.srcBits)
			if r != tt.wantRate || b != tt.wantBits {
				t.Errorf("Plan(%d,%d) = (%d,%d), want (%d,%d)", tt.srcRate, tt.srcBits, r, b, tt.wantRate, tt.wantBits)
			}
		})
	}
}

func TestSliceKeyIncludesQualityTargets(t *testing.T) {
	base := Slice{
		SourceAudioPath: "/music/album.flac",
		SourceModTime:   time.Unix(1700000000, 0),
		SourceSize:      123456,
		Start:           0,
		End:             60,
	}
	original := base.Key()

	resampled := base
	resampled.TargetSampleRate = 96000
	if resampled.Key() == original {
		t.Error("TargetSampleRate must change the cache key")
	}

	redepthed := base
	redepthed.TargetBits = 16
	if redepthed.Key() == original || redepthed.Key() == resampled.Key() {
		t.Error("TargetBits must change the cache key independently")
	}

	identical := base
	if identical.Key() != original {
		t.Error("key must be deterministic")
	}
}

func TestQualityString(t *testing.T) {
	cases := []struct {
		q    Quality
		want string
	}{
		{Quality{Bits: 24, SampleRate: 96000}, "24/96000"},
		{Quality{SampleRate: 96000}, "*/96000"},
		{Quality{Bits: 16}, "16/*"},
	}
	for _, c := range cases {
		if got := c.q.String(); got != c.want {
			t.Errorf("Quality.String() = %q, want %q", got, c.want)
		}
	}
}
