package cli

import (
	"github.com/klauspost/gocomp/internal/preset"
	"github.com/klauspost/gocomp/internal/tui"
)

func cmdTUI(args []string) error {
	fs := newFlags("tui")
	out := fs.String("out", "web/data", "database directory")
	cache := fs.String("cache", "", "corpus cache directory")
	presets := fs.String("presets", "", "preset directory (default "+preset.DefaultDir+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return tui.Run(tui.Options{Out: *out, Cache: *cache, Presets: preset.Dir(*presets)})
}
