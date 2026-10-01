package bench

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Expand crosses variants with the window sweep. A window the encoder does not
// list is dropped rather than clamped, so one sweep can drive every codec
// without measuring a clamped setting twice.
//
// largest is the biggest single input the encoder will be handed, or 0 when
// that is not known yet. Once a window holds the whole of it, every larger one
// does too and they cannot differ, so only the smallest such window is kept.
// The default is always kept: it is the baseline every other window is read
// against. A size equal to the default is the default, stored as 0, so one
// setting is never two rows.
func (c Config) Expand(vs []Variant, largest int) []Variant {
	ws := slices.Compact(slices.Sorted(slices.Values(c.Windows)))
	if len(ws) == 0 {
		ws = []int{0}
	}
	var out []Variant
	for _, v := range vs {
		covered, def := false, false
		for _, w := range ws {
			if !v.Enc.AcceptsWindow(w) {
				continue
			}
			if w > 0 && largest > 0 {
				if covered {
					continue
				}
				covered = w >= largest
			}
			if w == v.Enc.DefaultWindow(v.Level.N) {
				w = 0
			}
			if w == 0 {
				if def {
					continue
				}
				def = true
			}
			v.Window = w
			out = append(out, v)
		}
	}
	return out
}

// ParseWindows reads a -window list: "default" and sizes such as 8K, 64KB or
// 1M, in binary units. Every codec takes powers of two only, so anything else
// is a typo rather than a request to skip.
func ParseWindows(spec string) ([]int, error) {
	var out []int
	for _, s := range strings.Split(spec, ",") {
		s = strings.TrimSpace(s)
		switch {
		case s == "":
			continue
		case strings.EqualFold(s, "default"):
			out = append(out, 0)
			continue
		}
		n, err := parseSize(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-window selected nothing")
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func parseSize(s string) (int, error) {
	num := strings.TrimSuffix(strings.ToUpper(s), "B")
	shift := 0
	if i := strings.IndexAny(num, "KMG"); i == len(num)-1 && i > 0 {
		shift = map[byte]int{'K': 10, 'M': 20, 'G': 30}[num[i]]
		num = num[:i]
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, fmt.Errorf("window %q: want default or a size such as 64K", s)
	}
	n <<= shift
	if n <= 0 || n&(n-1) != 0 {
		return 0, fmt.Errorf("window %q is not a power of two", s)
	}
	return n, nil
}

// WindowName is the -window spelling of one size.
func WindowName(n int) string {
	for _, u := range []struct {
		shift int
		unit  string
	}{{30, "G"}, {20, "M"}, {10, "K"}} {
		if n >= 1<<u.shift && n%(1<<u.shift) == 0 {
			return strconv.Itoa(n>>u.shift) + u.unit
		}
	}
	if n == 0 {
		return "default"
	}
	return strconv.Itoa(n)
}

// WindowSpec renders a sweep back into the syntax ParseWindows reads.
func WindowSpec(ws []int) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = WindowName(w)
	}
	return strings.Join(parts, ",")
}
