//go:build !windows && !darwin

package main

// protectAppData is a no-op off Windows, where SofCat only runs for
// development.
func protectAppData(string) error { return nil }

// protectConfig is a no-op too.
func protectConfig(string) error { return nil }
