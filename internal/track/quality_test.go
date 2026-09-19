package track

import (
	"strings"
	"testing"
)

func TestValidateBits(t *testing.T) {
	tests := []struct {
		bits    int
		want    int
		wantErr string
	}{
		{bits: 0, want: 0},
		{bits: 16, want: 16},
		{bits: 24, want: 24},
		{bits: 8, wantErr: "not supported"},
		{bits: 20, wantErr: "not supported"},
		{bits: 32, wantErr: "not supported"},
		{bits: -1, wantErr: "not supported"},
	}
	for _, tt := range tests {
		got, err := ValidateBits(tt.bits)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateBits(%d): error = %v, want error containing %q", tt.bits, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ValidateBits(%d): unexpected error %v", tt.bits, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ValidateBits(%d) = %d, want %d", tt.bits, got, tt.want)
		}
	}
}

func TestParseRate(t *testing.T) {
	tests := []struct {
		spec    string
		want    int
		wantErr string
	}{
		{spec: "", want: 0},
		{spec: "0", want: 0},
		{spec: "44.1", want: 44100},
		{spec: "44.1k", want: 44100},
		{spec: "44.1kHz", want: 44100},
		{spec: "48", want: 48000},
		{spec: "48k", want: 48000},
		{spec: "88.2", want: 88200},
		{spec: "96", want: 96000},
		{spec: "96kHz", want: 96000},
		{spec: "176.4", want: 176400},
		{spec: "192", want: 192000},
		{spec: "96000", want: 96000},
		{spec: "44100Hz", want: 44100},
		{spec: " 44.1 ", want: 44100},
		{spec: "abc", wantErr: "invalid sample rate"},
		{spec: "2000000", wantErr: "out of supported range"},
		{spec: "-44100", wantErr: "must be positive"},
	}
	for _, tt := range tests {
		got, err := ParseRate(tt.spec)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseRate(%q): error = %v, want error containing %q", tt.spec, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRate(%q): unexpected error %v", tt.spec, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseRate(%q) = %d, want %d", tt.spec, got, tt.want)
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
		{"rate-only cap from higher rate", Quality{SampleRate: 48000}, 96000, 16, 48000, 0},
		{"unknown source does NOT upsample blindly", Quality{Bits: 16, SampleRate: 48000}, 0, 0, 0, 0},
		{"unknown sample rate does NOT upsample", Quality{Bits: 16, SampleRate: 48000}, 0, 24, 0, 16},
		{"unknown bits does NOT upsample", Quality{Bits: 16, SampleRate: 48000}, 96000, 0, 48000, 0},
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

func TestQualityFormatBitsAndRate(t *testing.T) {
	cases := []struct {
		q        Quality
		wantBits string
		wantRate string
	}{
		{Quality{}, "unlimited", "unlimited"},
		{Quality{Bits: 24, SampleRate: 96000}, "24-bit", "96 kHz (96000 Hz)"},
		{Quality{Bits: 16, SampleRate: 44100}, "16-bit", "44.1 kHz (44100 Hz)"},
		{Quality{Bits: 16}, "16-bit", "unlimited"},
		{Quality{SampleRate: 48000}, "unlimited", "48 kHz (48000 Hz)"},
	}
	for _, c := range cases {
		gotBits := c.q.FormatBits()
		gotRate := c.q.FormatRate()
		if gotBits != c.wantBits || gotRate != c.wantRate {
			t.Errorf("Quality%+v: FormatBits()=%q, FormatRate()=%q, want (%q, %q)",
				c.q, gotBits, gotRate, c.wantBits, c.wantRate)
		}
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
