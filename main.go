// Command compress-site benchmarks Go compressors and maintains the JSON
// database behind the static comparison site in web/.
package main

import (
	"fmt"
	"os"

	"github.com/klauspost/compress-site/internal/cli"
)

func main() {
	if err := cli.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
