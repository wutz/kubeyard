package webhook

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The API server reaches the webhook over TLS, so the operator has to hold a
// certificate the API server trusts. v1 issues it itself: one self-signed CA per
// installation, a serving certificate signed by it, and the CA bundle written
// into the ValidatingWebhookConfiguration. That keeps the install free of
// cert-manager; rotation is a restart of the operator with an empty cert dir.

const (
	// CertFile and KeyFile are the serving certificate names the webhook server
	// reads, as controller-runtime expects them.
	CertFile = "tls.crt"
	KeyFile  = "tls.key"
	// CAFile holds the CA certificate whose bundle the API server needs.
	CAFile = "ca.crt"

	// certValidity is how long a generated certificate is good for. The operator
	// regenerates before it expires, on the next start.
	certValidity = 365 * 24 * time.Hour
	// renewBefore is when a stored certificate is considered too old to reuse.
	renewBefore = 30 * 24 * time.Hour
)

// EnsureCert makes dir hold a serving certificate for dnsNames signed by a CA,
// and returns the CA certificate in PEM. A stored certificate that still parses,
// covers every name, and has more than renewBefore left is reused; anything else
// is replaced, CA included, so the returned bundle always matches the key the
// server will present.
func EnsureCert(dir string, dnsNames []string, now time.Time) ([]byte, error) {
	if len(dnsNames) == 0 {
		return nil, fmt.Errorf("certificate needs at least one DNS name")
	}
	if ca, ok := loadServing(dir, dnsNames, now); ok {
		return ca, nil
	}
	return issueCert(dir, dnsNames, now)
}

// loadServing returns the stored CA bundle when the stored serving certificate
// can still be used.
func loadServing(dir string, dnsNames []string, now time.Time) ([]byte, bool) {
	caPEM, err := os.ReadFile(filepath.Join(dir, CAFile))
	if err != nil {
		return nil, false
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, CertFile))
	if err != nil {
		return nil, false
	}
	if _, err := os.ReadFile(filepath.Join(dir, KeyFile)); err != nil {
		return nil, false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false
	}
	if now.Add(renewBefore).After(cert.NotAfter) {
		return nil, false
	}
	for _, name := range dnsNames {
		if err := cert.VerifyHostname(name); err != nil {
			return nil, false
		}
	}
	for _, ip := range localIPs() {
		if err := cert.VerifyHostname(ip.String()); err != nil {
			return nil, false
		}
	}
	return caPEM, true
}

// issueCert writes a fresh CA and serving certificate into dir.
func issueCert(dir string, dnsNames []string, now time.Time) ([]byte, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          serial(now),
		Subject:               pkix.Name{CommonName: "kubeyard-webhook-ca", Organization: []string{"kubeyard"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(now),
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"kubeyard"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// The loopback addresses and host.k3d.internal let a local operator
		// binary serve the same webhook to a local k3d cluster.
		DNSNames:    append(append([]string{}, dnsNames...), LocalName),
		IPAddresses: localIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	files := map[string][]byte{
		CAFile:   caPEM,
		CertFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyFile:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
	for name, data := range files {
		if err := writeFile(filepath.Join(dir, name), data); err != nil {
			return nil, err
		}
	}
	return caPEM, nil
}

// localIPs is the loopback addresses the serving certificate covers.
func localIPs() []net.IP {
	return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
}

// LocalName is how the API server of a local k3d cluster reaches a webhook that
// runs on the host, outside the cluster. k3d maps it to the host.
const LocalName = "host.k3d.internal"

// writeFile replaces a file atomically enough for a start-up write: the webhook
// server only reads these once, before it starts listening.
func writeFile(path string, data []byte) error {
	var buf bytes.Buffer
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// serial is a random positive serial number.
func serial(now time.Time) *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return big.NewInt(now.UnixNano())
	}
	return n
}
