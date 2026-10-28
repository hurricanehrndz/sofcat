package facts

import (
	"encoding/binary"
	"strings"
)

// smbiosInfo is what the facts need from SMBIOS: the System Information
// (type 1) strings and the System Enclosure (type 3) chassis type.
type smbiosInfo struct {
	manufacturer string
	model        string
	serial       string
	chassis      byte
}

// machineType is "laptop" for a portable chassis and "desktop" otherwise,
// as Munki's machine_type reads.
func (s smbiosInfo) machineType() string {
	switch s.chassis {
	case 8, 9, 10, 11, 14, 30, 31, 32: // portable, laptop, notebook, hand held, sub notebook, tablet, convertible, detachable
		return "laptop"
	}
	return "desktop"
}

// parseSMBIOS reads a GetSystemFirmwareTable('RSMB') buffer: an 8-byte
// RawSMBIOSData header, then structures, each a formatted area (type, length,
// handle, fields) followed by its strings, ended by a double NUL. Fields refer
// to strings by 1-based index. A truncated buffer yields what was read so far.
func parseSMBIOS(raw []byte) smbiosInfo {
	var info smbiosInfo
	if len(raw) < 8 {
		return info
	}
	tableLen := int(binary.LittleEndian.Uint32(raw[4:8]))
	data := raw[8:]
	if tableLen < len(data) {
		data = data[:tableLen]
	}

	for len(data) >= 4 {
		typ, length := data[0], int(data[1])
		if length < 4 || length > len(data) {
			break
		}
		formatted := data[:length]
		rest := data[length:]
		end := strings.Index(string(rest), "\x00\x00")
		if end < 0 {
			break
		}
		strs := strings.Split(string(rest[:end]), "\x00")
		str := func(offset int) string {
			if offset >= len(formatted) {
				return ""
			}
			i := int(formatted[offset])
			if i == 0 || i > len(strs) {
				return ""
			}
			return strings.TrimSpace(strs[i-1])
		}

		switch typ {
		case 1:
			info.manufacturer = str(4)
			info.model = str(5)
			info.serial = str(7)
		case 3:
			if len(formatted) > 5 {
				info.chassis = formatted[5] & 0x7f // bit 7 is the chassis lock flag
			}
		case 127: // end of table
			return info
		}
		data = rest[end+2:]
	}
	return info
}
