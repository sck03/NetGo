package node

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func certificatePEM(c *x509.Certificate, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer) ([]byte, []byte, error) {
	der, err := x509.CreateCertificate(rand.Reader, c, parent, key.Public(), parentKey)
	if err != nil {
		return nil, nil, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), nil
}
func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
func (m Manager) certificates(hosts map[string]string, source string, generate bool) (map[string][]byte, error) {
	if source == "" {
		if !generate {
			return nil, errors.New("证书目录不能为空")
		}
		source = filepath.Join(m.etc(), "tls")
	}
	if err := noLinks(source); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var ca *x509.Certificate
	var caKey crypto.Signer
	if !generate {
		for name := range hosts {
			for _, suffix := range []string{".crt", ".key"} {
				path := filepath.Join(source, name+suffix)
				if err := noLinks(path); err != nil {
					return nil, err
				}
				b, e := os.ReadFile(path)
				if e != nil {
					return nil, e
				}
				files[name+suffix] = b
			}
		}
		if b, e := os.ReadFile(filepath.Join(source, "ca.crt")); e == nil {
			files["ca.crt"] = b
		}
	} else {
		existingCA, ce := os.ReadFile(filepath.Join(source, "ca.crt"))
		existingKey, ke := os.ReadFile(filepath.Join(source, "ca.key"))
		if ce == nil && ke == nil {
			pair, err := tls.X509KeyPair(existingCA, existingKey)
			if err != nil {
				return nil, err
			}
			ca, err = x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				return nil, err
			}
			var ok bool
			caKey, ok = pair.PrivateKey.(crypto.Signer)
			if !ok || !ca.IsCA || time.Now().After(ca.NotAfter) {
				return nil, errors.New("invalid site CA")
			}
			files["ca.crt"], files["ca.key"] = existingCA, existingKey
		} else {
			if !os.IsNotExist(ce) || !os.IsNotExist(ke) {
				return nil, errors.New("已有 CA 证书/私钥不完整或无法读取，请修正证书目录")
			}
			if m.Role == "vps" {
				return nil, errors.New("请导入 LAN 签发的节点证书；VPS 不持有站点 CA 私钥")
			}
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return nil, err
			}
			caKey = key
			template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "NetGo site CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
			cert, keyPEM, err := certificatePEM(template, key, template, key)
			if err != nil {
				return nil, err
			}
			block, _ := pem.Decode(cert)
			ca, err = x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, err
			}
			files["ca.crt"], files["ca.key"] = cert, keyPEM
		}
		for name, host := range hosts {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return nil, err
			}
			c := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			if ip := net.ParseIP(host); ip != nil {
				c.IPAddresses = []net.IP{ip}
			} else {
				c.DNSNames = []string{host}
			}
			cert, private, err := certificatePEM(c, key, ca, caKey)
			if err != nil {
				return nil, err
			}
			files[name+".crt"], files[name+".key"] = cert, private
		}
	}
	if err := validateCertificates(hosts, files); err != nil {
		return nil, err
	}
	roots, _ := os.ReadFile(m.path("/etc/ssl/certs/ca-certificates.crt"))
	files["trust.pem"] = append(append(roots, '\n'), files["ca.crt"]...)
	return files, nil
}
func validateCertificates(hosts map[string]string, files map[string][]byte) error {
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if ca := files["ca.crt"]; len(ca) > 0 && !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid CA certificate")
	}
	for name, host := range hosts {
		pair, err := tls.X509KeyPair(files[name+".crt"], files[name+".key"])
		if err != nil {
			return err
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
		if time.Until(cert.NotAfter) < 24*time.Hour {
			return errors.New("certificate expires within one day")
		}
		intermediates := x509.NewCertPool()
		for _, der := range pair.Certificate[1:] {
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return err
			}
			intermediates.AddCert(c)
		}
		if _, err = cert.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return err
		}
	}
	return nil
}
