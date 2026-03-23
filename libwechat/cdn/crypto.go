package cdn

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// ParseAESKey decodes a base64-encoded AES key, handling two formats:
//
//	Format 1: base64(raw 16 bytes) — commonly used for images
//	Format 2: base64(hex string of 32 chars) — commonly used for files/voice/video
func ParseAESKey(b64Key string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("base64 decode AES key: %w", err)
	}

	if hexDecoded, err := hex.DecodeString(string(raw)); err == nil && len(hexDecoded) == 16 {
		return hexDecoded, nil
	}

	if len(raw) == 16 {
		return raw, nil
	}

	return nil, errors.New("invalid AES key: decoded key is not 16 bytes")
}

// AESECBEncrypt encrypts plaintext with AES-128-ECB and PKCS7 padding.
func AESECBEncrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))

	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(ciphertext[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}

	return ciphertext, nil
}

// AESECBDecrypt decrypts AES-128-ECB ciphertext and removes PKCS7 padding.
func AESECBDecrypt(ciphertext, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("empty ciphertext")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of block size %d", len(ciphertext), aes.BlockSize)
	}

	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}

	return pkcs7Unpad(plaintext)
}

// AESECBPaddedSize returns the ciphertext size after PKCS7 padding for AES-128-ECB.
func AESECBPaddedSize(plaintextSize int) int {
	padding := aes.BlockSize - (plaintextSize % aes.BlockSize)
	return plaintextSize + padding
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot unpad empty data")
	}

	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid PKCS7 padding: %d", padLen)
	}

	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return nil, fmt.Errorf("invalid PKCS7 padding at byte %d", i)
		}
	}

	return data[:len(data)-padLen], nil
}
