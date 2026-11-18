package config

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v4"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/version"
)

var (
	// Define flag defaults
	aboutArg          bool
	aboutDefault      = false
	configArg         string
	configDefault     = identity.ConfigPath()
	debugArg          bool
	debugDefault      = false
	helpArg           bool
	helpDefault       = false
	verboseArg        bool
	verboseDefault    = false
	checkOnlyArg      bool
	checkOnlyDefault  = false
	versionArg        bool
	versionDefault    = false
	serviceArg        bool
	serviceDefault    = false
	serviceCmdArg     string
	serviceCmdDefault = ""
	serviceInstallArg bool
	serviceRemoveArg  bool
	serviceStartArg   bool
	serviceStopArg    bool
	serviceStatusArg  bool

	// Use a fake function so we can override when testing
	osExit = os.Exit
)

const usage = `
SofCat - Munki-like Application Management for Windows
https://github.com/hurricanehrndz/sofcat

Usage: sofcat.exe [options]

Options:
-c, -config         path to configuration file in yaml format
-C, -checkonly	    enable check only mode
-v, -verbose        enable verbose output
-d, -debug          enable debug output
-a, -about          displays the version number and other build info
-V, -version        display the version number
-s, -service        run SofCat as a service (Windows SCM or macOS launchd)
-S, -servicecmd     send a command to a running SofCat service (run|GetServiceInfo|ListOptionalInstalls|GetBranding|InstallItem:itemName[:placement]|RemoveItem:itemName[:placement]|StreamOperationStatus:operationId|CancelOperation:operationId|ListUserPlacements|ReportUserPlacements:item,placement,outcome[,error])
-serviceinstall     install and start the SofCat service, restrict its data directory to SYSTEM and Administrators (root on macOS) and register the per-user helper at logon (Windows)
-serviceremove      stop and remove the SofCat service and the helper's logon entry, and end running helpers and storefronts (Windows)
-servicestart       start the SofCat service
-servicestop        stop the SofCat service
-servicestatus      show the SofCat service status
-h, -help           display this help message

`

// Configuration stores all of the possible parameters a config file could contain
type Configuration struct {
	URL             string   `yaml:"url"`
	URLPackages     string   `yaml:"url_packages"`
	Manifest        string   `yaml:"manifest"`
	LocalManifests  []string `yaml:"local_manifests,omitempty"`
	Catalogs        []string `yaml:"catalogs"`
	AppDataPath     string   `yaml:"app_data_path"`
	Verbose         bool     `yaml:"verbose,omitempty"`
	Debug           bool     `yaml:"debug,omitempty"`
	LogFilePlain    bool     `yaml:"log_file_plain,omitempty"`
	CheckOnly       bool     `yaml:"checkonly,omitempty"`
	AuthUser        string   `yaml:"auth_user,omitempty"`
	AuthPass        string   `yaml:"auth_pass,omitempty"`
	TLSAuth         bool     `yaml:"tls_auth,omitempty"`
	TLSClientCert   string   `yaml:"tls_client_cert,omitempty"`
	TLSClientKey    string   `yaml:"tls_client_key,omitempty"`
	TLSServerCert   string   `yaml:"tls_server_cert,omitempty"`
	CachePath       string
	NupkgPath       string
	ServiceMode     bool `yaml:"service_mode,omitempty"`
	ServiceCommand  string
	ServiceInstall  bool
	ServiceRemove   bool
	ServiceStart    bool
	ServiceStop     bool
	ServiceStatus   bool
	ServiceName     string   `yaml:"service_name,omitempty"`
	ServiceInterval string   `yaml:"service_interval,omitempty"`
	ServicePipeName string   `yaml:"service_pipe_name,omitempty"`
	Branding        Branding `yaml:"branding,omitempty"`
	ConfigPath      string
	// RequestedBy maps a self-service item to the user whose request the run
	// is carrying out, for the inventory. The service sets it per run; it is
	// never read from config.yaml.
	RequestedBy map[string]string `yaml:"-"`
	// ManagedWarnings are the managed settings Get ignored or dropped, for
	// the service to log at start.
	ManagedWarnings []string `yaml:"-"`
	// TLSClientCertStore selects the tls_auth client certificate from the
	// machine store (Windows LocalMachine, the macOS System keychain) by a
	// regular expression on TLSClientCertStoreField (subject, the default,
	// issuer, serial or dns_names), in place of TLSClientCert and TLSClientKey.
	TLSClientCertStore      string `yaml:"tls_client_cert_store,omitempty"`
	TLSClientCertStoreField string `yaml:"tls_client_cert_store_field,omitempty"`
}

// Branding is the optional organisation branding block for SofCat UI. Managed
// settings override it per field (applyManaged); pkg/branding validates it.
type Branding struct {
	Title   string `yaml:"title,omitempty"`
	Tagline string `yaml:"tagline,omitempty"`
	Logo    string `yaml:"logo,omitempty"`
	// BackgroundImage is a local image the banner shows in place of the accent
	// colour.
	BackgroundImage string `yaml:"background_image,omitempty"`
	// BackgroundText is the banner text colour over BackgroundImage: light
	// (the default) or dark.
	BackgroundText string `yaml:"background_text,omitempty"`
	// LinkURL and LinkLabel are the button that opens the admin's page.
	// LinkPlacement puts it in the banner (the default) or the app bar, and
	// LinkPosition in the banner: auto (the default) or a corner.
	LinkURL       string `yaml:"link_url,omitempty"`
	LinkLabel     string `yaml:"link_label,omitempty"`
	LinkPlacement string `yaml:"link_placement,omitempty"`
	LinkPosition  string `yaml:"link_position,omitempty"`
	// HelpURL and HelpLabel are the released names of link_url and
	// link_label, still accepted. foldLinkAliases moves them into the new
	// fields source by source, so nothing past config reads them.
	HelpURL   string `yaml:"help_url,omitempty"`
	HelpLabel string `yaml:"help_label,omitempty"`
	Accent    string `yaml:"accent,omitempty"`
	// ListHeading replaces the UI's "Available software" heading.
	ListHeading string `yaml:"list_heading,omitempty"`
	// TitleSize and TaglineSize scale the banner title (with the page
	// headings) and the tagline: small, medium (the default), large or x-large.
	TitleSize   string `yaml:"title_size,omitempty"`
	TaglineSize string `yaml:"tagline_size,omitempty"`
	// ContentWidth is how much of the window the storefront uses: compact
	// (the layout before the key), wide (the default) or full.
	ContentWidth string `yaml:"content_width,omitempty"`
	// FilterPlacement is where the list's search and category filters sit:
	// right (the default, on the heading's row) or left (above the heading).
	FilterPlacement string `yaml:"filter_placement,omitempty"`
	// AppBarBrand is what the app bar shows of the product: full (the
	// default, mark and name), mark or none.
	AppBarBrand string `yaml:"app_bar_brand,omitempty"`
	// BannerStyle is text (the default, the logo beside the title) or logo
	// (the logo in the title's place).
	BannerStyle string `yaml:"banner_style,omitempty"`
	// BannerAlign aligns the banner's content: left (the default), center or
	// right.
	BannerAlign string `yaml:"banner_align,omitempty"`
	// BannerHeight is the banner's minimum height: small, medium (the
	// default), large or x-large.
	BannerHeight string `yaml:"banner_height,omitempty"`
	// Progress is where the storefront shows running work: strip (the
	// default, a bar along the bottom) or button (in each item's button).
	Progress string `yaml:"progress,omitempty"`
	// ItemView is how the storefront opens an item: page (the default, a
	// detail page in the list's place) or dialog (a dialog over the list).
	ItemView string `yaml:"item_view,omitempty"`
	// Sections orders the storefront tabs built from catalog item sections.
	Sections []string `yaml:"sections,omitempty"`
}

// foldLinkAliases moves help_url and help_label into link_url and link_label
// where those are unset, so within one source the new name wins. resolve runs
// it on config.yaml before applyManaged, which does the same for the managed
// settings: a policy that still uses an old name overrides config.yaml's new
// one, as any managed key overrides the file.
func (b *Branding) foldLinkAliases() {
	if strings.TrimSpace(b.LinkURL) == "" {
		b.LinkURL = b.HelpURL
	}
	if strings.TrimSpace(b.LinkLabel) == "" {
		b.LinkLabel = b.HelpLabel
	}
	b.HelpURL, b.HelpLabel = "", ""
}

// registerFlags defines sofcat's flags on flag.CommandLine. Get calls it, not
// init: the user agent and the storefront import this package through
// pkg/service, and their own command lines must not inherit sofcat's flags.
// Once, because the tests parse several command lines.
var registerFlags = sync.OnceFunc(func() {
	// Define flag names and defaults here

	// About
	flag.BoolVar(&aboutArg, "about", aboutDefault, "")
	flag.BoolVar(&aboutArg, "a", aboutDefault, "")
	// Config
	flag.StringVar(&configArg, "config", configDefault, "")
	flag.StringVar(&configArg, "c", configDefault, "")
	// Debug
	flag.BoolVar(&debugArg, "debug", debugDefault, "")
	flag.BoolVar(&debugArg, "d", debugDefault, "")
	// Checkonly
	flag.BoolVar(&checkOnlyArg, "checkonly", checkOnlyDefault, "")
	flag.BoolVar(&checkOnlyArg, "C", checkOnlyDefault, "")
	// Help
	flag.BoolVar(&helpArg, "help", helpDefault, "")
	flag.BoolVar(&helpArg, "h", helpDefault, "")
	// Verbose
	flag.BoolVar(&verboseArg, "verbose", verboseDefault, "")
	flag.BoolVar(&verboseArg, "v", verboseDefault, "")
	// Version
	flag.BoolVar(&versionArg, "version", versionDefault, "")
	flag.BoolVar(&versionArg, "V", versionDefault, "")
	// Service mode
	flag.BoolVar(&serviceArg, "service", serviceDefault, "")
	flag.BoolVar(&serviceArg, "s", serviceDefault, "")
	// Service command
	flag.StringVar(&serviceCmdArg, "servicecmd", serviceCmdDefault, "")
	flag.StringVar(&serviceCmdArg, "S", serviceCmdDefault, "")
	// Service install/remove/start/stop
	flag.BoolVar(&serviceInstallArg, "serviceinstall", false, "")
	flag.BoolVar(&serviceRemoveArg, "serviceremove", false, "")
	flag.BoolVar(&serviceStartArg, "servicestart", false, "")
	flag.BoolVar(&serviceStopArg, "servicestop", false, "")
	flag.BoolVar(&serviceStatusArg, "servicestatus", false, "")
})

func parseArguments() (string, bool, bool, bool) {
	// Get the command line args
	registerFlags()
	flag.Parse()
	if helpArg {
		version.Print()
		fmt.Print(usage)
		osExit(0)
	}
	if versionArg {
		version.Print()
		osExit(0)
	}
	if aboutArg {
		version.PrintFull()
		osExit(0)
	}

	return configArg, verboseArg, debugArg, checkOnlyArg
}

// Get retrieves and parses the config file and returns a Configuration struct and any errors
func Get() Configuration {
	var cfg Configuration

	// Parse any arguments that may have been passed
	configPath, _, _, _ := parseArguments()

	serviceControlMode := serviceInstallArg || serviceRemoveArg || serviceStartArg || serviceStopArg || serviceStatusArg
	serviceClientMode := serviceCmdArg != ""

	// Managed settings (MDM or GPO) win over config.yaml key by key, and can
	// stand in for the file altogether.
	var warnings []string
	warn := func(msg string) { warnings = append(warnings, msg) }
	managed := readManagedSettings(warn)

	// Read the config file. The installer runs -serviceinstall, and the
	// service starts, before management tooling has written config.yaml, so
	// the service-control flags and service mode run with defaults until
	// the file exists; every managed run fails until then, unless managed
	// settings supply the url and manifest, and a service
	// restart picks the file up.
	configFile, err := os.ReadFile(configPath)
	if err != nil && (!serviceArg && !serviceControlMode && len(managed) == 0 || !errors.Is(err, fs.ErrNotExist)) {
		fmt.Println("Unable to read configuration file: ", err)
		osExit(1)
	}

	// Parse the config into a struct
	err = yaml.Unmarshal(configFile, &cfg)
	if err != nil {
		fmt.Println("Unable to parse yaml configuration: ", err)
		osExit(1)
	}
	file := cfg
	fileConfig = &file
	cfg = resolve(cfg, managed, warn)
	cfg.ManagedWarnings = warnings
	// The service logs them once its log exists; anything else has a console.
	if !serviceArg {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, w)
		}
	}

	// Normal run mode requires both manifest and URL. Service mode starts
	// without them and waits for config.yaml (see above).
	if !serviceControlMode && !serviceClientMode && !serviceArg {
		if cfg.Manifest == "" {
			fmt.Println("Invalid configuration - Manifest: ", err)
			osExit(1)
		}

		if cfg.URL == "" {
			fmt.Println("Invalid configuration - URL: ", err)
			osExit(1)
		}
	}

	return cfg
}

// fileConfig is config.yaml as Get parsed it, before managed settings, for
// Refresh to apply them to afresh.
var fileConfig *Configuration

// Refresh returns the configuration under the managed settings as they are
// now. The service calls it at the start of every run, so a profile or GPO
// that arrives after it started needs no restart, and a setting the policy
// drops goes back to config.yaml's value. config.yaml and the command line
// are still read once, by Get; app_data_path, service_name and
// service_pipe_name are not managed settings, so they never change. A cfg
// that Get did not build is returned as it is.
func Refresh(cfg Configuration) Configuration {
	if fileConfig == nil {
		return cfg
	}
	var warnings []string
	warn := func(msg string) { warnings = append(warnings, msg) }
	next := resolve(*fileConfig, readManagedSettings(warn), warn)
	next.ManagedWarnings = warnings
	return next
}

// resolve returns cfg, config.yaml as parsed, under the managed settings,
// the command line and the defaults.
func resolve(cfg Configuration, managed map[string]any, warn func(string)) Configuration {
	cfg.Branding.foldLinkAliases()
	applyManaged(&cfg, managed, warn)

	// If URLPackages wasn't provided, use the repo URL when available.
	if cfg.URLPackages == "" && cfg.URL != "" {
		cfg.URLPackages = cfg.URL
	}

	// If AppDataPath wasn't provided, configure a default
	if cfg.AppDataPath == "" {
		cfg.AppDataPath = identity.DataDir()
	} else {
		cfg.AppDataPath = filepath.Clean(cfg.AppDataPath)
	}

	// Set the verbosity
	if verboseArg && !cfg.Verbose {
		cfg.Verbose = true
	}

	// Set the debug and verbose
	if debugArg && !cfg.Debug {
		cfg.Debug = true
		cfg.Verbose = true
	}

	if checkOnlyArg && !cfg.CheckOnly {
		cfg.CheckOnly = true
	}
	cfg.ConfigPath = configArg
	cfg.ServiceMode = serviceArg
	cfg.ServiceCommand = serviceCmdArg
	cfg.ServiceInstall = serviceInstallArg
	cfg.ServiceRemove = serviceRemoveArg
	cfg.ServiceStart = serviceStartArg
	cfg.ServiceStop = serviceStopArg
	cfg.ServiceStatus = serviceStatusArg

	// Set the cache path and the root of the kept nupkg copies
	cfg.CachePath = filepath.Join(cfg.AppDataPath, "cache")
	cfg.NupkgPath = filepath.Join(cfg.AppDataPath, "nupkg")

	// Configure service defaults.
	if cfg.ServiceName == "" {
		cfg.ServiceName = identity.ServiceName()
	}
	if cfg.ServiceInterval == "" {
		cfg.ServiceInterval = "1h"
	}
	if cfg.ServicePipeName == "" {
		cfg.ServicePipeName = identity.PipeName()
	}

	return cfg
}
