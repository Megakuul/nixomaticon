package phone

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
)

func accountTLS(path string) (*tls.Config, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pem, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(pem) > 1<<20 {
		return nil, errors.New("CA certificate bundle exceeds 1 MiB")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("CA file contains no PEM certificates")
	}
	return &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
}
