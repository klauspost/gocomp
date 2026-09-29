package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/klauspost/compress-site/internal/corpus"
)

func cmdCorpus(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: compress-site corpus list|fetch|verify|rm|path [ids...]")
	}
	fs := newFlags("corpus " + args[0])
	cacheDir := fs.String("cache", "", "corpus cache directory (default testdata, or $"+corpus.EnvCache+")")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	dir := corpus.CacheDir(*cacheDir)
	cache, err := corpus.OpenCache(dir)
	if err != nil {
		return err
	}

	switch args[0] {
	case "path":
		abs, _ := filepath.Abs(dir)
		fmt.Println(abs)
		return nil
	case "list":
		return corpusList(cache)
	case "fetch":
		return corpusFetch(cache, fs.Args())
	case "verify":
		return corpusVerify(cache, fs.Args())
	case "rm":
		return corpusRemove(cache, fs.Args())
	}
	return fmt.Errorf("unknown corpus subcommand %q", args[0])
}

func corpusList(cache *corpus.Cache) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "id\tkind\ttier\tdownload\tsize\tfiles\tstate\tdescription")
	for _, c := range corpus.All() {
		state := "missing"
		if _, adopted, ok := cache.Resolve(c); ok {
			state = "cached"
			if adopted {
				state = "adopted"
			}
		} else if c.Synth != nil {
			state = "generated"
		} else if c.Remote == "" {
			state = "manual"
		}
		size, files := c.Bytes(), c.Files
		if e, ok := cache.Entries[c.ID]; ok && e.Size > 0 {
			size, files = e.Size, e.Files
		}
		dl := "-"
		if c.DL > 0 {
			dl = humanBytes(c.DL)
		}
		nf := "-"
		if c.Kind == corpus.KindDir {
			nf = strconv.Itoa(files)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID, c.Kind, c.Tier(), dl, humanBytes(size), nf, state, c.Desc)
	}
	return tw.Flush()
}

func corpusSelect(args []string) ([]corpus.Corpus, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("name at least one corpus id, a tier, or all")
	}
	return corpus.Select(args)
}

func corpusFetch(cache *corpus.Cache, args []string) error {
	list, err := corpusSelect(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	prog := progressTo(runOpts{})
	for _, c := range list {
		if c.Remote == "" && c.Synth == nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", c.ID, c.Note)
			continue
		}
		if err := corpus.Fetch(ctx, cache, c, prog); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
		fmt.Fprintf(os.Stderr, "\r\033[K%s: ready\n", c.ID)
	}
	return cache.Save()
}

// corpusVerify re-reads each corpus and records the measured size and hash, so
// "size unknown until extracted" resolves itself and a damaged cache is caught.
func corpusVerify(cache *corpus.Cache, args []string) error {
	list, err := corpusSelect(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "id\tsize\tfiles\txxh3\tstatus")
	for _, c := range list {
		d, err := corpus.Load(ctx, cache, c, nil)
		if err != nil {
			fmt.Fprintf(tw, "%s\t-\t-\t-\t%v\n", c.ID, err)
			continue
		}
		status := "ok"
		if prev, ok := cache.Entries[c.ID]; ok && prev.Hash != "" {
			if prev.Hash != strconv.FormatUint(d.Hash, 16) {
				status = "CHANGED since last verify"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%016x\t%s\n", c.ID, humanBytes(d.Total), len(d.Files), d.Hash, status)
		tw.Flush()
	}
	if err := cache.Save(); err != nil {
		return err
	}
	return tw.Flush()
}

func corpusRemove(cache *corpus.Cache, args []string) error {
	list, err := corpusSelect(args)
	if err != nil {
		return err
	}
	for _, c := range list {
		path, adopted, ok := cache.Resolve(c)
		if !ok {
			continue
		}
		if adopted {
			fmt.Fprintf(os.Stderr, "%s: adopted in place at %s, not removing\n", c.ID, path)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if c.Remote != "" {
			_ = os.Remove(filepath.Join(cache.Dir, "dl", c.Remote))
		}
		delete(cache.Entries, c.ID)
		fmt.Fprintf(os.Stderr, "%s: removed\n", c.ID)
	}
	return cache.Save()
}
