package placement

import (
	"fmt"
	"strings"
)

// windowsReserved are device names Windows will not use as a file name, with
// or without an extension. The port names include digit 0 and the
// superscript digits ¹ ² ³, which Windows also treats as devices.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"COM¹": true, "COM²": true, "COM³": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"LPT¹": true, "LPT²": true, "LPT³": true,
}

// CheckSegment refuses a path segment Windows cannot hold, on every OS, so an
// archive behaves the same everywhere.
func CheckSegment(seg string) error {
	switch {
	case seg == "" || seg == "." || seg == "..":
		return fmt.Errorf("empty, . or .. segment")
	case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
		return fmt.Errorf("segment %q ends in a dot or a space", seg)
	}
	for _, c := range []byte(seg) {
		if c < 0x20 || strings.IndexByte(`<>:"|?*\`, c) >= 0 {
			return fmt.Errorf("segment %q has a character Windows forbids", seg)
		}
	}
	stem, _, _ := strings.Cut(seg, ".")
	if windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
		return fmt.Errorf("segment %q is a Windows reserved name", seg)
	}
	return nil
}
