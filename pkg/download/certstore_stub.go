//go:build !windows && !(darwin && cgo)

package download

import "errors"

// openMachineStore has no OS store to open: tailscale/certstore refuses to
// build on Linux and needs cgo on macOS. Linux is only a development host.
func openMachineStore() ([]storeIdentity, func(), error) {
	return nil, nil, errors.New("tls_client_cert_store needs the Windows certificate store or the macOS keychain (a cgo build)")
}
