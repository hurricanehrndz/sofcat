package download

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

// A package level copy of our config for the `download` package to reference
var downloadCfg config.Configuration

// SetConfig accepts a configuration struct that all functions in the `download` package will use
func SetConfig(cfg config.Configuration) {
	downloadCfg = cfg
	storeCache.reset(cfg.TLSClientCertStore, cfg.TLSClientCertStoreField)
}

func fileContext(ctx context.Context, file string, url string, progress Progress) error {
	// Get the absolute file path
	_, fileName := path.Split(url)
	absPath := filepath.Join(file, fileName)

	// Create the directory
	err := os.MkdirAll(filepath.Clean(file), 0o755)
	if err != nil {
		slog.Warn("Unable to make filepath", "path", file, "err", err)
	}

	// Create the file
	f, err := os.Create(filepath.Clean(absPath))
	if err != nil {
		return err
	}
	defer f.Close()

	resp, err := open(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Stream the body to the file, so an installer is never held in memory,
	// counting it when the length is known.
	var dst io.Writer = f
	if progress != nil && resp.ContentLength > 0 {
		dst = io.MultiWriter(f, &progressWriter{total: resp.ContentLength, report: progress})
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// StatusError is a download the server answered with a status other than 200.
// errors.As it to tell a missing file (404) from other failures.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s : Download status code: %d", e.URL, e.Code)
}

// Get downloads a url and returns the body
// Timeout is 10 seconds
// Will only write to disk if http status code is 2XX
func Get(url string) ([]byte, error) {
	return getContext(context.Background(), url)
}

// clientTLSConfig is the TLS config for tls_auth: the client certificate from
// the machine store (tls_client_cert_store) or from PEM files, and the server
// roots from tls_server_cert or, when it is unset, the system's.
func clientTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if downloadCfg.TLSClientCertStore != "" {
		tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return storeCache.get()
		}
	} else {
		clientCert, err := tls.LoadX509KeyPair(downloadCfg.TLSClientCert, downloadCfg.TLSClientKey)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}
	if downloadCfg.TLSServerCert != "" {
		serverCert, err := os.ReadFile(downloadCfg.TLSServerCert)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(serverCert)
		tlsConfig.RootCAs = caCertPool
	}
	return tlsConfig, nil
}

// open sends the request and returns the response of a 200; the caller
// closes its body.
func open(ctx context.Context, url string) (*http.Response, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 10 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if downloadCfg.TLSAuth {
		tlsConfig, err := clientTLSConfig()
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}

	// Register a file handler so `file://` works
	transport.RegisterProtocol("file", http.NewFileTransport(http.Dir("/"))) //nolint:gosec // G111: file:// sources are absolute paths from the admin-controlled catalog

	client := &http.Client{Transport: transport}

	// Build the request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// If we have a user and pass, configure basic auth
	if downloadCfg.AuthUser != "" && downloadCfg.AuthPass != "" {
		req.SetBasicAuth(downloadCfg.AuthUser, downloadCfg.AuthPass)
	}

	// Actually send the request, using the client we setup
	// Storing the response in resp
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Check that the request was successful
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, &StatusError{URL: url, Code: resp.StatusCode}
	}
	return resp, nil
}

func getContext(ctx context.Context, url string) ([]byte, error) {
	resp, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Progress receives a download's whole percentage each time it changes. It
// is called only when the server sent the body's length.
type Progress func(percent int)

// progressWriter counts the bytes written through it against total and
// reports each new whole percentage.
type progressWriter struct {
	total, done int64
	last        int
	report      Progress
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	if percent := int(min(w.done*100/w.total, 100)); percent != w.last {
		w.last = percent
		w.report(percent)
	}
	return len(p), nil
}

// Verify compares a provided hash to the actual hash of a file
func Verify(file string, sha string) bool {
	f, err := os.Open(file)
	if err != nil {
		slog.Warn("Unable to open file", "err", err)
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		slog.Warn("Unable to verify hash due to IO error", "err", err)
		return false
	}
	shaHash := hex.EncodeToString(h.Sum(nil))
	if shaHash != strings.ToLower(sha) {
		slog.Debug("File hash does not match expected value", "path", file)
		return false
	}
	return true
}

// IfNeeded takes the same values as Download plus a hash as a string
// It will check if the file already exists, by comparing the hash
// If the hash does not match, it will attempt to download the file
// Once downloaded it will attempt to verify the hash again
// Cancelling ctx aborts a download in progress, and IfNeeded returns false.
// progress, when not nil, follows a download whose length the server sent.
func IfNeeded(ctx context.Context, absFile string, url string, hash string, progress Progress) bool {
	// If the file exists, check the hash
	verified := false
	if _, err := os.Stat(absFile); err == nil {
		verified = Verify(absFile, hash)
	}

	// If hash failed, download the installer
	if !verified {
		absPath, _ := filepath.Split(absFile)
		slog.Info("Downloading", "url", url, "path", absPath)
		// Download the installer
		err := fileContext(ctx, absPath, url, progress)
		if err != nil {
			slog.Warn("Unable to retrieve package", "url", url, "err", err)
			return verified
		}
		verified = Verify(absFile, hash)
	}

	// return the status of verified
	return verified
}
