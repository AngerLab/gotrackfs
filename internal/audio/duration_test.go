package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestProbe_RealFile(t *testing.T) {
	// Probe real FLAC if available in playground
	realFlac := filepath.Join("..", "..", ".playground", "SOURCE", "Dartz - Proxima Parada", "Dartz - Proxima Parada.flac")
	if _, err := os.Stat(realFlac); err != nil {
		t.Skip("skipping real flac test: file not found")
	}

	info, err := Probe(realFlac)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	// 150468612 / 44100 = 3411.986666... seconds
	expected := 3411.9866
	if math.Abs(info.Duration-expected) > 0.1 {
		t.Errorf("expected duration ~%.2f, got %.2f", expected, info.Duration)
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
