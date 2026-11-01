package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/klauspost/gocomp/internal/bench"
	"github.com/klauspost/gocomp/internal/codec"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/klauspost/gocomp/internal/db"
	"github.com/klauspost/gocomp/internal/preset"
	"github.com/klauspost/gocomp/internal/sysinfo"
)

func cmdList(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gocomp list codecs|corpora|runs|presets")
	}
	fs := newFlags("list " + args[0])
	asJSON := fs.Bool("json", false, "emit JSON")
	format := fs.String("format", "", "only codecs of this format")
	dir := fs.String("out", "web/data", "database directory, for 'list runs'")
	presets := fs.String("presets", "", "preset directory (default "+preset.DefaultDir+")")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "codecs":
		return listCodecs(*asJSON, *format)
	case "corpora":
		return listCorpora(*asJSON)
	case "runs":
		return listRuns(*asJSON, *dir)
	case "presets":
		return listPresets(*asJSON, preset.Dir(*presets))
	}
	return fmt.Errorf("unknown list target %q", args[0])
}

func listPresets(asJSON bool, dir string) error {
	ps, err := preset.List(dir)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(ps)
	}
	if len(ps) == 0 {
		fmt.Fprintf(os.Stderr, "no presets in %s; save one from the TUI with p\n", dir)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "name\tselection\tdescription")
	for _, p := range ps {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Summary(), p.Desc)
	}
	return tw.Flush()
}

func listCodecs(asJSON bool, format string) error {
	ix := &db.Index{Machines: map[string]db.MachineInfo{}}
	ix.RefreshCatalogs()
	if format != "" {
		var keep []db.CodecInfo
		for _, c := range ix.Codecs {
			if c.Format == format {
				keep = append(keep, c)
			}
		}
		ix.Codecs = keep
	}
	if asJSON {
		return writeJSON(ix.Codecs)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "id\tformat\timpl\trole\tkinds\tconcurrent\tlevels")
	dash := func(s []string) string {
		if len(s) == 0 {
			return "-"
		}
		return strings.Join(s, ",")
	}
	for _, c := range ix.Codecs {
		if r := c.Encode; r != nil {
			var lv []string
			for _, l := range r.Levels {
				s := fmt.Sprintf("%d:%s", l.N, l.Name)
				if l.Slow {
					s += "*"
				}
				lv = append(lv, s)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\tencode\t%s\t%s\t%s\n",
				c.ID, c.Format, c.Impl, dash(r.Kinds), dash(r.Conc), strings.Join(lv, " "))
		}
		if r := c.Decode; r != nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\tdecode\t%s\t%s\t\n",
				c.ID, c.Format, c.Impl, dash(r.Kinds), dash(r.Conc))
		}
	}
	fmt.Fprintln(tw, "\n* slow: needs -slow")
	return tw.Flush()
}

func listCorpora(asJSON bool) error {
	if asJSON {
		ix := &db.Index{Machines: map[string]db.MachineInfo{}}
		ix.RefreshCatalogs()
		return writeJSON(ix.Corpora)
	}
	cache, err := corpus.OpenCache(corpus.CacheDir(""))
	if err != nil {
		return err
	}
	return corpusList(cache)
}

func listRuns(asJSON bool, dir string) error {
	ix, err := db.LoadIndex(dir)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(ix.Runs)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "corpus\tid\tcpu\tgo\tgoamd64\tbench\tthreads\tencode\tdecode\tmerges\tupdated")
	for _, r := range ix.Runs {
		cpu := r.Machine
		if m, ok := ix.Machines[r.Machine]; ok {
			cpu = m.Label
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%v\t%d\t%d\t%d\t%s\n",
			r.Corpus, r.ID, cpu, r.Go, r.GOAMD64, r.Bench, r.Threads,
			r.NEncode, r.NDecode, r.Merges, r.Updated)
	}
	return tw.Flush()
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func cmdVersion(args []string) error {
	fs := newFlags("version")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	env := sysinfo.Detect()
	libs := sysinfo.Libs(codec.Modules())
	info := struct {
		Tool  sysinfo.Tool      `json:"tool"`
		Bench string            `json:"bench"`
		Libs  map[string]string `json:"libs"`
		Env   sysinfo.Env       `json:"env"`
	}{sysinfo.ToolInfo(), bench.Version, libs, env}

	if *asJSON {
		return writeJSON(info)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "tool\t%s %s\n", info.Tool.Version, info.Tool.Revision)
	fmt.Fprintf(tw, "bench version\t%s\n", bench.Version)
	fmt.Fprintf(tw, "go\t%s %s/%s", env.Go, env.GOOS, env.GOARCH)
	if env.GOAMD64 != "" {
		fmt.Fprintf(tw, " GOAMD64=%s", env.GOAMD64)
	}
	fmt.Fprintln(tw)
	fmt.Fprintf(tw, "cpu\t%s (%d cores / %d threads, x86-64-v%d)\n",
		env.CPU.Brand, env.CPU.PhysicalCores, env.CPU.LogicalCores, env.CPU.X64Level)
	fmt.Fprintf(tw, "memory\t%s\n", humanBytes(int64(env.MemTotal)))
	fmt.Fprintf(tw, "os\t%s %s %s\n", env.OS.Name, env.OS.Version, env.OS.Kernel)
	fmt.Fprintln(tw, "libraries\t")
	for _, m := range codec.Modules() {
		fmt.Fprintf(tw, "  %s\t%s\n", m, libs[m])
	}
	if w := env.ArchLevelNote(); w != "" {
		fmt.Fprintf(tw, "\nnote\t%s\n", w)
	}
	return tw.Flush()
}

func cmdServe(args []string) error {
	fs := newFlags("serve")
	addr := fs.String("addr", "localhost:8080", "listen address")
	root := fs.String("root", "web", "directory to serve")
	open := fs.Bool("open", true, "open the site in a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	url := browseURL(ln.Addr())
	fmt.Fprintf(os.Stderr, "serving %s at %s\n", *root, url)
	if *open {
		// The listener is already bound, so the browser's connection waits in
		// the accept backlog instead of being refused. No sleep needed.
		go openBrowser(url)
	}
	// ES modules will not load over file://, which is why this exists.
	//
	// no-cache makes the browser revalidate every file, a 304 when nothing
	// changed. Without it, modules are cached heuristically and one at a time,
	// so after an edit the page can load a new module against an old one that
	// lacks the export it imports, and fail to start at all.
	files := http.FileServer(http.Dir(*root))
	return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
}

// browseURL builds an address a browser can actually use. It reads the bound
// listener rather than the -addr string, so "-addr :0" reports the port the
// kernel chose, and a wildcard bind becomes localhost instead of "[::]".
func browseURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String() + "/"
	}
	switch host {
	case "", "::", "0.0.0.0":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// openBrowser is best effort: a headless or locked-down machine simply gets no
// window, and serve carries on regardless.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// The empty argument is start's window title. Without it start takes
		// the URL as the title and opens nothing.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Run()
}
