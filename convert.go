package main

import (
	"fmt"
	"io"
)

// Convert streams flags from r to w, translating from one format to
// another one record at a time. At no point does it hold more than a
// single Flag in memory, so input size is bounded only by disk, not RAM.
func Convert(w io.Writer, r io.Reader, from, to string) error {
	reader, err := newReader(from, r)
	if err != nil {
		return err
	}
	writer, err := newWriter(to, w)
	if err != nil {
		return err
	}

	for {
		f, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			writer.Close()
			return err
		}
		if err := writer.Write(f); err != nil {
			writer.Close()
			return err
		}
	}

	return writer.Close()
}

// Validate reads every record from r without producing any output. Unlike
// Convert, it doesn't stop at the first bad record: each error is written
// to errW and reading continues, so one pass reports every problem in the
// file instead of just the first one. It returns the number of records
// that failed validation.
func Validate(errW io.Writer, r io.Reader, from string) (int, error) {
	reader, err := newReader(from, r)
	if err != nil {
		return 0, err
	}

	bad := 0
	for {
		_, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			bad++
			fmt.Fprintln(errW, err)
			continue
		}
	}
	return bad, nil
}
