// Command makecatalogs compiles a SofCat repository's packages-info files
// into its catalogs, like Munki's makecatalogs. It runs on any platform and
// reads no agent configuration. Its pack, tree-hash and zip-item subcommands
// build zip items (see site/src/content/docs/zip-items/building.md).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/version"
)

const usage = `Usage: makecatalogs [options] <repo_path>
       makecatalogs pack|tree-hash|zip-item ... (-h for each)

Compiles <repo_path>/packages-info/*.yaml into <repo_path>/catalogs/.

Options:
`

// subcommands are matched on the first argument only, so a repo path never
// collides with one unless it is spelled exactly like it; use ./pack then.
var subcommands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"pack":      runPack,
	"tree-hash": runTreeHash,
	"zip-item":  runZipItem,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes makecatalogs and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if sub, ok := subcommands[args[0]]; ok {
			return sub(args[1:], stdout, stderr)
		}
	}

	flags := flag.NewFlagSet("makecatalogs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "validate the repo and report what would be written, without writing; exit nonzero on any problem")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "makecatalogs %s\n", version.Version().Version)
		return 0
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	repoPath := flags.Arg(0)

	set, err := admin.CollectCatalogs(repoPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "makecatalogs:", err)
		return 1
	}

	verb := "Wrote"
	if *check {
		verb = "Would write"
		set.Problems = append(set.Problems, admin.CheckConditionsIndex(repoPath, set)...)
	} else {
		for _, problem := range set.Problems {
			_, _ = fmt.Fprintln(stderr, "warning:", problem)
		}
		if err := admin.WriteCatalogs(repoPath, set); err != nil {
			_, _ = fmt.Fprintln(stderr, "makecatalogs:", err)
			return 1
		}
	}
	for _, name := range set.Names() {
		_, _ = fmt.Fprintf(stdout, "%s catalogs/%s.yaml (%d items)\n", verb, name, len(set.Catalogs[name]))
	}
	if len(set.Conditions) > 0 {
		_, _ = fmt.Fprintf(stdout, "%s %s (%d condition scripts)\n", verb, conditions.IndexPath, len(set.Conditions))
	}

	if *check && len(set.Problems) > 0 {
		for _, problem := range set.Problems {
			_, _ = fmt.Fprintln(stderr, "error:", problem)
		}
		_, _ = fmt.Fprintf(stderr, "makecatalogs: check failed with %d problem(s)\n", len(set.Problems))
		return 1
	}
	return 0
}
