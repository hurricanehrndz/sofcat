//go:build darwin

package main

// protectAppData makes the Data directory root-only (protectRootOnlyDir).
func protectAppData(root string) error { return protectRootOnlyDir(root) }

// protectConfig makes config.yaml root-only (protectConfigFile).
func protectConfig(path string) error { return protectConfigFile(path) }
