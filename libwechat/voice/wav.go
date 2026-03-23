package voice

import "encoding/binary"

// EncodeWAV wraps raw PCM data in a standard RIFF/WAVE header.
// Parameters: 24000 Hz sample rate, 1 channel (mono), 16 bits per sample,
// signed little-endian PCM. The WAV header is 44 bytes.
func EncodeWAV(pcmData []byte) []byte {
	const (
		sampleRate    = 24000
		numChannels   = 1
		bitsPerSample = 16
		byteRate      = sampleRate * numChannels * bitsPerSample / 8
		blockAlign    = numChannels * bitsPerSample / 8
		headerSize    = 44
	)

	dataSize := len(pcmData)
	fileSize := headerSize + dataSize - 8 // RIFF chunk size excludes "RIFF" and size field

	buf := make([]byte, headerSize+dataSize)

	// RIFF header
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(fileSize)) //nolint:gosec // WAV sizes are bounded by PCM input
	copy(buf[8:12], "WAVE")

	// fmt sub-chunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // Sub-chunk size (PCM = 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // Audio format (PCM = 1)
	binary.LittleEndian.PutUint16(buf[22:24], numChannels)
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], byteRate)
	binary.LittleEndian.PutUint16(buf[32:34], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)

	// data sub-chunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize)) //nolint:gosec // WAV sizes are bounded by PCM input
	copy(buf[headerSize:], pcmData)

	return buf
}
