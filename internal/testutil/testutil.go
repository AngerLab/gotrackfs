// Package testutil provides shared test fixtures for audio container headers.
//
// The FLAC/WAV builders used to be copy-pasted across the audio, vfs, and
// cutter test suites; keeping them here gives the test code a single source
// of truth for synthetic container fixtures.
package testutil

import (
	"bytes"
	"encoding/binary"
)

// FlacHeader returns a minimal FLAC file with a valid STREAMINFO block for the
// given sample rate, channel count, and bits per sample.
func FlacHeader(rate, chans, bps uint64) []byte {
	return FlacHeaderWithSamples(rate, chans, bps, 1000)
}

// FlacHeaderWithSamples returns a minimal FLAC file with a valid STREAMINFO
// block carrying the given total sample count (for duration assertions).
func FlacHeaderWithSamples(rate, chans, bps, totalSamples uint64) []byte {
	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.Write([]byte{0x80, 0x00, 0x00, 34}) // isLast=1, type=0, len=34
	var streaminfo [34]byte
	v := rate<<44 | (chans-1)<<41 | (bps-1)<<36 | (totalSamples & 0xFFFFFFFFF)
	binary.BigEndian.PutUint64(streaminfo[10:18], v)
	buf.Write(streaminfo[:])
	buf.Write(make([]byte, 1024))
	return buf.Bytes()
}

// WavHeader returns a RIFF/WAVE header with fmt and data chunks for the given
// parameters. The PCM payload is intentionally omitted; parsers derive the
// duration from the data chunk size.
func WavHeader(rate, chans, bps uint32, durationSec float64) []byte {
	byteRate := rate * chans * (bps / 8)
	dataSize := uint32(durationSec * float64(byteRate))
	buf := new(bytes.Buffer)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(chans))
	binary.Write(buf, binary.LittleEndian, rate)
	binary.Write(buf, binary.LittleEndian, byteRate)
	binary.Write(buf, binary.LittleEndian, uint16(chans*(bps/8)))
	binary.Write(buf, binary.LittleEndian, uint16(bps))
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, dataSize)
	return buf.Bytes()
}
