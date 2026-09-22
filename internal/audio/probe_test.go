package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbe_RealFLACFile(t *testing.T) {
	// Probe a real (ffmpeg-encoded) FLAC file committed as a test fixture,
	// covering the pure-Go STREAMINFO parser end to end.
	realFlac := filepath.Join("testdata", "sample.flac")
	info, err := Probe(realFlac)
	if err != nil {
		t.Fatalf("Probe failed on real FLAC file: %v", err)
	}
	if math.Abs(info.Duration-1.0) > 0.01 {
		t.Errorf("expected duration ~1.0s, got %f", info.Duration)
	}
	if info.Format.SampleRate != 44100 || info.Format.Bits != 16 || info.Format.Channels != 2 {
		t.Errorf("expected 44100/16/2, got %+v", info.Format)
	}
}

func TestProbe_FLACSynthetic(t *testing.T) {
	tmpDir := t.TempDir()
	flacPath := filepath.Join(tmpDir, "test.flac")

	// Create valid synthetic FLAC streaminfo
	// "fLaC" (4 bytes)
	// block header: isLast=true, type=0, length=34
	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.Write([]byte{0x80, 0x00, 0x00, 34}) // isLast=1, type=0, len=34

	var streaminfo [34]byte
	// 44100 Hz, 2 ch, 16 bits, 441000 samples (10 seconds)
	v := uint64(44100)<<44 | uint64(1)<<41 | uint64(15)<<36 | uint64(441000)
	binary.BigEndian.PutUint64(streaminfo[10:18], v)

	buf.Write(streaminfo[:])

	if err := os.WriteFile(flacPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write synthetic flac: %v", err)
	}

	info, err := Probe(flacPath)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if math.Abs(info.Duration-10.0) > 0.001 {
		t.Errorf("expected duration 10.0, got %f", info.Duration)
	}
	if info.Format.SampleRate != 44100 || info.Format.Bits != 16 || info.Format.Channels != 2 {
		t.Errorf("expected 44100/16/2, got %+v", info.Format)
	}
}

func TestProbe_WAVSynthetic(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "test.wav")

	// 44100 Hz, 2 ch, 16 bits -> byteRate = 44100 * 2 * 2 = 176400 bytes/sec
	// 5 seconds = 882000 bytes
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+882000))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(2))      // Channels
	binary.Write(&buf, binary.LittleEndian, uint32(44100))  // Sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(176400)) // Byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(4))      // Block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))     // Bits per sample

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(882000))

	if err := os.WriteFile(wavPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write synthetic wav: %v", err)
	}

	info, err := Probe(wavPath)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if math.Abs(info.Duration-5.0) > 0.001 {
		t.Errorf("expected duration 5.0, got %f", info.Duration)
	}
	if info.Format.SampleRate != 44100 || info.Format.Bits != 16 || info.Format.Channels != 2 {
		t.Errorf("expected 44100/16/2, got %+v", info.Format)
	}
}

func TestProbe_FlacSyntheticFormats(t *testing.T) {
	tests := []struct {
		name  string
		rate  uint64
		chans uint64
		bps   uint64
	}{
		{"CD", 44100, 2, 16},
		{"hi-res", 96000, 2, 24},
		{"vinyl rip", 192000, 2, 24},
		{"mono 12", 8000, 1, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			flacPath := filepath.Join(tmpDir, "test.flac")

			var buf bytes.Buffer
			buf.WriteString("fLaC")
			buf.Write([]byte{0x80, 0x00, 0x00, 34}) // isLast=1, type=0, len=34
			var streaminfo [34]byte
			v := tt.rate<<44 | (tt.chans-1)<<41 | (tt.bps-1)<<36 | 1000
			binary.BigEndian.PutUint64(streaminfo[10:18], v)
			buf.Write(streaminfo[:])
			if err := os.WriteFile(flacPath, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}

			info, err := Probe(flacPath)
			if err != nil {
				t.Fatalf("Probe failed: %v", err)
			}
			if info.Format.SampleRate != int(tt.rate) || info.Format.Bits != int(tt.bps) || info.Format.Channels != int(tt.chans) {
				t.Errorf("Probe = %+v, want rate=%d bits=%d ch=%d", info.Format, tt.rate, tt.bps, tt.chans)
			}
		})
	}
}

func TestProbe_UnsupportedExt(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "track.ogg")
	if err := os.WriteFile(p, []byte("OggS"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(p); err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestProbe_SinglePassFLAC(t *testing.T) {
	tmpDir := t.TempDir()
	flacPath := filepath.Join(tmpDir, "test.flac")

	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.Write([]byte{0x80, 0x00, 0x00, 34})
	var streaminfo [34]byte
	// 96000 Hz, 2 ch, 24 bits, 960000 samples (10 seconds)
	v := uint64(96000)<<44 | uint64(1)<<41 | uint64(23)<<36 | uint64(960000)
	binary.BigEndian.PutUint64(streaminfo[10:18], v)
	buf.Write(streaminfo[:])
	if err := os.WriteFile(flacPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Probe(flacPath)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if math.Abs(info.Duration-10.0) > 0.001 {
		t.Errorf("expected duration 10.0, got %f", info.Duration)
	}
	if info.Format.SampleRate != 96000 || info.Format.Bits != 24 || info.Format.Channels != 2 {
		t.Errorf("expected 96000/24/2, got %+v", info.Format)
	}
}

func TestProbe_MalformedWAVMissingFmt(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "bad.wav")

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(12))
	buf.WriteString("WAVE")
	buf.WriteString("JUNK")
	binary.Write(&buf, binary.LittleEndian, uint32(4))
	buf.WriteString("1234")

	if err := os.WriteFile(wavPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Probe(wavPath); err == nil {
		t.Error("expected error for WAV missing fmt chunk")
	}
}

func TestProbe_WavPackFixture(t *testing.T) {
	// Real WavPack (.wv) file committed as a test fixture; exercises the ffprobe
	// fallback against a genuine non-FLAC/WAV container instead of a mock.
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("skipping wavpack fixture test: ffprobe not found in PATH")
	}

	wvPath := filepath.Join("testdata", "sample.wv")
	info, err := Probe(wvPath)
	if err != nil {
		t.Fatalf("Probe failed on real .wv file: %v", err)
	}

	if math.Abs(info.Duration-1.0) > 0.01 {
		t.Errorf("expected duration ~1.0s, got %f", info.Duration)
	}
	if info.Format.SampleRate != 44100 || info.Format.Bits != 16 || info.Format.Channels != 2 {
		t.Errorf("expected 44100/16/2, got %+v", info.Format)
	}
}

func TestParseFFprobeJSON(t *testing.T) {
	t.Run("standard audio format and duration", func(t *testing.T) {
		jsonBlob := []byte(`{
			"streams": [
				{
					"codec_type": "audio",
					"sample_rate": "96000",
					"channels": 2,
					"bits_per_raw_sample": "24",
					"duration": "120.5"
				}
			],
			"format": {
				"duration": "120.5"
			}
		}`)
		info, err := parseFFprobeJSON(jsonBlob)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Format.SampleRate != 96000 || info.Format.Bits != 24 || info.Format.Channels != 2 {
			t.Errorf("unexpected format: %+v", info.Format)
		}
		if math.Abs(info.Duration-120.5) > 0.001 {
			t.Errorf("unexpected duration: %f", info.Duration)
		}
	})

	t.Run("bits_per_sample fallback", func(t *testing.T) {
		jsonBlob := []byte(`{
			"streams": [
				{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"bits_per_raw_sample": "0",
					"bits_per_sample": 16
				}
			],
			"format": { "duration": "10.0" }
		}`)
		info, err := parseFFprobeJSON(jsonBlob)
		if err != nil {
			t.Fatal(err)
		}
		if info.Format.Bits != 16 {
			t.Errorf("expected 16 bits, got %d", info.Format.Bits)
		}
	})

	t.Run("sample_fmt fallback matrix", func(t *testing.T) {
		cases := []struct {
			fmtStr   string
			wantBits int
		}{
			{"s16", 16},
			{"s16p", 16},
			{"s32", 32},
			{"s32p", 32},
			{"flt", 32},
			{"fltp", 32},
			{"dbl", 64},
			{"dblp", 64},
			{"s8", 8},
			{"u8", 8},
			{"unknown", 0},
		}
		for _, tc := range cases {
			jsonBlob := []byte(`{
				"streams": [
					{
						"codec_type": "audio",
						"sample_rate": "48000",
						"channels": 2,
						"sample_fmt": "` + tc.fmtStr + `"
					}
				],
				"format": { "duration": "5.0" }
			}`)
			info, err := parseFFprobeJSON(jsonBlob)
			if err != nil {
				t.Fatalf("fmt %s failed: %v", tc.fmtStr, err)
			}
			if info.Format.Bits != tc.wantBits {
				t.Errorf("sample_fmt %s: got %d bits, want %d", tc.fmtStr, info.Format.Bits, tc.wantBits)
			}
		}
	})

	t.Run("stream duration fallback when format duration is N/A", func(t *testing.T) {
		jsonBlob := []byte(`{
			"streams": [
				{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"duration": "42.0"
				}
			],
			"format": { "duration": "N/A" }
		}`)
		info, err := parseFFprobeJSON(jsonBlob)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(info.Duration-42.0) > 0.001 {
			t.Errorf("expected stream duration 42.0, got %f", info.Duration)
		}
	})

	t.Run("picks audio stream over video artwork", func(t *testing.T) {
		jsonBlob := []byte(`{
			"streams": [
				{
					"codec_type": "video",
					"duration": "100.0"
				},
				{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"bits_per_raw_sample": "16",
					"duration": "100.0"
				}
			],
			"format": { "duration": "100.0" }
		}`)
		info, err := parseFFprobeJSON(jsonBlob)
		if err != nil {
			t.Fatal(err)
		}
		if info.Format.SampleRate != 44100 {
			t.Errorf("expected audio stream with 44100 rate, got %d", info.Format.SampleRate)
		}
	})

	t.Run("no audio stream error", func(t *testing.T) {
		jsonBlob := []byte(`{"streams": []}`)
		if _, err := parseFFprobeJSON(jsonBlob); err == nil {
			t.Error("expected error when streams array is empty")
		}
	})

	t.Run("only video stream error", func(t *testing.T) {
		jsonBlob := []byte(`{
			"streams": [
				{
					"codec_type": "video",
					"duration": "100.0"
				}
			],
			"format": { "duration": "100.0" }
		}`)
		if _, err := parseFFprobeJSON(jsonBlob); err == nil {
			t.Error("expected error when no audio stream is present in streams array")
		}
	})

	t.Run("malformed json error", func(t *testing.T) {
		if _, err := parseFFprobeJSON([]byte("{not valid json")); err == nil {
			t.Error("expected unmarshal error")
		}
	})
}

func TestProbe_WAVOddFmtChunkPadding(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "odd_fmt.wav")

	// RIFF header
	// fmt chunk: 17 bytes (odd!), so there is 1 byte padding after fmt chunk
	// data chunk: 8 bytes
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(4+8+18+8+8)) // RIFF length
	buf.WriteString("WAVE")

	// fmt chunk (17 bytes: 16 bytes format + 1 extra byte)
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(17))     // chunkSize = 17 (odd)
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(2))      // 2 channels
	binary.Write(&buf, binary.LittleEndian, uint32(44100))  // sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(176400)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(4))      // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))     // bits per sample
	buf.WriteByte(0x00)                                     // 17th byte of fmt chunk
	buf.WriteByte(0x00)                                     // pad byte (to make 18 bytes on disk)

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(176400)) // 1 second of audio
	buf.Write(make([]byte, 176400))

	if err := os.WriteFile(wavPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := Probe(wavPath)
	if err != nil {
		t.Fatalf("Probe failed on WAV with odd fmt chunk: %v", err)
	}
	if math.Abs(info.Duration-1.0) > 0.001 {
		t.Errorf("expected duration 1.0s, got %f", info.Duration)
	}
	if info.Format.SampleRate != 44100 || info.Format.Bits != 16 || info.Format.Channels != 2 {
		t.Errorf("unexpected format: %+v", info.Format)
	}
}

func TestProbe_FFprobeRunnerMock(t *testing.T) {
	origRunner := ffprobeRunner
	defer func() { ffprobeRunner = origRunner }()

	tmpDir := t.TempDir()

	t.Run("non-flac file calls ffprobe runner", func(t *testing.T) {
		called := false
		ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
			called = true
			return []byte(`{
				"streams": [{
					"codec_type": "audio",
					"sample_rate": "192000",
					"channels": 2,
					"bits_per_raw_sample": "24"
				}],
				"format": { "duration": "300.0" }
			}`), nil
		}

		p := filepath.Join(tmpDir, "album.wv")
		if err := os.WriteFile(p, []byte("fake-wv-data"), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := Probe(p)
		if err != nil {
			t.Fatalf("Probe failed: %v", err)
		}
		if !called {
			t.Error("expected ffprobe runner to be called for .wv file")
		}
		if info.Format.SampleRate != 192000 || info.Format.Bits != 24 || info.Duration != 300.0 {
			t.Errorf("unexpected info: %+v", info)
		}
	})

	t.Run("broken flac falls back to ffprobe", func(t *testing.T) {
		called := false
		ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
			called = true
			return []byte(`{
				"streams": [{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"bits_per_raw_sample": "16"
				}],
				"format": { "duration": "50.0" }
			}`), nil
		}

		p := filepath.Join(tmpDir, "corrupt.flac")
		if err := os.WriteFile(p, []byte("fLaC\x00\x00"), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := Probe(p)
		if err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if !called {
			t.Error("expected ffprobe runner to be called on corrupt flac")
		}
		if info.Format.SampleRate != 44100 {
			t.Errorf("unexpected rate: %d", info.Format.SampleRate)
		}
	})

	t.Run("flac with zero total samples falls back to ffprobe", func(t *testing.T) {
		called := false
		ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
			called = true
			return []byte(`{
				"streams": [{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"bits_per_raw_sample": "16"
				}],
				"format": { "duration": "123.4" }
			}`), nil
		}

		// Valid FLAC header with TotalSamples == 0
		var buf bytes.Buffer
		buf.WriteString("fLaC")
		buf.Write([]byte{0x80, 0x00, 0x00, 34}) // isLast=1, type=0, len=34
		var streaminfo [34]byte
		v := uint64(44100)<<44 | uint64(1)<<41 | uint64(15)<<36 | 0 // 0 total samples
		binary.BigEndian.PutUint64(streaminfo[10:18], v)
		buf.Write(streaminfo[:])

		p := filepath.Join(tmpDir, "stream.flac")
		if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := Probe(p)
		if err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if !called {
			t.Error("expected ffprobe to be called when FLAC has TotalSamples == 0")
		}
		if math.Abs(info.Duration-123.4) > 0.001 {
			t.Errorf("expected duration 123.4 from ffprobe, got %f", info.Duration)
		}
	})

	t.Run("wav with missing data chunk falls back to ffprobe", func(t *testing.T) {
		called := false
		ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
			called = true
			return []byte(`{
				"streams": [{
					"codec_type": "audio",
					"sample_rate": "44100",
					"channels": 2,
					"bits_per_raw_sample": "16"
				}],
				"format": { "duration": "75.0" }
			}`), nil
		}

		// Valid RIFF WAVE header and fmt chunk, but NO data chunk (DataSize == 0)
		var buf bytes.Buffer
		buf.WriteString("RIFF")
		binary.Write(&buf, binary.LittleEndian, uint32(36))
		buf.WriteString("WAVE")
		buf.WriteString("fmt ")
		binary.Write(&buf, binary.LittleEndian, uint32(16))
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(2))
		binary.Write(&buf, binary.LittleEndian, uint32(44100))
		binary.Write(&buf, binary.LittleEndian, uint32(176400))
		binary.Write(&buf, binary.LittleEndian, uint16(4))
		binary.Write(&buf, binary.LittleEndian, uint16(16))

		p := filepath.Join(tmpDir, "nodata.wav")
		if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}

		info, err := Probe(p)
		if err != nil {
			t.Fatalf("Probe failed: %v", err)
		}
		if !called {
			t.Error("expected ffprobe to be called when WAV has DataSize == 0")
		}
		if info.Duration != 75.0 {
			t.Errorf("expected duration 75.0 from ffprobe fallback, got %f", info.Duration)
		}
	})

	t.Run("errors wrap ErrNotSupported with underlying details", func(t *testing.T) {
		ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
			return nil, errors.New("command timed out")
		}

		p := filepath.Join(tmpDir, "broken.wav")
		if err := os.WriteFile(p, []byte("RIFF1234WAVEJUNK"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := Probe(p)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("expected error to match ErrNotSupported, got: %v", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "command timed out") {
			t.Errorf("expected error to mention ffprobe failure, got: %s", msg)
		}
		if !strings.Contains(msg, "wav parser") {
			t.Errorf("expected error to mention direct parser failure, got: %s", msg)
		}
	})
}
