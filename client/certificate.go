package client

import (
	"crypto/rand"
	"crypto/rsa"

	// #nosec
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const certServerPort = 8181

func (c *client) requestCertificate(address string) error {
	hostname, port, err := extractHostAndPort(address)
	if err != nil {
		return errors.New("invalid certificate url")
	}

	u := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(hostname, strconv.Itoa(certServerPort)),
	}

	content, err := c.tor.HTTPrequest(u.String())
	if err != nil {
		return err
	}

	cert := []byte(content)
	p, _ := strconv.Atoi(port)
	err = c.storeCertificate(hostname, p, cert)
	if err != nil {
		return err
	}

	return c.saveCertificateConfigFile()
}

func extractHostAndPort(address string) (host string, port string, err error) {
	u, err := url.Parse(address)
	if err != nil {
		return
	}

	host, port, err = net.SplitHostPort(u.Host)
	if err != nil {
		return
	}

	return host, port, nil
}

func (c *client) storeCertificate(hostname string, port int, cert []byte) error {
	if c.isTheCertificateInDB(hostname) {
		return nil
	}

	block, _ := pem.Decode(cert)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid certificate")
	}

	digest, err := digestForCertificate(block.Bytes)
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"hostname": hostname,
		"port":     port,
		"digest":   digest,
	}).Info("Storing Mumble client certificate")

	return c.storeCertificateInDB(hostname, port, digest)
}

const (
	defaultHostToReplace   = "ffaaffaabbddaabbddeeaaddccaaffeebbaabbeeddeeaaddbbeeeeff.onion"
	defaultPortToReplace   = 64738
	defaultDigestToReplace = "AAABACADAFBABBBCBDBEBFCACBCCCDCECFDADBDC"
)

func (c *client) storeCertificateInDB(id string, port int, digest string) error {
	db, err := c.db()
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"defaultHost":   defaultHostToReplace,
		"defaultPort":   defaultPortToReplace,
		"defaultDigest": defaultDigestToReplace,
		"newHost":       id,
		"newPort":       port,
		"newDigest":     digest,
	}).Debug("Replacing content in Mumble sqlite database")

	db.replaceString(defaultHostToReplace, id)
	db.replaceString(defaultDigestToReplace, digest)
	db.replaceInteger(uint16(defaultPortToReplace), uint16(port))

	return db.write()
}

func (c *client) isTheCertificateInDB(hostname string) bool {
	d, err := c.db()
	if err != nil {
		return false
	}

	return d.exists(hostname)
}

func digestForCertificate(cert []byte) (string, error) {
	// #nosec
	h := sha1.New()
	_, err := h.Write(cert)
	if err != nil {
		return "", err
	}

	bs := h.Sum(nil)

	return fmt.Sprintf("%x", bs), nil
}

// openssl req -newkey rsa:2048 -nodes -keyout key.pem -x509 -days 365 -out certificate.pem
func genCertInto(certFilename, keyFilename string) error {
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(0),
		Subject: pkix.Name{
			CommonName: "Wahay Autogenerated Certificate",
		},
		NotBefore: now.Add(-300 * time.Second),
		NotAfter:  now.Add(24 * time.Hour * 365),

		SubjectKeyId: []byte{1, 2, 3, 4},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	certbuf, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certblk := pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certbuf,
	}

	keybuf := x509.MarshalPKCS1PrivateKey(priv)
	keyblk := pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keybuf,
	}

	file, err := os.OpenFile(certFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer closeAndIgnore(file)
	err = pem.Encode(file, &certblk)
	if err != nil {
		return err
	}

	file, err = os.OpenFile(keyFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer closeAndIgnore(file)
	err = pem.Encode(file, &keyblk)
	if err != nil {
		return err
	}

	return nil
}

// generateTemporaryMumbleCertificate will generate a certificate and private key and
// then format that in PKCS12, finally formatting it in the @ByteArray format that
// Mumble configuration files use
// This will fail if OpenSSL is not installed on the system.
func generateTemporaryMumbleCertificate() (string, error) {
	dir, err := ioutil.TempDir("", "wahay_cert_generation")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	err = genCertInto(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		return "", err
	}

	args := []string{"pkcs12", "-passout", "pass:", "-inkey", filepath.Join(dir, "key.pem"),
		"-in", filepath.Join(dir, "cert.pem"), "-export", "-out", filepath.Join(dir, "transformed.p12")}
	// This executes the openssl command. The args are completely under our control
	/* #nosec G204 */
	cmd := exec.Command("openssl", args...)
	_, err = cmd.Output()
	if err != nil {
		return "", err
	}

	data, err := ioutil.ReadFile(filepath.Clean(filepath.Join(dir, "transformed.p12")))
	if err != nil {
		return "", err
	}

	return byteArrayUnparse(data), nil
}

// Implement functions that match the QByteArray used in Mumble among other things
func byteArrayIsHex(b byte) bool {
	switch b {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	case 'a', 'b', 'c', 'd', 'e', 'f':
		return true
	case 'A', 'B', 'C', 'D', 'E', 'F':
		return true
	default:
		return false
	}
}

const byteArrayPrefix = "@ByteArray("
const byteArraySuffix = ")"

func byteArrayIsDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func byteArrayIsPrintable(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if byteArrayIsDigit(b) {
		return true
	}
	switch b {
	case '*', '+', '\'', ' ', ',', ';', '`', '~', '{', '}', '(', '[', ')', ']', ':',
		'.', '$', '|', '/', '&', '^', '=', '-', '%', '<', '>', '!', '#', '_', '@', '?':
		return true
	default:
		return false
	}
}

func byteArrayIsSpecial(b byte) bool {
	switch b {
	case '\t', '\r', '\a', '\b', '\v', '\f', '\n', 0, '"', '\\':
		return true
	default:
		return false
	}
}

func byteArrayFormatSpecial(b byte) (string, bool) {
	switch b {
	case '\t':
		return "\\t", false
	case '\r':
		return "\\r", false
	case '\a':
		return "\\a", false
	case '\b':
		return "\\b", false
	case '\v':
		return "\\v", false
	case '\f':
		return "\\f", false
	case '\n':
		return "\\n", false
	case 0:
		return "\\0", true
	case '"':
		return "\\\"", false
	case '\\':
		return "\\\\", false
	default:
		return "", false
	}
}

func byteArrayFormatEscaped(b byte) (string, bool) {
	out := hex.EncodeToString([]byte{b})
	if out[0] == '0' {
		return fmt.Sprintf("\\x%s", out[1:]), true
	}
	return fmt.Sprintf("\\x%s", out), true
}

func byteArrayUnparseByte(b byte, previousHexBefore bool) (string, bool) {
	if byteArrayIsHex(b) && previousHexBefore {
		return byteArrayFormatEscaped(b)
	}
	if byteArrayIsPrintable(b) {
		return string([]byte{b}), false
	}
	if byteArrayIsSpecial(b) {
		return byteArrayFormatSpecial(b)
	}
	return byteArrayFormatEscaped(b)
}

func byteArrayUnparse(bs []byte) string {
	result := make([]string, 0, len(bs)*3)
	result = append(result, byteArrayPrefix)
	var bb string
	var hexBefore bool

	for _, b := range bs {
		bb, hexBefore = byteArrayUnparseByte(b, hexBefore)
		result = append(result, bb)
	}

	result = append(result, byteArraySuffix)

	return strings.Join(result, "")
}
