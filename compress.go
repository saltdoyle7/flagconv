package main

import (
	"bufio"
	"compress/gzip"
	"io"
	"strings"
)

// gzipMagic is the two-byte header every gzip stream starts with.
var gzipMagic = []byte{0x1f, 0x8b}

// maybeGunzip returns a reader over r's decompressed contents if r starts
// with a gzip header, and a reader over the bytes as-is otherwise. Sniffing
// the header instead of trusting a file extension means piped input works
// too, where there is no name to look at. The returned close function
// releases the gzip state; it does not close r.
func maybeGunzip(r io.Reader) (io.Reader, func() error, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(gzipMagic))
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, nil, err
	}
	if len(head) < len(gzipMagic) || head[0] != gzipMagic[0] || head[1] != gzipMagic[1] {
		return br, func() error { return nil }, nil
	}
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, nil, err
	}
	return zr, zr.Close, nil
}

// maybeGzip wraps w in a gzip writer when enabled is true. The returned
// close function must run before the underlying file is closed so the
// gzip footer gets flushed; with enabled false it does nothing.
func maybeGzip(w io.Writer, enabled bool) (io.Writer, func() error) {
	if !enabled {
		return w, func() error { return nil }
	}
	zw := gzip.NewWriter(w)
	return zw, zw.Close
}

// wantsGzip reports whether an output path asks for compression by name.
func wantsGzip(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".gz")
}
