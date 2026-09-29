// Package corpus defines the benchmark test data sets and loads them into memory.
package corpus

import (
	"fmt"

	"github.com/pbnjay/memory"
)

// Kind is a bitmask so it can also express "which kinds does this support".
type Kind uint8

const (
	// KindFile is a single large buffer, benchmarked through streaming APIs.
	KindFile Kind = 1 << iota
	// KindDir is many small buffers, benchmarked through block APIs with reuse.
	KindDir
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindFile | KindDir:
		return "file|dir"
	}
	return "none"
}

// ParseKind is the inverse of Kind.String for the single-kind values.
func ParseKind(s string) (Kind, error) {
	switch s {
	case "file":
		return KindFile, nil
	case "dir":
		return KindDir, nil
	}
	return 0, fmt.Errorf("unknown corpus kind %q", s)
}

// Tier groups corpora by uncompressed size so a default run stays reasonable.
type Tier uint8

const (
	TierSmall  Tier = iota // < 128 MB
	TierMedium             // < 1 GB
	TierLarge              // < 4 GB
	TierHuge               // >= 4 GB
)

var tierNames = [...]string{"small", "medium", "large", "huge"}

func (t Tier) String() string { return tierNames[t] }

// ParseTier is the inverse of Tier.String.
func ParseTier(s string) (Tier, error) {
	for i, n := range tierNames {
		if n == s {
			return Tier(i), nil
		}
	}
	return 0, fmt.Errorf("unknown tier %q", s)
}

func tierFor(size int64) Tier {
	switch {
	case size < 128<<20:
		return TierSmall
	case size < 1<<30:
		return TierMedium
	case size < 4<<30:
		return TierLarge
	}
	return TierHuge
}

// Corpus is a test data set definition. It is static metadata; see Load for the bytes.
type Corpus struct {
	ID    string
	Name  string
	Desc  string
	Kind  Kind
	Tags  []string
	Size  int64 // uncompressed bytes, 0 when only known after extraction
	DL    int64 // download size, used to estimate Size while it is unknown
	Files int   // members, for KindDir

	// Remote is the filename on the download server, "" for synthetic corpora.
	Remote string
	// Sub names the wanted entry when Remote is a multi-entry archive and Kind is KindFile.
	Sub string
	// Local lists plain (already extracted) names accepted from the cache root.
	// Checked before any download, so an existing collection is adopted as-is.
	Local []string
	// Synth fills dst deterministically for corpora that are generated, not downloaded.
	Synth func(dst []byte)
	// Note is shown by "corpus list"/"corpus fetch" for corpora needing manual steps.
	Note string
}

// Bytes is the uncompressed size, estimated from the download when unmeasured.
func (c Corpus) Bytes() int64 {
	if c.Size > 0 {
		return c.Size
	}
	return c.DL * 4
}

func (c Corpus) Tier() Tier { return tierFor(c.Bytes()) }

// URL is the download location, "" when the corpus is synthetic or manual.
func (c Corpus) URL() string {
	if c.Remote == "" {
		return ""
	}
	return BaseURL + c.Remote
}

// Need is the peak resident bytes for benchmarking c. The worst case is a
// store-only level, where the compressed copy is as large as the source and
// the scratch buffer is live alongside it.
func (c Corpus) Need() int64 { n := c.Bytes(); return n*3 + n/2 }

// CheckMem reports whether c fits in the memory budget.
func (c Corpus) CheckMem() error {
	total := int64(memory.TotalMemory())
	if total <= 0 {
		return nil
	}
	budget := total / 5 * 3
	if need := c.Need(); need > budget {
		return fmt.Errorf("corpus %s needs ~%s, system has %s, 60%% budget is %s; use -force to override",
			c.ID, iec(need), iec(total), iec(budget))
	}
	return nil
}

// File is one member of a KindDir corpus.
type File struct {
	Name string
	Data []byte
	Hash uint64
}

// Data is a corpus loaded into memory.
type Data struct {
	Corpus
	File  []byte // KindFile
	Files []File // KindDir, sorted by Name for deterministic iteration
	Total int64
	Hash  uint64 // xxh3 of File, or of the members in sorted order
	Max   int    // largest member, for sizing encode destinations
}

func iec(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
