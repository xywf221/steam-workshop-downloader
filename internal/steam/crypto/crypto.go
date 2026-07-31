// Package crypto implements Steam wire crypto used by CM channel encrypt and CDN chunks.
//
// Behaviour matches ValvePython steam.core.crypto (AES-ECB IV + AES-CBC body,
// PKCS7 pad, RSA-OAEP-SHA1 session key with the Public universe key).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
)

const blockSize = 16

// universePublicKeyDER is the Steam Public universe RSA key (PKCS#1 / SubjectPublicKeyInfo
// as shipped in ValvePython steam.core.crypto.UniverseKey.Public).
const universePublicKeyB64 = `
MIGdMA0GCSqGSIb3DQEBAQUAA4GLADCBhwKBgQDf7BrWLBBmLBc1OhSwfFkRf53T
2Ct64+AVzRkeRuh7h3SiGEYxqQMUeYKO6UWiSRKpI2hzic9pobFhRr3Bvr/WARvY
gdTckPv+T1JzZsuVcNfFjrocejN1oWI0Rrtgt4Bo+hOneoo3S57G9F1fOpn5nsQ6
6WOiu4gZKODnFMBCiQIBEQ==
`

var universeKey *rsa.PublicKey

func init() {
	der, err := base64.StdEncoding.DecodeString(stripSpace(universePublicKeyB64))
	if err != nil {
		panic("steam crypto: universe key b64: " + err.Error())
	}
	// The blob is PKCS#1 RSAPublicKey (MIGd... from Cryptodome import_key).
	pub, err := parseUniversePublicKey(der)
	if err != nil {
		panic("steam crypto: universe key parse: " + err.Error())
	}
	universeKey = pub
}

func stripSpace(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			out = append(out, c)
		}
	}
	return string(out)
}

// GenerateSessionKey returns (sessionKey 32B, encryptedSessionKey for ChannelEncryptResponse).
// Optional hmacSecret is appended to the plaintext before RSA-OAEP-SHA1 (Steam HMAC mode).
func GenerateSessionKey(hmacSecret []byte) (sessionKey, encrypted []byte, err error) {
	sessionKey = make([]byte, 32)
	if _, err = rand.Read(sessionKey); err != nil {
		return nil, nil, err
	}
	plain := append([]byte{}, sessionKey...)
	plain = append(plain, hmacSecret...)
	encrypted, err = rsa.EncryptOAEP(sha1.New(), rand.Reader, universeKey, plain, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("rsa oaep: %w", err)
	}
	return sessionKey, encrypted, nil
}

// SymmetricEncrypt encrypts message with a random IV (ECB-encrypted IV || CBC body).
func SymmetricEncrypt(message, key []byte) ([]byte, error) {
	iv := make([]byte, blockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return SymmetricEncryptWithIV(message, key, iv)
}

// SymmetricEncryptWithIV encrypts with a provided 16-byte IV.
//
// Wire layout (matches ValvePython): AES-ECB(iv) || AES-CBC(pkcs7(message), iv).
// The IV itself is encrypted as a single raw ECB block — no PKCS7 on the IV.
func SymmetricEncryptWithIV(message, key, iv []byte) ([]byte, error) {
	if len(iv) != blockSize {
		return nil, fmt.Errorf("iv must be %d bytes", blockSize)
	}
	encIV, err := encryptECBBlock(iv, key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(message, blockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append(encIV, out...), nil
}

// SymmetricEncryptECB encrypts with AES-ECB + PKCS7 (generic helper).
func SymmetricEncryptECB(message, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(message, blockSize)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += blockSize {
		block.Encrypt(out[i:i+blockSize], padded[i:i+blockSize])
	}
	return out, nil
}

// encryptECBBlock encrypts exactly one AES block with no padding (Steam IV path).
func encryptECBBlock(block16, key []byte) ([]byte, error) {
	if len(block16) != blockSize {
		return nil, fmt.Errorf("ecb block must be %d bytes", blockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, blockSize)
	block.Encrypt(out, block16)
	return out, nil
}

// decryptECBBlock decrypts exactly one AES block with no padding.
func decryptECBBlock(block16, key []byte) ([]byte, error) {
	if len(block16) != blockSize {
		return nil, fmt.Errorf("ecb block must be %d bytes", blockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, blockSize)
	block.Decrypt(out, block16)
	return out, nil
}

// SymmetricDecrypt decrypts ECB-IV || CBC ciphertext (CDN chunks, channel).
func SymmetricDecrypt(ciphertext, key []byte) ([]byte, error) {
	iv, err := SymmetricDecryptIV(ciphertext, key)
	if err != nil {
		return nil, err
	}
	return SymmetricDecryptWithIV(ciphertext, key, iv)
}

// SymmetricDecryptIV decrypts the first 16 bytes as raw AES-ECB to recover the IV.
func SymmetricDecryptIV(ciphertext, key []byte) ([]byte, error) {
	if len(ciphertext) < blockSize {
		return nil, fmt.Errorf("ciphertext too short for IV")
	}
	return decryptECBBlock(ciphertext[:blockSize], key)
}

// SymmetricDecryptECB decrypts AES-ECB and strips PKCS7.
func SymmetricDecryptECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%blockSize != 0 {
		return nil, fmt.Errorf("ecb ciphertext length %d", len(ciphertext))
	}
	out := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += blockSize {
		block.Decrypt(out[i:i+blockSize], ciphertext[i:i+blockSize])
	}
	return pkcs7Unpad(out)
}

// SymmetricDecryptWithIV decrypts the body after the IV using AES-CBC.
func SymmetricDecryptWithIV(ciphertext, key, iv []byte) ([]byte, error) {
	if len(ciphertext) < blockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	body := ciphertext[blockSize:]
	if len(body) == 0 || len(body)%blockSize != 0 {
		return nil, fmt.Errorf("cbc body length %d", len(body))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, body)
	return pkcs7Unpad(out)
}

// SymmetricEncryptHMAC is the HMAC-IV variant used on some Steam channel messages.
func SymmetricEncryptHMAC(message, key, hmacSecret []byte) ([]byte, error) {
	prefix := make([]byte, 3)
	if _, err := rand.Read(prefix); err != nil {
		return nil, err
	}
	mac := HMACSHA1(hmacSecret, append(prefix, message...))
	iv := append(mac[:13], prefix...)
	return SymmetricEncryptWithIV(message, key, iv)
}

// SymmetricDecryptHMAC decrypts and verifies the HMAC-IV variant.
func SymmetricDecryptHMAC(ciphertext, key, hmacSecret []byte) ([]byte, error) {
	iv, err := SymmetricDecryptIV(ciphertext, key)
	if err != nil {
		return nil, err
	}
	message, err := SymmetricDecryptWithIV(ciphertext, key, iv)
	if err != nil {
		return nil, err
	}
	mac := HMACSHA1(hmacSecret, append(iv[13:], message...))
	if !hmac.Equal(iv[:13], mac[:13]) {
		return nil, fmt.Errorf("HMAC does not match")
	}
	return message, nil
}

// HMACSHA1 returns HMAC-SHA1(secret, data).
func HMACSHA1(secret, data []byte) []byte {
	m := hmac.New(sha1.New, secret)
	m.Write(data)
	return m.Sum(nil)
}

// SHA1Hash returns SHA-1(data).
func SHA1Hash(data []byte) []byte {
	s := sha1.Sum(data)
	return s[:]
}

func pkcs7Pad(data []byte, bs int) []byte {
	pad := bs - len(data)%bs
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty pkcs7")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > len(data) || pad > blockSize {
		return nil, fmt.Errorf("invalid pkcs7 pad %d", pad)
	}
	for i := 0; i < pad; i++ {
		if data[len(data)-1-i] != byte(pad) {
			return nil, fmt.Errorf("invalid pkcs7 padding bytes")
		}
	}
	return data[:len(data)-pad], nil
}
