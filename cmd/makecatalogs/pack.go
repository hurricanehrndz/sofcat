package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
)

const packUsage = `Usage: makecatalogs pack [--os darwin|windows] <dir> -o <file.zip>

Writes a deterministic zip of <dir> and prints {"hash","tree_hash"} as JSON.
Without --os, files keep their executable bits (none on Windows).

Options:
`

const treeHashUsage = `Usage: makecatalogs tree-hash [--os darwin|windows] <dir>

Prints the tree hash of <dir>, sha256:<hex>, and writes nothing.

Options:
`

func runPack(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("pack", packUsage, stderr)
	goos := flags.String("os", "", "target OS: darwin or windows (default: the OS this runs on)")
	out := flags.String("o", "", "zip file to write (required)")
	positional, code := parseArgs(flags, args, 1)
	if code >= 0 {
		return code
	}
	if *out == "" {
		flags.Usage()
		return 2
	}
	target, err := targetOS(*goos)
	if err != nil {
		return fail(stderr, err)
	}
	packed, err := artifact.PackFile(positional[0], target, *out)
	if err != nil {
		return fail(stderr, err)
	}
	line, err := json.Marshal(packed)
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, string(line))
	return 0
}

func runTreeHash(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("tree-hash", treeHashUsage, stderr)
	goos := flags.String("os", "", "target OS: darwin or windows (default: the OS this runs on)")
	positional, code := parseArgs(flags, args, 1)
	if code >= 0 {
		return code
	}
	target, err := targetOS(*goos)
	if err != nil {
		return fail(stderr, err)
	}
	sum, err := artifact.TreeHash(positional[0], target)
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, sum)
	return 0
}

// targetOS validates an --os value. Empty means the OS this runs on: Windows
// drops executable bits, any other OS keeps them.
func targetOS(goos string) (string, error) {
	switch goos {
	case "":
		return runtime.GOOS, nil
	case artifact.Darwin, artifact.Windows:
		return goos, nil
	default:
		return "", fmt.Errorf("--os %q: want darwin or windows", goos)
	}
}

func newFlagSet(name, usage string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("makecatalogs "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	return flags
}

// parseArgs parses flags that may come before, between or after the
// positional arguments, and requires exactly n of those. It returns the
// positional arguments and -1, or an exit code when the command should stop.
func parseArgs(flags *flag.FlagSet, args []string, n int) ([]string, int) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, 0
			}
			return nil, 2
		}
		if flags.NArg() == 0 {
			break
		}
		positional = append(positional, flags.Arg(0))
		args = flags.Args()[1:]
	}
	if len(positional) != n {
		flags.Usage()
		return nil, 2
	}
	return positional, -1
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "makecatalogs:", err)
	return 1
}
