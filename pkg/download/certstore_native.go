//go:build windows || (darwin && cgo)

package download

import (
	"crypto"
	"io"

	"github.com/tailscale/certstore"
)

// openMachineStore opens the machine store read-only: LocalMachine on
// Windows, the System keychain on macOS. The service runs as SYSTEM or root,
// so a user's store is never the one wanted.
func openMachineStore() ([]storeIdentity, func(), error) {
	store, err := certstore.Open(certstore.System, certstore.ReadOnly)
	if err != nil {
		return nil, nil, err
	}
	ids, err := store.Identities()
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	out := make([]storeIdentity, len(ids))
	for i, id := range ids {
		out[i] = hintIdentity{id}
	}
	return out, store.Close, nil
}

// hintIdentity explains a signing failure the store reports only as a code.
type hintIdentity struct{ certstore.Identity }

func (h hintIdentity) Signer() (crypto.Signer, error) {
	s, err := h.Identity.Signer()
	if err != nil {
		return nil, err
	}
	return hintSigner{s}, nil
}

type hintSigner struct{ crypto.Signer }

func (h hintSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	sig, err := h.Signer.Sign(rand, digest, opts)
	return sig, signHint(err)
}
