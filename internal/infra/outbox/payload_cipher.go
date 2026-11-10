package outbox

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

type PayloadCipher struct {
	aead cipher.AEAD
}

func NewPayloadCipher(key []byte) (*PayloadCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("outbox encryption key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &PayloadCipher{aead: aead}, nil
}

func (c *PayloadCipher) Seal(payload, identity []byte) []byte {
	return c.aead.Seal(nil, nil, payload, identity)
}

func (c *PayloadCipher) Open(payload, identity []byte) ([]byte, error) {
	return c.aead.Open(nil, nil, payload, identity)
}
