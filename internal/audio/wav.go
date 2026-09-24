package audio

import (
	"encoding/binary"
	"errors"
	"io"
)

type wavHeaderInfo struct {
	Format   Format
	ByteRate uint32
	DataSize uint32
	HasFmt   bool
}

// probeWAVInfo reads RIFF WAVE header fmt and data chunks from r, returning container info.
func probeWAVInfo(r io.ReadSeeker) (wavHeaderInfo, error) {
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return wavHeaderInfo{}, err
	}

	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return wavHeaderInfo{}, errors.New("wav: not a valid RIFF WAVE file")
	}

	var info wavHeaderInfo

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(r, chunkHeader[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return info, err
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		if chunkID == "fmt " && chunkSize >= 16 {
			var fmtData [16]byte
			if _, err := io.ReadFull(r, fmtData[:]); err != nil {
				return info, err
			}
			info.Format.Channels = int(binary.LittleEndian.Uint16(fmtData[2:4]))
			info.Format.SampleRate = int(binary.LittleEndian.Uint32(fmtData[4:8]))
			info.ByteRate = binary.LittleEndian.Uint32(fmtData[8:12])
			info.Format.Bits = int(binary.LittleEndian.Uint16(fmtData[14:16]))
			info.HasFmt = true
			remaining := int64(chunkSize) - 16
			if chunkSize%2 != 0 {
				remaining++
			}
			if remaining > 0 {
				if _, err := r.Seek(remaining, io.SeekCurrent); err != nil {
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
			if _, err := r.Seek(skip, io.SeekCurrent); err != nil {
				break
			}
		}
	}

	return info, nil
}
