package crypto

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// parseUniversePublicKey parses the Steam Public universe RSA key blob.
// ValvePython ships it as SubjectPublicKeyInfo (PKIX); we also accept bare PKCS#1.
func parseUniversePublicKey(der []byte) (*rsa.PublicKey, error) {
	if pub, err := x509.ParsePKIXPublicKey(der); err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("not RSA public key")
		}
		return rsaPub, nil
	}
	// Fallback: PKCS#1 RSAPublicKey.
	var pk struct {
		N *big.Int
		E int
	}
	if _, err := asn1.Unmarshal(der, &pk); err != nil {
		return nil, fmt.Errorf("pkix/pkcs1 parse failed: %w", err)
	}
	if pk.N == nil || pk.E == 0 {
		return nil, fmt.Errorf("missing N or E")
	}
	return &rsa.PublicKey{N: pk.N, E: pk.E}, nil
}
