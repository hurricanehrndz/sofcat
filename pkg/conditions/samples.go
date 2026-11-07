package conditions

// SampleFacts returns an example value for every built-in fact SofCat
// collects on goos (darwin, windows or linux): the conditions guide's
// examples (site/src/content/docs/guides/conditions.md, "Built-in facts").
// makecatalogs renders templates against them, since it has no machine. The
// per-OS tests in pkg/facts assert every built-in they collect is here, so
// the two cannot drift.
func SampleFacts(goos string) Facts {
	facts := Facts{
		"hostname":       "LAB-GPU-01",
		"os":             goos,
		"arch":           "amd64",
		"ipv4_address":   []string{"10.20.1.5"},
		"engine_version": "v1.4.0",
	}
	switch goos {
	case "windows":
		facts["domain"] = "corp.example.com"
		facts["os_version"] = "10.0.22631.4460"
		facts["os_build"] = 22631
		facts["os_edition"] = "Enterprise"
		facts["machine_manufacturer"] = "LENOVO"
		facts["machine_model"] = "20XW0055US"
		facts["machine_type"] = "laptop"
		facts["serial_number"] = "PF1ABCDE"
	case "darwin":
		facts["os_version"] = "15.1"
		facts["machine_manufacturer"] = "Apple"
		facts["machine_model"] = "Mac16,12"
		facts["machine_type"] = "laptop"
		facts["serial_number"] = "PF1ABCDE"
	}
	return facts
}
