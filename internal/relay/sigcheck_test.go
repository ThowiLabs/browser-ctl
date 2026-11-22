package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestSignFormat(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	key := &PairingKey{
		PublicKey: "unused",
		sign: func(payload string) (string, error) {
			sum := sha256.Sum256([]byte(payload))
			r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
			if err != nil {
				return "", err
			}
			buf := make([]byte, 64)
			r.FillBytes(buf[:32])
			s.FillBytes(buf[32:])
			return base64.StdEncoding.EncodeToString(buf), nil
		},
	}
	sig, err := key.sign("test-payload")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sig)
	t.Logf("sig len=%d", len(raw))
	t.Logf("sig hex=%s", hex.EncodeToString(raw))
	t.Logf("r hex=%s", hex.EncodeToString(raw[:32]))
	t.Logf("s hex=%s", hex.EncodeToString(raw[32:]))
	// WebCrypto expects exactly 64 bytes for P-256 (r||s, 32 each).
	if len(raw) != 64 {
		t.Fatalf("expected 64 bytes, got %d", len(raw))
	}
}
