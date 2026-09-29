// Package sysinfo captures everything needed to reproduce a benchmark run.
//
// Nothing collected here identifies a machine or a person: no hostname,
// username, filesystem path, MAC address or serial number.
package sysinfo

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/klauspost/cpuid/v2"
	"github.com/pbnjay/memory"
	"github.com/zeebo/xxh3"
)

// x86Features and armFeatures are curated: cpuid.FeatureSet() returns hundreds
// of flags, which would dwarf the measurements in every run file. These are the
// ones that actually change compression codepaths.
var x86Features = []string{
	"SSSE3", "SSE4", "SSE42", "POPCNT", "LZCNT", "MOVBE",
	"AVX", "AVX2", "AVXSLOW", "F16C", "FMA3",
	"AVX512F", "AVX512BW", "AVX512DQ", "AVX512VL", "AVX512VBMI", "AVX512VBMI2", "AVX512VNNI",
	"BMI1", "BMI2", "GFNI", "SHA", "VAES", "VPCLMULQDQ",
}

var armFeatures = []string{"ASIMD", "ASIMDDP", "SHA2", "CRC32", "SVE", "SVE2", "ATOMICS"}

type Cache struct {
	L1I int `json:"l1i"`
	L1D int `json:"l1d"`
	L2  int `json:"l2"`
	L3  int `json:"l3"`
}

type CPU struct {
	Brand          string   `json:"brand"`
	Vendor         string   `json:"vendor"`
	Family         int      `json:"family"`
	Model          int      `json:"model"`
	Stepping       int      `json:"stepping"`
	PhysicalCores  int      `json:"physical_cores"`
	LogicalCores   int      `json:"logical_cores"`
	ThreadsPerCore int      `json:"threads_per_core"`
	BaseHz         int64    `json:"base_hz"`
	BoostHz        int64    `json:"boost_hz"`
	CacheLine      int      `json:"cacheline"`
	Cache          Cache    `json:"cache"`
	X64Level       int      `json:"x64level,omitempty"`
	ARM64Level     string   `json:"arm64level,omitempty"`
	VM             bool     `json:"vm"`
	Hypervisor     string   `json:"hypervisor,omitempty"`
	Features       []string `json:"features"`
}

type OS struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Kernel  string `json:"kernel,omitempty"`
}

// Env is the reproducibility record stored with every run.
type Env struct {
	Go         string `json:"go"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GOAMD64    string `json:"goamd64,omitempty"`
	GOARM64    string `json:"goarm64,omitempty"`
	CGO        bool   `json:"cgo"`
	Tags       string `json:"tags,omitempty"`
	Experiment string `json:"goexperiment,omitempty"`
	GOGC       int    `json:"gogc"`
	GOMemLimit int64  `json:"gomemlimit"`
	NumCPU     int    `json:"numcpu"`
	MemTotal   uint64 `json:"mem_total"`
	OS         OS     `json:"os"`
	CPU        CPU    `json:"cpu"`
}

// Tool identifies the benchmark binary itself.
type Tool struct {
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	Modified bool   `json:"modified,omitempty"`
}

func detectCPU() CPU {
	c := cpuid.CPU
	var feats []string
	names := x86Features
	if runtime.GOARCH == "arm64" {
		names = armFeatures
	}
	for _, n := range names {
		if id := cpuid.ParseFeature(n); id != cpuid.UNKNOWN && c.Has(id) {
			feats = append(feats, n)
		}
	}
	out := CPU{
		Brand: strings.TrimSpace(c.BrandName), Vendor: c.VendorString,
		Family: c.Family, Model: c.Model, Stepping: c.Stepping,
		PhysicalCores: c.PhysicalCores, LogicalCores: c.LogicalCores,
		ThreadsPerCore: c.ThreadsPerCore,
		BaseHz:         c.Hz, BoostHz: c.BoostFreq, CacheLine: c.CacheLine,
		Cache: Cache{L1I: c.Cache.L1I, L1D: c.Cache.L1D, L2: c.Cache.L2, L3: c.Cache.L3},
		VM:    c.VM(), Hypervisor: c.HypervisorVendorString,
		Features: feats,
	}
	switch runtime.GOARCH {
	case "amd64", "386":
		out.X64Level = c.X64Level()
	case "arm64":
		out.ARM64Level = c.GOARM64()
	}
	if out.Brand == "" {
		out.Brand = "unknown " + runtime.GOARCH
	}
	return out
}

// Detect gathers the full environment record.
func Detect() Env {
	e := Env{
		Go: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(), MemTotal: memory.TotalMemory(),
		OS: osInfo(), CPU: detectCPU(),
	}
	// Round-trip the runtime knobs rather than reading the environment, so a
	// programmatic SetGCPercent is recorded accurately too.
	gc := debug.SetGCPercent(100)
	debug.SetGCPercent(gc)
	e.GOGC = gc
	lim := debug.SetMemoryLimit(-1)
	if lim != int64(^uint64(0)>>1) {
		e.GOMemLimit = lim
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "GOOS":
				e.GOOS = s.Value
			case "GOARCH":
				e.GOARCH = s.Value
			case "GOAMD64":
				e.GOAMD64 = s.Value
			case "GOARM64":
				e.GOARM64 = s.Value
			case "CGO_ENABLED":
				e.CGO = s.Value == "1"
			case "-tags":
				e.Tags = s.Value
			case "GOEXPERIMENT":
				e.Experiment = s.Value
			}
		}
	}
	return e
}

// ToolInfo reports the benchmark binary's own version.
func ToolInfo() Tool {
	t := Tool{Version: "(devel)"}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return t
	}
	if bi.Main.Version != "" {
		t.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			t.Revision = s.Value
		case "vcs.modified":
			t.Modified = s.Value == "true"
		}
	}
	return t
}

// Libs maps each requested module path to the version linked into this binary.
// "std" is reported as the Go toolchain version.
func Libs(mods []string) map[string]string {
	out := map[string]string{}
	want := make(map[string]bool, len(mods))
	for _, m := range mods {
		want[m] = true
		if m == "std" {
			out["std"] = runtime.Version()
		}
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, d := range bi.Deps {
		m := d
		for m.Replace != nil {
			m = m.Replace
		}
		if want[m.Path] {
			out[m.Path] = m.Version
		}
	}
	return out
}

// MachineID is a stable short key for the CPU model on this architecture. It is
// derived from public model information only, so two identical machines share it.
func (e Env) MachineID() string {
	h := xxh3.HashString(e.CPU.Brand + "|" + e.GOARCH + "|" + strconv.Itoa(e.CPU.LogicalCores))
	return strconv.FormatUint(h, 36)[:8]
}

// ShortCPU trims the marketing noise from a CPU brand string for display.
func (e Env) ShortCPU() string {
	s := e.CPU.Brand
	for _, cut := range []string{
		" 16-Core Processor", " 12-Core Processor", " 8-Core Processor",
		" Processor", " CPU", "(R)", "(TM)", "(tm)",
	} {
		s = strings.ReplaceAll(s, cut, "")
	}
	if i := strings.Index(s, " @ "); i > 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}

// ArchLevelNote reports a binary compiled below what the CPU supports. It is
// context for reading the numbers, not a demand to rebuild: GOAMD64 is recorded
// on every run and gates merges, so a mismatch can never go unnoticed.
func (e Env) ArchLevelNote() string {
	if e.GOARCH != "amd64" || e.CPU.X64Level == 0 {
		return ""
	}
	built := 1
	if e.GOAMD64 != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.GOAMD64, "v")); err == nil {
			built = n
		}
	}
	if built >= e.CPU.X64Level {
		return ""
	}
	return "built with GOAMD64=v" + strconv.Itoa(built) +
		" on a v" + strconv.Itoa(e.CPU.X64Level) + " CPU"
}
