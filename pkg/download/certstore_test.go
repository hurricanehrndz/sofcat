package download

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

type fakeIdentity struct {
	leaf   *x509.Certificate
	key    crypto.Signer
	closed bool
}

func (f *fakeIdentity) Certificate() (*x509.Certificate, error) { return f.leaf, nil }
func (f *fakeIdentity) CertificateChain() ([]*x509.Certificate, error) {
	return []*x509.Certificate{f.leaf}, nil
}
func (f *fakeIdentity) Signer() (crypto.Signer, error) { return f.key, nil }
func (f *fakeIdentity) Close()                         { f.closed = true }

func newFakeIdentity(t *testing.T, cn string, notBefore, notAfter time.Time) *fakeIdentity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(notAfter.Unix()),
		Subject:      pkix.Name{CommonName: cn},
		Issuer:       pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeIdentity{leaf: leaf, key: key}
}

// fakeStore makes openStoreIdentities return ids and counts the opens.
func fakeStore(t *testing.T, ids ...*fakeIdentity) *int {
	t.Helper()
	opens := 0
	orig, origCfg := openStoreIdentities, downloadCfg
	t.Cleanup(func() {
		openStoreIdentities, downloadCfg = orig, origCfg
		storeCache.reset("", "")
	})
	openStoreIdentities = func() ([]storeIdentity, func(), error) {
		opens++
		out := make([]storeIdentity, len(ids))
		for i, id := range ids {
			id.closed = false
			out[i] = id
		}
		return out, func() {}, nil
	}
	return &opens
}

// After a renewal the store holds the old and the new certificate; the
// newest valid match must win, and expired or not-yet-valid ones must not.
func TestLoadStoreCertPicksNewestValidMatch(t *testing.T) {
	now := time.Now()
	expired := newFakeIdentity(t, "device-1.corp", now.Add(-48*time.Hour), now.Add(-time.Hour))
	old := newFakeIdentity(t, "device-1.corp", now.Add(-24*time.Hour), now.Add(24*time.Hour))
	renewed := newFakeIdentity(t, "device-1.corp", now.Add(-time.Hour), now.Add(72*time.Hour))
	future := newFakeIdentity(t, "device-1.corp", now.Add(time.Hour), now.Add(96*time.Hour))
	other := newFakeIdentity(t, "printer.corp", now.Add(-time.Hour), now.Add(100*time.Hour))
	fakeStore(t, expired, old, renewed, future, other)

	got, err := loadStoreCert(`^device-\d+\.corp$`, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.cert.Leaf != renewed.leaf {
		t.Fatalf("picked %v, want the renewed certificate", got.cert.Leaf.NotAfter)
	}
	for _, id := range []*fakeIdentity{expired, old, future, other} {
		if !id.closed {
			t.Errorf("unselected identity %v left open", id.leaf.NotAfter)
		}
	}
	if renewed.closed {
		t.Error("selected identity closed")
	}
}

func TestLoadStoreCertErrors(t *testing.T) {
	now := time.Now()
	fakeStore(t, newFakeIdentity(t, "device-1.corp", now.Add(-time.Hour), now.Add(time.Hour)))
	for _, tc := range []struct{ pattern, field, want string }{
		{"^nomatch$", "", "no valid certificate"},
		{"(", "", "tls_client_cert_store"},
		{"device", "thumbprint", "tls_client_cert_store_field"},
	} {
		if _, err := loadStoreCert(tc.pattern, tc.field, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("loadStoreCert(%q, %q) = %v, want an error containing %q", tc.pattern, tc.field, err, tc.want)
		}
	}
}

// The store is read once per run: handshakes in a run reuse the identity,
// and the next SetConfig makes the next handshake read the store again, so a
// renewed certificate needs no restart.
func TestStoreCacheReadsOncePerRun(t *testing.T) {
	now := time.Now()
	id := newFakeIdentity(t, "device-1.corp", now.Add(-time.Hour), now.Add(time.Hour))
	opens := fakeStore(t, id)
	cfg := config.Configuration{TLSClientCertStore: "device"}

	SetConfig(cfg)
	for range 3 {
		if _, err := storeCache.get(); err != nil {
			t.Fatal(err)
		}
	}
	if *opens != 1 {
		t.Fatalf("store opened %d times in one run, want 1", *opens)
	}
	SetConfig(cfg)
	if _, err := storeCache.get(); err != nil {
		t.Fatal(err)
	}
	if *opens != 2 {
		t.Fatalf("store opened %d times over two runs, want 2", *opens)
	}
}

// A reset keeps the previous identity open for a handshake still using it.
func TestStoreCacheKeepsPreviousOpen(t *testing.T) {
	now := time.Now()
	id := newFakeIdentity(t, "device-1.corp", now.Add(-time.Hour), now.Add(time.Hour))
	fakeStore(t, id)

	storeCache.reset("device", "")
	if _, err := storeCache.get(); err != nil {
		t.Fatal(err)
	}
	first := storeCache.current
	storeCache.reset("device", "")
	if id.closed {
		t.Fatal("identity closed by the first reset")
	}
	if storeCache.previous != first {
		t.Fatal("first identity not kept as previous")
	}
	storeCache.reset("device", "")
	if !id.closed {
		t.Fatal("identity still open two resets later")
	}
}

// A download presents the store's certificate to a server that requires it.
func TestFileTLSCertStore(t *testing.T) {
	now := time.Now()
	id := newFakeIdentity(t, "device-1.corp", now.Add(-time.Hour), now.Add(time.Hour))
	fakeStore(t, id)

	serverCert, err := tls.LoadX509KeyPair("testdata/server.pem", "testdata/server.key")
	if err != nil {
		t.Fatal(err)
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(id.leaf)
	ts := httptest.NewUnstartedServer(router())
	ts.TLS = &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	ts.StartTLS()
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	SetConfig(config.Configuration{
		TLSAuth:            true,
		TLSClientCertStore: `^device-1\.corp$`,
		TLSServerCert:      "testdata/server.pem",
	})
	if err := fileContext(context.Background(), t.TempDir(), "https://localhost:"+u.Port()+"/tlsauth", nil); err != nil {
		t.Fatalf("download with a store certificate: %v", err)
	}
}

// Without tls_server_cert the system roots verify the server, so tls_auth
// works against a publicly trusted repository. The test server is not
// publicly trusted, so the download must fail on its certificate rather than
// on reading a missing tls_server_cert file.
func TestFileTLSWithoutServerCertUsesSystemRoots(t *testing.T) {
	ts := httptest.NewTLSServer(router())
	defer ts.Close()

	orig := downloadCfg
	t.Cleanup(func() { downloadCfg = orig })
	SetConfig(config.Configuration{TLSAuth: true, TLSClientCert: "testdata/client.pem", TLSClientKey: "testdata/client.key"})
	err := fileContext(context.Background(), t.TempDir(), ts.URL+"/tlsauth", nil)
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("download error = %v, want the server's certificate rejected by the system roots", err)
	}
}

// A launchd daemon cannot answer the keychain's access prompt, and macOS
// reports that only as -25308; the error must say how to deploy the key.
func TestSignHint(t *testing.T) {
	err := signHint(errors.New("CFError -25308"))
	if err == nil || !strings.Contains(err.Error(), "AllowAllAppsAccess") {
		t.Fatalf("signHint = %v, want the AllowAllAppsAccess hint", err)
	}
	if signHint(nil) != nil {
		t.Fatal("signHint(nil) != nil")
	}
	other := errors.New("CFError -1")
	if !errors.Is(signHint(other), other) || strings.Contains(signHint(other).Error(), "AllowAllAppsAccess") {
		t.Fatal("hint added to an unrelated error")
	}
}
