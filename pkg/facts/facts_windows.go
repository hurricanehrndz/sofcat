//go:build windows

package facts

import (
	"fmt"
	"log/slog"
	"strings"
	"unsafe"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// platformFacts adds the Windows facts. A fact that cannot be read is left
// out, so a condition that uses it is an error and the item is not installed.
func platformFacts(facts conditions.Facts) {
	// RtlGetVersion is not subject to the manifest-based version lie of GetVersionEx.
	v := windows.RtlGetVersion()
	ubr, _ := regInt(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "UBR")
	facts["os_version"] = fmt.Sprintf("%d.%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber, ubr)
	facts["os_build"] = int(v.BuildNumber)
	if edition, err := regString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "EditionID"); err == nil {
		facts["os_edition"] = edition
	}

	// The native architecture, not this binary's: an amd64 build under
	// emulation on an ARM64 machine reports arm64.
	if arch, err := regString(`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, "PROCESSOR_ARCHITECTURE"); err == nil {
		facts["arch"] = windowsArch(arch)
	}

	domain, _ := regString(`SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`, "Domain")
	facts["domain"] = domain

	data, err := firmwareTable()
	if err != nil {
		slog.Warn("unable to read SMBIOS", "err", err)
		return
	}
	info := parseSMBIOS(data)
	facts["serial_number"] = info.serial
	facts["machine_manufacturer"] = info.manufacturer
	facts["machine_model"] = info.model
	facts["machine_type"] = info.machineType()
}

func windowsArch(arch string) string {
	switch strings.ToUpper(arch) {
	case "AMD64":
		return "amd64"
	case "ARM64":
		return "arm64"
	case "X86":
		return "x86"
	}
	return strings.ToLower(arch)
}

func regString(path, name string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	s, _, err := k.GetStringValue(name)
	return s, err
}

func regInt(path, name string) (uint64, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer k.Close()
	n, _, err := k.GetIntegerValue(name)
	return n, err
}

var procGetSystemFirmwareTable = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemFirmwareTable")

// firmwareTable returns the raw SMBIOS table ('RSMB' provider), including
// its 8-byte RawSMBIOSData header.
func firmwareTable() ([]byte, error) {
	const rsmb = 0x52534D42
	size, _, err := procGetSystemFirmwareTable.Call(rsmb, 0, 0, 0)
	if size == 0 {
		return nil, err
	}
	buf := make([]byte, size)
	n, _, err := procGetSystemFirmwareTable.Call(rsmb, 0, uintptr(unsafe.Pointer(&buf[0])), size)
	if n == 0 || n > size {
		return nil, err
	}
	return buf[:n], nil
}
