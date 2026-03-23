package voice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Decoder converts voice data to WAV format.
type Decoder interface {
	DecodeToWAV(data []byte) ([]byte, error)
}

// ExecSILKDecoder decodes SILK audio by invoking an external decoder binary.
// The binary should accept two arguments: input file path and output file path,
// producing raw PCM output.
type ExecSILKDecoder struct {
	BinaryPath string
}

// NewExecSILKDecoder creates a new ExecSILKDecoder with the given binary path.
func NewExecSILKDecoder(binaryPath string) *ExecSILKDecoder {
	return &ExecSILKDecoder{BinaryPath: binaryPath}
}

// DecodeToWAV decodes SILK data to WAV format. It strips the leading 0x02 byte
// that WeChat prepends to SILK data, invokes the external decoder to produce
// raw PCM, then wraps the PCM in a WAV header.
func (d *ExecSILKDecoder) DecodeToWAV(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty SILK data")
	}

	// Strip leading 0x02 byte if present (WeChat prepends it).
	if data[0] == 0x02 {
		data = data[1:]
	}

	tmpDir, err := os.MkdirTemp("", "silk-decode-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck // best-effort cleanup

	tmpInput := filepath.Join(tmpDir, "input.silk")
	tmpOutput := filepath.Join(tmpDir, "output.pcm")

	if writeErr := os.WriteFile(tmpInput, data, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write temp input: %w", writeErr)
	}

	cmd := exec.Command(d.BinaryPath, tmpInput, tmpOutput) //nolint:gosec // binary path is configured by the caller, not user input
	if output, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return nil, fmt.Errorf("silk decoder: %w (output: %s)", cmdErr, string(output))
	}

	pcmData, err := os.ReadFile(tmpOutput) //nolint:gosec // path is constructed from controlled temp dir
	if err != nil {
		return nil, fmt.Errorf("read PCM output: %w", err)
	}

	return EncodeWAV(pcmData), nil
}

// NoOpDecoder returns data as-is without any decoding.
type NoOpDecoder struct{}

// DecodeToWAV returns the input data unchanged.
func (d *NoOpDecoder) DecodeToWAV(data []byte) ([]byte, error) {
	return data, nil
}
