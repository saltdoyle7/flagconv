package main

import (
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
