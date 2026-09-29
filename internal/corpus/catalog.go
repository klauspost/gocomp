package corpus

import (
	"fmt"
	"sort"
)

// BaseURL hosts the downloadable corpora.
const BaseURL = "https://klauspost.com/files/compress/"

// Sizes below are exact uncompressed byte counts where they have been measured.
// Where Size is 0 the real size is only known after the first extraction; Tier
// estimates from the download size until then, and Load records the measured
// value in the cache index.
var catalog = []Corpus{
	// --- single file ---
	{
		ID: "stacktrace", Name: "stacktrace.txt", Desc: "Go panic stack traces",
		Kind: KindFile, Tags: []string{"text", "log"},
		Remote: "stacktrace.txt", DL: 5_308_216, Size: 5_308_216,
		Local: []string{"stacktrace.txt"},
	},
	{
		ID: "enwik8", Name: "enwik8", Desc: "First 10^8 bytes of English Wikipedia XML",
		Kind: KindFile, Tags: []string{"text", "xml"},
		Remote: "enwik8.zst", DL: 26_960_000, Size: 100_000_000,
		Local: []string{"enwik8"},
	},
	{
		ID: "silesia", Name: "silesia.tar", Desc: "Silesia compression corpus, tarred",
		Kind: KindFile, Tags: []string{"mixed"},
		Remote: "silesia.tar.zst", DL: 66_726_655, Size: 211_947_520,
		Local: []string{"silesia.tar"},
		// Upstream ships the 12 files loose, so "silesia.tar" is a packing
		// choice rather than a canonical artifact. This is the one now hosted;
		// a tar built differently hashes differently, and the merge gate will
		// hold those runs apart instead of averaging them together.
		Note: "packed from https://sun.aei.polsl.pl//~sdeor/corpus/silesia.zip",
	},
	{
		ID: "parking-citations", Name: "parking-citations-1M.json", Desc: "1M LA parking citations",
		Kind: KindFile, Tags: []string{"json"},
		Remote: "parking-citations-1M.json.zst", DL: 19_300_000, Size: 365_489_362,
		Local: []string{"parking-citations-1M.json"},
	},
	{
		ID: "ts40", Name: "TS40.txt", Desc: "Time series text",
		Kind: KindFile, Tags: []string{"text", "numeric"},
		Remote: "TS40.txt.zst", DL: 109_500_000, Size: 400_000_000,
		Local: []string{"TS40.txt"},
	},
	{
		ID: "reddit", Name: "RC_2009-01.json", Desc: "Reddit comments, January 2009",
		Kind: KindFile, Tags: []string{"json"},
		Remote: "RC_2009-01.json.zst", DL: 94_300_000,
		Local: []string{"RC_2009-01.json"},
	},
	{
		ID: "enwik9", Name: "enwik9", Desc: "First 10^9 bytes of English Wikipedia XML",
		Kind: KindFile, Tags: []string{"text", "xml"},
		Remote: "enwik9.zst", DL: 235_698_881, Size: 1_000_000_000,
		Local: []string{"enwik9"},
	},
	{
		ID: "glove", Name: "glove.6B.300d.txt", Desc: "GloVe word vectors, 300 dimensions",
		Kind: KindFile, Tags: []string{"text", "numeric"},
		Remote: "glove.6B.300d.txt.zst", DL: 335_200_000, Size: 1_037_962_819,
		Local: []string{"glove.6B.300d.txt"},
	},
	{
		ID: "coronavirus", Name: "coronavirus.unwrapped.fasta",
		Desc: "SARS-CoV-2 genomes, FASTA. Extremely redundant: 1.8MB download, 1.3GB payload",
		Kind: KindFile, Tags: []string{"text", "genomic"},
		Remote: "coronavirus.unwrapped.fasta.zst", DL: 1_908_000, Size: 1_317_937_667,
		Local: []string{"coronavirus.unwrapped.fasta"},
	},
	{
		ID: "github-ranks", Name: "github-ranks-backup.bin", Desc: "GitHub ranking database dump",
		Kind: KindFile, Tags: []string{"binary"},
		Remote: "github-ranks-backup.bin.zst", DL: 346_192_536, Size: 1_862_623_243,
		Local: []string{"github-ranks-backup.bin"},
	},
	{
		ID: "gob-stream", Name: "gob-stream", Desc: "Go gob-encoded object stream",
		Kind: KindFile, Tags: []string{"binary", "structured"},
		Remote: "gob-stream.zst", DL: 127_166_673, Size: 1_911_399_616,
		Local: []string{"gob-stream"},
	},
	{
		ID: "rand2gb", Name: "random 2GB",
		Desc: "Incompressible PCG output, generated locally from seed 0xdeadbeef",
		Kind: KindFile, Tags: []string{"incompressible", "synthetic"},
		Size: 2_147_483_647, Synth: Rand,
		// Deliberately no Local entry: always generating keeps the bytes
		// identical across machines, which an existing random file would not.
	},
	{
		ID: "apache-log", Name: "apache.log", Desc: "Apache access log",
		Kind: KindFile, Tags: []string{"text", "log"},
		Remote: "apache.log.zst", DL: 92_752_896, Size: 2_622_574_440,
		Local: []string{"apache.log"},
	},
	{
		ID: "cockroach-log", Name: "cockroach.node1.log", Desc: "CockroachDB node log",
		Kind: KindFile, Tags: []string{"text", "log"},
		Remote: "cockroach.node1.log.zst", DL: 291_400_000,
		Local: []string{"cockroach.node1.log"},
	},
	{
		ID: "nyc-taxi", Name: "nyc-taxi-data-10M.csv", Desc: "10M New York taxi trips",
		Kind: KindFile, Tags: []string{"csv", "numeric"},
		Remote: "nyc-taxi-data-10M.csv.zst", DL: 405_300_000, Size: 3_325_605_752,
		Local: []string{"nyc-taxi-data-10M.csv"},
	},
	{
		ID: "github-june", Name: "github-june-2days-2019.json", Desc: "Two days of GitHub events",
		Kind: KindFile, Tags: []string{"json"},
		Remote: "github-june-2days-2019.json.zst", DL: 460_500_000, Size: 6_273_951_764,
		Local: []string{"github-june-2days-2019.json"},
	},
	{
		ID: "adresser", Name: "adresser.json", Desc: "Danish address database",
		Kind: KindFile, Tags: []string{"json"},
		Remote: "adresser.json.zst", DL: 163_668_581, Size: 7_983_034_785,
		Local: []string{"adresser.json"},
	},
	{
		ID: "rawstudio", Name: "rawstudio-mint14.tar", Desc: "Rawstudio Mint 14 image set, tarred",
		Kind: KindFile, Tags: []string{"binary", "images"},
		Remote: "rawstudio-mint14.tar.zst", DL: 2_843_597_635, Size: 8_558_382_592,
		Local: []string{"rawstudio-mint14.tar"},
	},
	{
		ID: "enwik10", Name: "enwik10", Desc: "First 10^10 bytes of English Wikipedia XML",
		Kind: KindFile, Tags: []string{"text", "xml"},
		Remote: "enwik10.7z", DL: 1_872_400_000, Size: 10_000_000_000,
		Local: []string{"enwik10"},
	},
	{
		ID: "10gb-tar", Name: "10gb.tar", Desc: "Mixed 10GB file archive, tarred",
		Kind: KindFile, Tags: []string{"mixed"},
		Remote: "10gb.tar.zst", DL: 3_814_100_000, Size: 10_065_157_632,
		Local: []string{"10gb.tar"},
	},
	{
		ID: "consensus-db", Name: "consensus.db.10gb", Desc: "Consensus database snapshot",
		Kind: KindFile, Tags: []string{"binary", "database"},
		Remote: "consensus.db.10gb.zst", DL: 3_408_000_000, Size: 10_737_418_240,
		Local: []string{"consensus.db.10gb"},
	},
	{
		ID: "paranoid-passwords", Name: "klauspost-paranoid-passwords", Desc: "Generated password list",
		Kind: KindFile, Tags: []string{"text"},
		Remote: "klauspost-paranoid-passwords.xz", DL: 2_654_200_000,
		Local: []string{"klauspost-paranoid-passwords"},
	},
	{
		ID: "sofia-air", Name: "sofia-air-quality-dataset.tar", Desc: "Sofia air quality sensor data, tarred",
		Kind: KindFile, Tags: []string{"csv", "numeric"},
		Remote: "sofia-air-quality-dataset.tar.zst", DL: 2_378_900_000, Size: 15_464_463_872,
		Local: []string{"sofia-air-quality-dataset.tar"},
	},

	// --- directories ---
	{
		ID: "gofiles", Name: "gofiles", Desc: "Go source files",
		Kind: KindDir, Tags: []string{"text", "source"},
		Remote: "gofiles.7z", DL: 5_293_370, Size: 65_888_228, Files: 5_186,
		Local: []string{"gofiles"},
	},
	{
		ID: "zstd-dict-tests", Name: "zstd-dict-tests", Desc: "Small files from the zstd dictionary test set",
		Kind: KindDir, Tags: []string{"mixed", "small-files"},
		Remote: "zstd-dict-tests.zip", DL: 18_050_000,
		Local: []string{"zstd-dict-tests"},
	},
	{
		ID: "objectfiles", Name: "objectfiles", Desc: "Compiled Go object files",
		Kind: KindDir, Tags: []string{"binary", "source"},
		Remote: "objectfiles.7z", DL: 24_680_000, Size: 300_491_980, Files: 708,
		Local: []string{"objectfiles"},
	},
	{
		ID: "webdevdata", Name: "webdevdata.org 2015-01-07 subset", Desc: "Crawled web pages, 4GB subset",
		Kind: KindDir, Tags: []string{"html", "web", "small-files"},
		Remote: "webdevdata.org-2015-01-07-4GB-subset.7z", DL: 502_592_109,
		Size: 4_016_000_000, Files: 53_926,
		Local: []string{
			"webdevdata.org-2015-01-07-subset",
			"webdevdata.org-2015-01-07-4GB-subset",
		},
	},
	{
		ID: "zstd-10kfiles", Name: "zstd-10kfiles", Desc: "10,000 small files from the zstd test set",
		Kind: KindDir, Tags: []string{"mixed", "small-files"},
		Remote: "zstd-10kfiles.zip", DL: 878_400_000, Files: 10_000,
		Local: []string{"zstd-10kfiles"},
	},
}

// All returns the catalog in tier then id order.
func All() []Corpus {
	out := append([]Corpus(nil), catalog...)
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := out[i].Tier(), out[j].Tier(); ti != tj {
			return ti < tj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Lookup finds a corpus by id.
func Lookup(id string) (Corpus, bool) {
	for _, c := range catalog {
		if c.ID == id {
			return c, true
		}
	}
	return Corpus{}, false
}

// Select resolves a comma-separated list of ids, tier names, "all", or a kind
// name ("file"/"dir") into corpora.
func Select(spec []string) ([]Corpus, error) {
	var out []Corpus
	seen := map[string]bool{}
	add := func(c Corpus) {
		if !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	for _, s := range spec {
		switch s {
		case "all":
			for _, c := range All() {
				add(c)
			}
			continue
		case "file", "dir":
			k, _ := ParseKind(s)
			for _, c := range All() {
				if c.Kind == k {
					add(c)
				}
			}
			continue
		}
		if t, err := ParseTier(s); err == nil {
			for _, c := range All() {
				if c.Tier() == t {
					add(c)
				}
			}
			continue
		}
		c, ok := Lookup(s)
		if !ok {
			return nil, fmt.Errorf("unknown corpus %q", s)
		}
		add(c)
	}
	return out, nil
}
