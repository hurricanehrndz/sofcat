package download

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// storeIdentity is the part of a certstore.Identity sofcat uses: a
// certificate whose private key stays in the OS store (Windows LocalMachine,
// the macOS System keychain), possibly non-exportable or TPM-backed.
type storeIdentity interface {
	Certificate() (*x509.Certificate, error)
	CertificateChain() ([]*x509.Certificate, error)
	Signer() (crypto.Signer, error)
	Close()
}

// openStoreIdentities lists the machine store's identities and returns the
// store's close function. It is a variable so tests supply a fake store.
var openStoreIdentities = openMachineStore

// storeFields are the certificate fields tls_client_cert_store_field selects.
var storeFields = map[string]func(*x509.Certificate) []string{
	"subject":   func(c *x509.Certificate) []string { return []string{c.Subject.CommonName} },
	"issuer":    func(c *x509.Certificate) []string { return []string{c.Issuer.CommonName} },
	"serial":    func(c *x509.Certificate) []string { return []string{c.SerialNumber.String()} },
	"dns_names": func(c *x509.Certificate) []string { return c.DNSNames },
}

// storeCert is one identity loaded from the store, held open while handshakes
// may still sign with it.
type storeCert struct {
	cert       tls.Certificate
	identity   storeIdentity
	closeStore func()
}

func (s *storeCert) close() {
	s.identity.Close()
	s.closeStore()
}

// clientCertCache holds the identity the current run presents. SetConfig
// resets it, so each run (and each service request that seeds the config)
// reads the store once, on its first handshake, and a renewed certificate is
// picked up by the next run without a restart.
//
// A reset may race a handshake that already holds the current identity, so
// the previous one stays open until the next reset and only the one before it
// is closed. That bounds the open handles at two; a handshake outliving two
// resets would fail and be retried by the next run.
type clientCertCache struct {
	mu       sync.Mutex
	pattern  string
	field    string
	current  *storeCert
	previous *storeCert
}

var storeCache clientCertCache

func (c *clientCertCache) reset(pattern, field string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.previous != nil {
		c.previous.close()
	}
	c.previous, c.current = c.current, nil
	c.pattern, c.field = pattern, field
}

// get returns the cached identity's certificate, loading it on first use.
func (c *clientCertCache) get() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		loaded, err := loadStoreCert(c.pattern, c.field, time.Now())
		if err != nil {
			return nil, err
		}
		c.current = loaded
	}
	cert := c.current.cert
	return &cert, nil
}

// validateStoreSelector checks tls_client_cert_store and its field.
func validateStoreSelector(pattern, field string) (*regexp.Regexp, func(*x509.Certificate) []string, error) {
	if field == "" {
		field = "subject"
	}
	values, ok := storeFields[field]
	if !ok {
		return nil, nil, fmt.Errorf("tls_client_cert_store_field %q: want subject, issuer, serial or dns_names", field)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("tls_client_cert_store: %w", err)
	}
	return re, values, nil
}

// loadStoreCert opens the machine store and picks, among the identities whose
// field matches pattern, the one valid at now with the latest NotAfter: after
// a renewal the old and new certificates often sit in the store together.
func loadStoreCert(pattern, field string, now time.Time) (*storeCert, error) {
	re, values, err := validateStoreSelector(pattern, field)
	if err != nil {
		return nil, err
	}
	identities, closeStore, err := openStoreIdentities()
	if err != nil {
		return nil, fmt.Errorf("open certificate store: %w", err)
	}
	var best storeIdentity
	var bestLeaf *x509.Certificate
	for _, id := range identities {
		leaf, leafErr := id.Certificate()
		if leafErr != nil || now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) ||
			!slices.ContainsFunc(values(leaf), re.MatchString) ||
			(bestLeaf != nil && !leaf.NotAfter.After(bestLeaf.NotAfter)) {
			id.Close()
			continue
		}
		if best != nil {
			best.Close()
		}
		best, bestLeaf = id, leaf
	}
	if best == nil {
		closeStore()
		return nil, fmt.Errorf("no valid certificate in the machine store with %s matching %q", fieldOrDefault(field), pattern)
	}
	cert, err := storeTLSCertificate(best, bestLeaf)
	if err != nil {
		best.Close()
		closeStore()
		return nil, err
	}
	return &storeCert{cert: cert, identity: best, closeStore: closeStore}, nil
}

func fieldOrDefault(field string) string {
	if field == "" {
		return "subject"
	}
	return field
}

func storeTLSCertificate(id storeIdentity, leaf *x509.Certificate) (tls.Certificate, error) {
	chain, err := id.CertificateChain()
	if err != nil || len(chain) == 0 {
		// The store could not build a chain; the server may still accept
		// the leaf alone if it holds the intermediates.
		chain = []*x509.Certificate{leaf}
	}
	signer, err := id.Signer()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("client certificate %q: %w", leaf.Subject.CommonName, err)
	}
	raw := make([][]byte, 0, len(chain))
	for _, c := range chain {
		raw = append(raw, c.Raw)
	}
	return tls.Certificate{Certificate: raw, PrivateKey: signer, Leaf: leaf}, nil
}

// signHint names the fix for a keychain key whose access control leaves out
// the daemon: macOS reports it only as errSecInteractionNotAllowed (-25308),
// since a launchd daemon cannot show the access prompt.
func signHint(err error) error {
	if err != nil && strings.Contains(err.Error(), "-25308") {
		return fmt.Errorf("%w: the key's access control does not allow this service; deploy the identity with AllowAllAppsAccess in its MDM payload, or import it with security import -A", err)
	}
	return err
}
