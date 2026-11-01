// Package cli implements the command line interface.
package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type command struct {
	name string
	desc string
	run  func([]string) error
}

var commands = []command{
	{"run", "run benchmarks and merge them into the JSON database", cmdRun},
	{"tui", "interactive selection (the default with no arguments)", cmdTUI},
	{"corpus", "list, fetch, verify or remove cached corpora", cmdCorpus},
	{"validate", "round-trip integrity check with xxh3; writes nothing", cmdValidate},
	{"list", "list codecs, corpora, recorded runs or presets", cmdList},
	{"serve", "serve web/ over HTTP so the site loads as ES modules", cmdServe},
	{"version", "print tool, Go, library and environment information", cmdVersion},
}

// Main dispatches a subcommand.
func Main(args []string) error {
	if len(args) == 0 {
		return cmdTUI(nil)
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return nil
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:])
		}
	}
	usage()
	return fmt.Errorf("unknown command %q", args[0])
}

func usage() {
	fmt.Fprintln(os.Stderr, "gocomp <command> [flags]")
	fmt.Fprintln(os.Stderr)
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-9s %s\n", c.name, c.desc)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'gocomp <command> -h' for the flags of a command.")
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "gocomp %s [flags]\n\n", name)
		fs.PrintDefaults()
	}
	return fs
}

// splitList splits a comma separated flag value, dropping empties.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseInts parses a thread sweep such as "1,2,4,8".
func parseInts(s string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, p := range splitList(s) {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", p)
		}
		if n < 1 {
			return nil, fmt.Errorf("thread count must be at least 1, got %d", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// parseLevels parses "1-9,-2". A '-' at index 0 is a sign, anywhere else it
// separates a range, so "-3--1" is the range -3 to -1 and "-2" is a single level.
func parseLevels(s string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(n int) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, p := range splitList(s) {
		if i := strings.IndexByte(p[1:], '-'); i >= 0 {
			lo, err1 := strconv.Atoi(p[:i+1])
			hi, err2 := strconv.Atoi(p[i+2:])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid level range %q", p)
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			for n := lo; n <= hi; n++ {
				add(n)
			}
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid level %q", p)
		}
		add(n)
	}
	sort.Ints(out)
	return out, nil
}
