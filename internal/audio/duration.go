package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotSupported is returned when the audio format is not supported or duration cannot be determined.
var ErrNotSupported = errors.New("audio: unsupported format or unreadable duration")

// ProbeDuration attempts to determine the exact duration in seconds of an audio file
// by reading header metadata (FLAC STREAMINFO or WAV fmt/data chunks) without external tools.
func ProbeDuration(filePath string) (float64, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".flac":
		return probeFLACDuration(filePath)
	case ".wav", ".wave":
		return probeWAVDuration(filePath)
	default:
		return 0, ErrNotSupported
	}
}

// probeFLACDuration reads the FLAC STREAMINFO metadata block (34 bytes).
func probeFLACDuration(filePath string) (float64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return 0, err
	}
	if string(magic[:]) != "fLaC" {
		return 0, fmt.Errorf("not a flac file: %q", magic)
	}

	// Loop metadata blocks until STREAMINFO (block type 0, usually the very first block)
	for {
		var header [4]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return 0, err
		}

		isLast := (header[0] & 0x80) != 0
		blockType := header[0] & 0x7F
		length := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])

		if blockType == 0 { // STREAMINFO
			if length < 34 {
				return 0, errors.New("flac: STREAMINFO block too small")
			}
			var data [34]byte
			if _, err := io.ReadFull(f, data[:]); err != nil {
				return 0, err
			}

			// Bytes 10-17:
			// sample_rate (20 bits), channels-1 (3 bits), bps-1 (5 bits), total_samples (36 bits)
			v := binary.BigEndian.Uint64(data[10:18])
			sampleRate := uint32(v >> 44)
			totalSamples := v & 0xFFFFFFFFF

			if sampleRate == 0 || totalSamples == 0 {
				return 0, errors.New("flac: zero sample rate or total samples in STREAMINFO")
			}

			return float64(totalSamples) / float64(sampleRate), nil
		}

		if isLast {
			break
		}

		if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
			return 0, err
		}
	}

	return 0, errors.New("flac: STREAMINFO block not found")
}

// probeWAVDuration reads RIFF WAVE header fmt and data chunks to calculate exact duration.
func probeWAVDuration(filePath string) (float64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, err
	}

	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return 0, errors.New("wav: not a valid RIFF WAVE file")
	}

	var byteRate uint32
	var dataSize uint32

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(f, chunkHeader[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return 0, err
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		if chunkID == "fmt " && chunkSize >= 16 {
			var fmtData [16]byte
			if _, err := io.ReadFull(f, fmtData[:]); err != nil {
				return 0, err
			}
			byteRate = binary.LittleEndian.Uint32(fmtData[8:12])
			remaining := int64(chunkSize) - 16
			if remaining > 0 {
				if _, err := f.Seek(remaining, io.SeekCurrent); err != nil {
					return 0, err
				}
			}
		} else if chunkID == "data" {
			dataSize = chunkSize
			break
		} else {
			// Skip chunk and 1-byte padding if odd
			skip := int64(chunkSize)
			if skip%2 != 0 {
				skip++
			}
			if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
				break
			}
		}
	}

	if byteRate == 0 || dataSize == 0 {
		return 0, errors.New("wav: missing fmt or data chunk")
	}

	return float64(dataSize) / float64(byteRate), nil
}
