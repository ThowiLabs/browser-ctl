package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// Identity is the pairing key pair we sign the handshake with. It is created on first
// start and kept across restarts, so the extension's Allow survives them.
type Identity struct {
	PublicKey  string `json:"publicKey"`  // base64 SPKI (DER)
	PrivateKey string `json:"privateKey"` // PKCS#8 PEM
}

// PairingKey signs payloads for the handshake.
type PairingKey struct {
	PublicKey string
	sign      func(payload string) (string, error)
}

// LoadIdentity reads the identity from <home>/identity.json, creating it if absent.
func LoadIdentity(home string) (*PairingKey, error) {
	file := filepath.Join(home, "identity.json")
	if data, err := os.ReadFile(file); err == nil {
		var id Identity
		if err := json.Unmarshal(data, &id); err == nil {
			key, err := id.pairingKey()
			if err == nil {
				return key, nil
			}
		}
	}
	id, err := generateIdentity()
	if err != nil {
		return nil, err
	}
	if err := writeIdentity(file, id); err != nil {
		return nil, err
	}
	return id.pairingKey()
}

func generateIdentity() (*Identity, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return &Identity{
		PublicKey:  base64.StdEncoding.EncodeToString(pubDER),
		PrivateKey: pemEncode("PRIVATE KEY", privDER),
	}, nil
}

func (id *Identity) pairingKey() (*PairingKey, error) {
	key, err := parsePrivateKey(id.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &PairingKey{
		PublicKey: id.PublicKey,
		sign: func(payload string) (string, error) {
			sum := sha256.Sum256([]byte(payload))
			r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
			if err != nil {
				return "", err
			}
			// IEEE P1363: r || s, each 32 bytes for P-256.
			buf := make([]byte, 64)
			r.FillBytes(buf[:32])
			s.FillBytes(buf[32:])
			return base64.StdEncoding.EncodeToString(buf), nil
		},
	}, nil
}

func parsePrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("invalid private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not ECDSA")
	}
	return key, nil
}

func writeIdentity(file string, id *Identity) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", file, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func pemEncode(label string, der []byte) string {
	body := base64.StdEncoding.EncodeToString(der)
	var out []string
	out = append(out, "-----BEGIN "+label+"-----")
	for len(body) > 0 {
		n := 64
		if n > len(body) {
			n = len(body)
		}
		out = append(out, body[:n])
		body = body[n:]
	}
	out = append(out, "-----END "+label+"-----")
	return joinLines(out)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
