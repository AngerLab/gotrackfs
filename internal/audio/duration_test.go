package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeFLACDuration_RealFile(t *testing.T) {
	// Probe real FLAC if available in playground
	realFlac := filepath.Join("..", "..", ".playground", "SOURCE", "Dartz - Proxima Parada", "Dartz - Proxima Parada.flac")
	if _, err := os.Stat(realFlac); err != nil {
		t.Skip("skipping real flac test: file not found")
	}

	dur, err := ProbeDuration(realFlac)
	if err != nil {
		t.Fatalf("ProbeDuration failed: %v", err)
	}

	// 150468612 / 44100 = 3411.986666... seconds
	expected := 3411.9866
	if math.Abs(dur-expected) > 0.1 {
		t.Errorf("expected duration ~%.2f, got %.2f", expected, dur)
	}
}

func TestProbeFLACDuration_Synthetic(t *testing.T) {
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
	// sample_rate: 44100 (0xAC44, 20 bits)
	// ch-1: 1 (3 bits)
	// bps-1: 15 (5 bits)
	// total_samples: 441000 (0x6BAE8, 36 bits)
	v := uint64(44100)<<44 | uint64(1)<<41 | uint64(15)<<36 | uint64(441000)
	binary.BigEndian.PutUint64(streaminfo[10:18], v)

	buf.Write(streaminfo[:])

	if err := os.WriteFile(flacPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write synthetic flac: %v", err)
	}

	dur, err := ProbeDuration(flacPath)
	if err != nil {
		t.Fatalf("ProbeDuration failed: %v", err)
	}

	if math.Abs(dur-10.0) > 0.001 {
		t.Errorf("expected duration 10.0, got %f", dur)
	}
}

func TestProbeWAVDuration_Synthetic(t *testing.T) {
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

	dur, err := ProbeDuration(wavPath)
	if err != nil {
		t.Fatalf("ProbeDuration failed: %v", err)
	}

	if math.Abs(dur-5.0) > 0.001 {
		t.Errorf("expected duration 5.0, got %f", dur)
	}
}
