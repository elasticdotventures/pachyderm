// cert is a library for generating x509 certificates. Largely cribbed from the
// go binary src/crypto/tls/generate_cert.go (with some simplifications for its
// application in Pachyderm, and adapted to be a library)

package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"sync/atomic"
	"time"

	"github.com/pachyderm/pachyderm/src/internal/errors"
)

const rsaKeySize = 2048               // Recommended by SO (below) and generate_cert.go
const validDur = 365 * 24 * time.Hour // 1 year

var serialNumber int64

// PublicCertToPEM serializes the public x509 cert in 'cert' to a PEM-formatted
// block
func PublicCertToPEM(cert *tls.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Certificate[0],
	})
}

// KeyToPEM serializes the private key in 'cert' to a PEM-formatted block if
// it's an RSA key, or nil otherwise (all certs returned by
// GenerateSelfSignedCert use RSA keys)
func KeyToPEM(cert *tls.Certificate) []byte {
	switch k := cert.PrivateKey.(type) {
	case *rsa.PrivateKey:
		return pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(k),
		})
	default:
		return nil
	}
}

// GenerateSelfSignedCert generates a self-signed TLS cert for the domain name
// 'address', with a private key. Other attributes of the subject can be set in
// 'name' and ip addresses can be set in 'ipAddresses'
func GenerateSelfSignedCert(address string, name *pkix.Name, ipAddresses ...string) (*tls.Certificate, error) {
	// Generate Subject Distinguished Name
	if name == nil {
		name = &pkix.Name{}
	}
	switch {
	case address == "" && name.CommonName == "":
		return nil, errors.New("must set either \"address\" or \"name.CommonName\"")
	case address != "" && name.CommonName == "":
		name.CommonName = address
	case address != "" && name.CommonName != "" && name.CommonName != address:
		return nil, errors.Errorf("set address to \"%s\" but name.CommonName to \"%s\"", address, name.CommonName)
	default:
		// name.CommonName is already valid--nothing to do
	}

	// Parse IPs in ipAddresses
	parsedIPs := []net.IP{}
	for _, strIP := range ipAddresses {
		nextParsedIP := net.ParseIP(strIP)
		if nextParsedIP == nil {
			return nil, errors.Errorf("invalid IP: %s", strIP)
		}
		parsedIPs = append(parsedIPs, nextParsedIP)
	}
	// Generate key pair. According to
	// https://security.stackexchange.com/questions/5096/rsa-vs-dsa-for-ssh-authentication-keys
	// RSA is likely to be faster and more secure in practice than DSA/ECDSA, so
	// this only generates RSA keys
	key, err := rsa.GenerateKey(rand.Reader, rsaKeySize)
	if err != nil {
		return nil, errors.Wrapf(err, "could not generate RSA private key")
	}

	// Generate unsigned cert
	cert := x509.Certificate{
		// the x509 spec requires every x509 cert must have a serial number that is
		// unique for the signing CA. All of the certs generated by this package
		// are self-signed, so this just starts at 1 and counts up
		SerialNumber: big.NewInt(atomic.AddInt64(&serialNumber, 1)),
		Subject:      *name,
		NotBefore:    time.Now().Add(-1 * time.Second),
		NotAfter:     time.Now().Add(validDur),

		KeyUsage: x509.KeyUsageCertSign | // can sign certs (need for self-signing)
			x509.KeyUsageKeyEncipherment | // can encrypt other keys (need for TLS in symmetric mode)
			x509.KeyUsageKeyAgreement, // can establish keys (need for TLS in Diffie-Hellman mode)
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, // can authenticate server (for TLS)

		IsCA:                  true, // must be set b/c KeyUsageCertSign is set
		BasicConstraintsValid: true, // mark "Basic Constraints" extn critical(?)
		MaxPathLenZero:        true, // must directly sign all end entity certs
		IPAddresses:           parsedIPs,
		DNSNames:              []string{address},
	}

	// Sign 'cert' (cert is both 'template' and 'parent' b/c it's self-signed)
	signedCertDER, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &key.PublicKey, key)
	if err != nil {
		return nil, errors.Wrapf(err, "could not self-sign certificate")
	}
	signedCert, err := x509.ParseCertificate(signedCertDER)
	if err != nil {
		return nil, errors.Wrapf(err, "could not parse the just-generated signed certificate")
	}
	return &tls.Certificate{
		Certificate: [][]byte{signedCertDER},
		Leaf:        signedCert,
		PrivateKey:  key,
	}, nil
}
