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
	gzipOut := flag.Bool("gzip", false, "gzip-compress output (implied when -out ends in .gz)")
	validateOnly := flag.Bool("validate-only", false, "check input for errors and report them without writing output")
	flag.Parse()

	if *from == "" {
		flag.Usage()
		return fmt.Errorf("-from is required")
	}
	if !*validateOnly && *to == "" {
		flag.Usage()
		return fmt.Errorf("-to is required unless -validate-only is set")
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

	src, closeSrc, err := maybeGunzip(in)
	if err != nil {
		return err
	}
	defer closeSrc()

	if *validateOnly {
		bad, err := Validate(os.Stderr, src, *from)
		if err != nil {
			return err
		}
		if bad > 0 {
			return fmt.Errorf("%d record(s) failed validation", bad)
		}
		return nil
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

	dst, closeDst := maybeGzip(out, *gzipOut || wantsGzip(*outPath))
	if err := Convert(dst, src, *from, *to); err != nil {
		closeDst()
		return err
	}
	// The gzip footer is written on close, so a failure here means the
	// output file is truncated and has to be reported.
	return closeDst()
}
