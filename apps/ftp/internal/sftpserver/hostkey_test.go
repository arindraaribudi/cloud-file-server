package sftpserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestLoadOrGenerateHostKey_Empty(t *testing.T) {
	signer, err := LoadOrGenerateHostKey("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer")
	}
}

func TestLoadOrGenerateHostKey_InvalidBase64(t *testing.T) {
	if _, err := LoadOrGenerateHostKey("not-valid-base64!!!", nil); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestLoadOrGenerateHostKey_InvalidPEM(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("not a pem block"))
	if _, err := LoadOrGenerateHostKey(b64, nil); err == nil {
		t.Fatal("expected error for undecodable PEM")
	}
}

func TestLoadOrGenerateHostKey_ValidPEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	b64 := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(block))

	signer, err := LoadOrGenerateHostKey(b64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer")
	}
}
