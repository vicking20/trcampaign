// Package lsx is a minimal local "Origin" (LSX) responder for the Ebisu SDK
// built into NFS The Run.
//
// The handshake and message crypto are a Go port of EAappEmulater
// (https://github.com/CrazyZhang666/EAappEmulater, MIT License,
// Copyright (c) CrazyZhang666 / Misaka_Mikoto_01 / B1397KB):
// Core/LSXTcpServer.cs, Core/EaCrypto.cs, Core/CRandom.cs.
package lsx

import (
	"bytes"
	"crypto/aes"
	"encoding/hex"
	"errors"
	"strings"
)

// challengeKey is the fixed AES key used for the challenge and for "version 2"
// (older SDK) message encryption: bytes 0x00..0x0F.
var challengeKey = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	return append(append([]byte(nil), b...), bytes.Repeat([]byte{byte(n)}, n)...)
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, errors.New("bad length")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("bad padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("bad padding")
		}
	}
	return b[:len(b)-n], nil
}

// encryptHex is AES-ECB + PKCS7, returned as lowercase hex (EaCrypto.Encrypt).
func encryptHex(key, plain []byte) (string, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	p := pkcs7Pad(plain, aes.BlockSize)
	out := make([]byte, len(p))
	for i := 0; i < len(p); i += aes.BlockSize {
		c.Encrypt(out[i:i+aes.BlockSize], p[i:i+aes.BlockSize])
	}
	return hex.EncodeToString(out), nil
}

// hexBytes mirrors EaCrypto.GetByteArray: keep only hex digits.
func hexBytes(s string) ([]byte, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if strings.ContainsRune("0123456789abcdef", r) {
			b.WriteRune(r)
		}
	}
	return hex.DecodeString(b.String())
}

// decryptHex reverses encryptHex.
func decryptHex(key []byte, data string) (string, error) {
	raw, err := hexBytes(data)
	if err != nil || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", errors.New("not an LSX ciphertext")
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += aes.BlockSize {
		c.Decrypt(out[i:i+aes.BlockSize], raw[i:i+aes.BlockSize])
	}
	p, err := pkcs7Unpad(out)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

// cRandom is the MSVC-style LCG used by EaCrypto.GetLSXKey (note: 16-bit output).
type cRandom struct{ seed uint32 }

func (r *cRandom) Seed(s uint32) { r.seed = s }
func (r *cRandom) Rand() uint32 {
	r.seed = r.seed*214013 + 2531011
	return (r.seed >> 16) & 0xFFFF
}

// seededKey is EaCrypto.GetLSXKey(seed): the per-session key of newer SDKs.
func seededKey(seed uint16) []byte {
	var r cRandom
	r.Seed(7)
	r.Seed(r.Rand() + uint32(seed))
	k := make([]byte, 16)
	for i := range k {
		k[i] = byte(r.Rand())
	}
	return k
}

// checkChallengeResponse: the client must return AES(challengeKey, ourKey).
func checkChallengeResponse(response, ourKey string) bool {
	p, err := decryptHex(challengeKey, response)
	return err == nil && p == ourKey
}

// makeChallengeResponse answers the client's own key.
func makeChallengeResponse(clientKey string) string {
	s, _ := encryptHex(challengeKey, []byte(clientKey))
	return s
}

// seedFromResponse mirrors LSXTcpServer: (newResponse[0] << 8) | newResponse[1],
// i.e. the first two hex characters of our ChallengeAccepted response.
func seedFromResponse(resp string) uint16 {
	if len(resp) < 2 {
		return 0
	}
	return uint16(resp[0])<<8 | uint16(resp[1])
}
