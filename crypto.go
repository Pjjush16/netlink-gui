// crypto.go - X25519 key exchange + ChaCha20-Poly1305 encryption
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

func GenerateKeyPair() (*KeyPair, error) {
	priv := make([]byte, 32)
	rand.Read(priv)
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	return &KeyPair{PrivateKey: priv, PublicKey: pub}, nil
}

func LoadKeyPair(hexStr string) (*KeyPair, error) {
	priv, err := hex.DecodeString(hexStr)
	if err != nil || len(priv) != 32 {
		return nil, fmt.Errorf("invalid private key")
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	return &KeyPair{PrivateKey: priv, PublicKey: pub}, nil
}

type Cipher struct {
	aead interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
		NonceSize() int
		Overhead() int
	}
}

func NewCipher(secret []byte) (*Cipher, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("secret must be 32 bytes")
	}
	aead, err := chacha20poly1305.NewX(secret)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce)
	ct := c.aead.Seal(nil, nonce, plaintext, nil)
	result := make([]byte, len(nonce)+len(ct))
	copy(result, nonce)
	copy(result[len(nonce):], ct)
	return result, nil
}

func (c *Cipher) Decrypt(data []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("too short")
	}
	return c.aead.Open(nil, data[:ns], data[ns:], nil)
}
