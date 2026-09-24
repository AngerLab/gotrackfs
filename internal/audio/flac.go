package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type flacStreamInfo struct {
	Format       Format
	TotalSamples uint64
}

// probeFLACInfo reads the FLAC STREAMINFO metadata block (34 bytes) from r
// and returns container info.
func probeFLACInfo(r io.ReadSeeker) (flacStreamInfo, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return flacStreamInfo{}, err
	}
	if string(magic[:]) != "fLaC" {
		return flacStreamInfo{}, fmt.Errorf("not a flac file: %q", magic)
	}

	// Loop metadata blocks until STREAMINFO (block type 0, usually the very first block)
	for {
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
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
			if _, err := io.ReadFull(r, data[:]); err != nil {
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

		if _, err := r.Seek(int64(length), io.SeekCurrent); err != nil {
			return flacStreamInfo{}, err
		}
	}

	return flacStreamInfo{}, errors.New("flac: STREAMINFO block not found")
}
