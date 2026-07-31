package crypto

import (
	"bytes"
	"fmt"
	"testing"
)

func TestSymmetricRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	msg := []byte("hello steam workshop downloader crypto")
	ct, err := SymmetricEncrypt(msg, key)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := SymmetricDecrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("got %q want %q", pt, msg)
	}
}

func TestSymmetricWithKnownIV(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	iv := bytes.Repeat([]byte{2}, 16)
	msg := []byte("abc")
	ct, err := SymmetricEncryptWithIV(msg, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	// First 16 bytes are ECB(iv).
	gotIV, err := SymmetricDecryptIV(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotIV, iv) {
		t.Fatalf("iv mismatch")
	}
	pt, err := SymmetricDecrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("pt %q", pt)
	}
}

func TestHMACRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	secret := []byte("hmac-secret-16b!") // any length
	msg := []byte("channel payload")
	ct, err := SymmetricEncryptHMAC(msg, key, secret)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := SymmetricDecryptHMAC(ct, key, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("got %q", pt)
	}
	// Wrong secret must fail.
	if _, err := SymmetricDecryptHMAC(ct, key, []byte("nope")); err == nil {
		t.Fatal("expected HMAC failure")
	}
}

func TestGenerateSessionKey(t *testing.T) {
	sk, enc, err := GenerateSessionKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sk) != 32 {
		t.Fatalf("session key len %d", len(sk))
	}
	// OAEP-SHA1 ciphertext size = modulus bytes (Steam key is 768-bit → 96 bytes).
	if len(enc) < 64 {
		t.Fatalf("encrypted session key too short: %d", len(enc))
	}
}

func TestPKCS7(t *testing.T) {
	p := pkcs7Pad([]byte("hi"), 16)
	if len(p) != 16 {
		t.Fatalf("len %d", len(p))
	}
	u, err := pkcs7Unpad(p)
	if err != nil || string(u) != "hi" {
		t.Fatalf("unpad %q %v", u, err)
	}
}

func TestInteropsPythonVector(t *testing.T) {
	// Fixed vector from ValvePython steam.core.crypto.symmetric_encrypt_with_iv:
	// key=0123..def, iv=abcdefghijklmnop, msg=workshop
	// ct hex captured from Cryptodome on the same inputs.
	key := []byte("0123456789abcdef0123456789abcdef") // 32
	iv := []byte("abcdefghijklmnop")                   // 16
	msg := []byte("workshop")
	wantHex := "2caa03ffbc3f459f8427455a83340b7ff2a001127183a7dca9dee2e0e7e9ce61"
	ct, err := SymmetricEncryptWithIV(msg, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	gotHex := fmt.Sprintf("%x", ct)
	if gotHex != wantHex {
		t.Fatalf("ct mismatch\n got %s\nwant %s", gotHex, wantHex)
	}
	pt, err := SymmetricDecrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatal(pt)
	}
}
