package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMaybeGunzip(t *testing.T) {
	const text = "hello\nworld\n"
	tests := []struct {
		name string
		in   []byte
	}{
		{"plain", []byte(text)},
		{"gzip", gzipBytes(t, text)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, closeFn, err := maybeGunzip(bytes.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			defer closeFn()
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != text {
				t.Errorf("got %q, want %q", got, text)
			}
		})
	}
}

func TestMaybeGunzipShortInput(t *testing.T) {
	for _, in := range []string{"", "x"} {
		r, closeFn, err := maybeGunzip(strings.NewReader(in))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(got) != in {
			t.Errorf("got %q, want %q", got, in)
		}
		closeFn()
	}
}

func TestMaybeGunzipTruncatedHeader(t *testing.T) {
	data := gzipBytes(t, "some text")[:5]
	r, _, err := maybeGunzip(bytes.NewReader(data))
	if err == nil {
		_, err = io.ReadAll(r)
	}
	if err == nil {
		t.Error("expected an error for truncated gzip data")
	}
}

func TestMaybeGzipRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, closeFn := maybeGzip(&buf, true)
	if _, err := io.WriteString(w, "payload"); err != nil {
		t.Fatal(err)
	}
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), gzipMagic) {
		t.Fatal("output is not gzip")
	}
	r, rc, err := maybeGunzip(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer rc()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Errorf("got %q", got)
	}
}

func TestMaybeGzipDisabled(t *testing.T) {
	var buf bytes.Buffer
	w, closeFn := maybeGzip(&buf, false)
	io.WriteString(w, "raw")
	closeFn()
	if buf.String() != "raw" {
		t.Errorf("got %q, want raw", buf.String())
	}
}

func TestConvertGzipThrough(t *testing.T) {
	in := `{"key":"a","description":"d","environments":[{"name":"prod","enabled":true,"rollout":10}]}` + "\n"
	r, closeIn, err := maybeGunzip(bytes.NewReader(gzipBytes(t, in)))
	if err != nil {
		t.Fatal(err)
	}
	defer closeIn()

	var out bytes.Buffer
	w, closeOut := maybeGzip(&out, true)
	if err := Convert(w, r, "jsonl", "csv"); err != nil {
		t.Fatal(err)
	}
	if err := closeOut(); err != nil {
		t.Fatal(err)
	}

	zr, err := gzip.NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "key,environment,enabled,rollout,") {
		t.Errorf("unexpected csv output: %q", got)
	}
}

func TestWantsGzip(t *testing.T) {
	for path, want := range map[string]bool{
		"flags.csv.gz":   true,
		"FLAGS.JSONL.GZ": true,
		"flags.csv":      false,
		"":               false,
	} {
		if got := wantsGzip(path); got != want {
			t.Errorf("wantsGzip(%q) = %v, want %v", path, got, want)
		}
	}
}
