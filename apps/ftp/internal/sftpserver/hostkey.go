package sftpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/ssh"
)

// LoadOrGenerateHostKey builds the SFTP server's SSH identity from a
// base64-encoded PEM private key (the SFTP_HOST_KEY env var). If b64PEM is
// empty, an ephemeral ed25519 key is generated instead — only its
// fingerprint is logged (never key material), since it changes on every
// restart and operators should notice.
func LoadOrGenerateHostKey(b64PEM string, log *slog.Logger) (ssh.Signer, error) {
	if b64PEM == "" {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("sftpserver: generate host key: %w", err)
		}
		signer, err := ssh.NewSignerFromSigner(priv)
		if err != nil {
			return nil, fmt.Errorf("sftpserver: wrap generated host key: %w", err)
		}
		if log != nil {
			log.Warn("sftp: SFTP_HOST_KEY not set, using an ephemeral host key (fingerprint changes on every restart)",
				"fingerprint", ssh.FingerprintSHA256(signer.PublicKey()))
		}
		return signer, nil
	}
	pemBytes, err := base64.StdEncoding.DecodeString(b64PEM)
	if err != nil {
		return nil, fmt.Errorf("sftpserver: SFTP_HOST_KEY: invalid base64: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("sftpserver: SFTP_HOST_KEY: parse: %w", err)
	}
	return signer, nil
}
