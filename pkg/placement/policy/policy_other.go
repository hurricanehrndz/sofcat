//go:build !windows && !darwin

package policy

// readPolicyRoots has no policy store off Windows and macOS.
func readPolicyRoots() (machine, user []string) { return nil, nil }
