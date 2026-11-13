package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/micromdm/plist"
)

// Managed settings are the config.yaml keys MDM or GPO may set: values under
// HKLM\SOFTWARE\Policies\<Name> on Windows, keys of the machine-level managed
// preferences plist on macOS. They use the YAML key names and win over
// config.yaml key by key.

// managedSecrets are never taken from policy: both policy stores are readable
// by every local user, so these stay in the SYSTEM- or root-only config.yaml.
var managedSecrets = []string{"auth_user", "auth_pass"}

// readManagedSettings is a variable so tests can supply policy on any host.
// It and applyManaged report problems through warn: config.Get runs before
// the log exists, so Get keeps them in ManagedWarnings for the caller to log.
var readManagedSettings = readManaged

// managedFields maps each key policy may set to its field in c. Branding
// fields are "branding.<key>". app_data_path, service_name and
// service_pipe_name are left out: they must match the build's identity.
func managedFields(c *Configuration) map[string]any {
	return map[string]any{
		"url":              &c.URL,
		"url_packages":     &c.URLPackages,
		"manifest":         &c.Manifest,
		"local_manifests":  &c.LocalManifests,
		"catalogs":         &c.Catalogs,
		"verbose":          &c.Verbose,
		"debug":            &c.Debug,
		"log_file_plain":   &c.LogFilePlain,
		"checkonly":        &c.CheckOnly,
		"tls_auth":         &c.TLSAuth,
		"tls_client_cert":  &c.TLSClientCert,
		"tls_client_key":   &c.TLSClientKey,
		"tls_server_cert":  &c.TLSServerCert,
		"service_interval": &c.ServiceInterval,

		"tls_client_cert_store":       &c.TLSClientCertStore,
		"tls_client_cert_store_field": &c.TLSClientCertStoreField,

		"branding.title":            &c.Branding.Title,
		"branding.tagline":          &c.Branding.Tagline,
		"branding.logo":             &c.Branding.Logo,
		"branding.background_image": &c.Branding.BackgroundImage,
		"branding.help_url":         &c.Branding.HelpURL,
		"branding.help_label":       &c.Branding.HelpLabel,
		"branding.accent":           &c.Branding.Accent,
		"branding.list_heading":     &c.Branding.ListHeading,
		"branding.sections":         &c.Branding.Sections,
	}
}

// applyManaged sets each managed value over cfg. A blank string or empty list
// is unset and keeps config.yaml's value; a list replaces config.yaml's whole.
// A value of the wrong type is dropped with a warning, unknown keys (WriteRoots,
// payload metadata) are ignored.
func applyManaged(cfg *Configuration, managed map[string]any, warn func(string)) {
	fields := managedFields(cfg)
	set := func(name string, raw any) {
		if slices.Contains(managedSecrets, name) {
			warn(fmt.Sprintf("managed setting %s ignored: set it in config.yaml", name))
			return
		}
		ptr, ok := fields[name]
		if !ok {
			return
		}
		switch p := ptr.(type) {
		case *string:
			if s, ok := raw.(string); ok {
				if strings.TrimSpace(s) != "" {
					*p = s
				}
				return
			}
		case *[]string:
			if list, ok := stringList(raw); ok {
				if len(list) > 0 {
					*p = list
				}
				return
			}
		case *bool:
			if b, ok := boolValue(raw); ok {
				*p = b
				return
			}
		}
		warn(fmt.Sprintf("managed setting %s dropped: wrong type %T", name, raw))
	}
	for name, raw := range managed {
		if name != "branding" {
			set(name, raw)
			continue
		}
		block, ok := raw.(map[string]any)
		if !ok {
			warn(fmt.Sprintf("managed setting %s dropped: not a dictionary", name))
			continue
		}
		for key, value := range block {
			set("branding."+key, value)
		}
	}
}

// stringList accepts a list whose entries are all strings.
func stringList(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// boolValue accepts a bool or, for a REG_DWORD or a plist integer, a number:
// non-zero is true.
func boolValue(raw any) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case uint64:
		return v != 0, true
	case int64:
		return v != 0, true
	case int:
		return v != 0, true
	}
	return false, false
}

// parseManagedPlist reads a managed preferences plist, binary or XML, into its
// top-level keys.
func parseManagedPlist(data []byte) (map[string]any, error) {
	var out map[string]any
	if err := plist.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
