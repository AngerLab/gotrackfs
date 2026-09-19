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

// Format holds basic source-audio properties discovered by reading container
// headers (FLAC STREAMINFO or WAV fmt chunk) without external tools.
// Zero values mean "unknown".
type Format struct {
	SampleRate int // Hz
	Bits       int // bits per sample
	Channels   int // channel count
}

// Info holds basic source-audio metadata (duration and format) discovered
// by reading container headers without external tools.
type Info struct {
	Duration float64 // seconds (0 if unknown)
	Format   Format
}

type flacStreamInfo struct {
	Format       Format
	TotalSamples uint64
}

type wavHeaderInfo struct {
	Format   Format
	ByteRate uint32
	DataSize uint32
	HasFmt   bool
}

// Probe reads container headers (FLAC STREAMINFO or WAV fmt/data chunks) once,
// returning both duration and format properties in a single pass without external tools.
func Probe(filePath string) (Info, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".flac":
		flacInfo, err := probeFLACInfo(filePath)
		if err != nil {
			return Info{}, err
		}
		if flacInfo.Format.SampleRate == 0 {
			return Info{}, errors.New("flac: zero sample rate in STREAMINFO")
		}
		var dur float64
		if flacInfo.TotalSamples > 0 {
			dur = float64(flacInfo.TotalSamples) / float64(flacInfo.Format.SampleRate)
		}
		return Info{
			Duration: dur,
			Format:   flacInfo.Format,
		}, nil
	case ".wav", ".wave":
		wavInfo, err := probeWAVInfo(filePath)
		if err != nil {
			return Info{}, err
		}
		if !wavInfo.HasFmt || wavInfo.Format.SampleRate == 0 {
			return Info{}, errors.New("wav: missing or invalid fmt chunk")
		}
		var dur float64
		if wavInfo.ByteRate > 0 && wavInfo.DataSize > 0 {
			dur = float64(wavInfo.DataSize) / float64(wavInfo.ByteRate)
		}
		return Info{
			Duration: dur,
			Format:   wavInfo.Format,
		}, nil
	default:
		return Info{}, ErrNotSupported
	}
}

// probeFLACInfo reads the FLAC STREAMINFO metadata block (34 bytes) and returns container info.
func probeFLACInfo(filePath string) (flacStreamInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return flacStreamInfo{}, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return flacStreamInfo{}, err
	}
	if string(magic[:]) != "fLaC" {
		return flacStreamInfo{}, fmt.Errorf("not a flac file: %q", magic)
	}

	// Loop metadata blocks until STREAMINFO (block type 0, usually the very first block)
	for {
		var header [4]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return flacStreamInfo{}, err
		}

		isLast := (header[0] & 0x80) != 0
		blockType := header[0] & 0x7F
		length := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])

		if blockType == 0 { // STREAMINFO
			if length < 34 {
				return flacStreamInfo{}, errors.New("flac: STREAMINFO block too small")
			}
			var data [34]byte
			if _, err := io.ReadFull(f, data[:]); err != nil {
				return flacStreamInfo{}, err
			}

			// Bytes 10-17:
			// sample_rate (20 bits), channels-1 (3 bits), bps-1 (5 bits), total_samples (36 bits)
			v := binary.BigEndian.Uint64(data[10:18])
			sampleRate := int(v >> 44)
			channels := int(v>>41&0x7) + 1
			bits := int(v>>36&0x1F) + 1
			totalSamples := v & 0xFFFFFFFFF

			return flacStreamInfo{
				Format: Format{
					SampleRate: sampleRate,
					Bits:       bits,
					Channels:   channels,
				},
				TotalSamples: totalSamples,
			}, nil
		}

		if isLast {
			break
		}

		if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
			return flacStreamInfo{}, err
		}
	}

	return flacStreamInfo{}, errors.New("flac: STREAMINFO block not found")
}

// probeWAVInfo reads RIFF WAVE header fmt and data chunks, returning container info.
func probeWAVInfo(filePath string) (wavHeaderInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return wavHeaderInfo{}, err
	}
	defer f.Close()

	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return wavHeaderInfo{}, err
	}

	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return wavHeaderInfo{}, errors.New("wav: not a valid RIFF WAVE file")
	}

	var info wavHeaderInfo

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(f, chunkHeader[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return info, err
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		if chunkID == "fmt " && chunkSize >= 16 {
			var fmtData [16]byte
			if _, err := io.ReadFull(f, fmtData[:]); err != nil {
				return info, err
			}
			info.Format.Channels = int(binary.LittleEndian.Uint16(fmtData[2:4]))
			info.Format.SampleRate = int(binary.LittleEndian.Uint32(fmtData[4:8]))
			info.ByteRate = binary.LittleEndian.Uint32(fmtData[8:12])
			info.Format.Bits = int(binary.LittleEndian.Uint16(fmtData[14:16]))
			info.HasFmt = true
			remaining := int64(chunkSize) - 16
			if remaining > 0 {
				if _, err := f.Seek(remaining, io.SeekCurrent); err != nil {
					return info, err
				}
			}
		} else if chunkID == "data" {
			info.DataSize = chunkSize
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

	return info, nil
}
