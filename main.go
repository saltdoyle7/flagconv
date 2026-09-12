// Command flagconv converts feature flag exports between JSONL and CSV
// without loading the whole file into memory.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "flagconv:", err)
		os.Exit(1)
	}
}

func run() error {
	from := flag.String("from", "", "input format: jsonl or csv")
	to := flag.String("to", "", "output format: jsonl or csv")
	inPath := flag.String("in", "", "input file (default stdin)")
	outPath := flag.String("out", "", "output file (default stdout)")
	flag.Parse()

	if *from == "" || *to == "" {
		flag.Usage()
		return fmt.Errorf("both -from and -to are required")
	}

	in := os.Stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}

	out := os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}

	return Convert(out, in, *from, *to)
}
