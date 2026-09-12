package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Flag is the in-memory representation shared by every format this tool
// speaks. Formats add or drop fields around this set, not the other way
// around.
type Flag struct {
	Key         string
	Enabled     bool
	Rollout     int // percentage, 0-100
	Description string
}

// FlagReader yields one Flag at a time and returns io.EOF once the input is
// exhausted. Nothing is read ahead beyond what the underlying format needs
// to produce that one record, so callers can process arbitrarily large
// inputs in constant memory.
type FlagReader interface {
	Read() (Flag, error)
}

// FlagWriter accepts one Flag at a time. Close must be called to flush any
// buffered output.
type FlagWriter interface {
	Write(Flag) error
	Close() error
}

func newReader(format string, r io.Reader) (FlagReader, error) {
	switch format {
	case "jsonl":
		return newJSONLReader(r), nil
	case "csv":
		return newCSVReader(r)
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

func newWriter(format string, w io.Writer) (FlagWriter, error) {
	switch format {
	case "jsonl":
		return newJSONLWriter(w), nil
	case "csv":
		return newCSVWriter(w)
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

// --- JSONL ---
//
// One JSON object per line. This is the shape most flag services export
// when they stream results out of a database cursor, so a line-oriented
// scanner is the natural fit: each Scan() call pulls exactly one flag's
// worth of bytes off the wire.

type jsonlLine struct {
	Key         string `json:"key"`
	Enabled     bool   `json:"enabled"`
	Rollout     int    `json:"rollout,omitempty"`
	Description string `json:"description,omitempty"`
}

type jsonlReader struct {
	scanner *bufio.Scanner
	line    int
}

func newJSONLReader(r io.Reader) *jsonlReader {
	s := bufio.NewScanner(r)
	// Descriptions can be long; grow the buffer past the default 64KB
	// token limit instead of failing on a single oversized line.
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &jsonlReader{scanner: s}
}

func (jr *jsonlReader) Read() (Flag, error) {
	for jr.scanner.Scan() {
		jr.line++
		raw := strings.TrimSpace(jr.scanner.Text())
		if raw == "" {
			continue
		}
		var l jsonlLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return Flag{}, fmt.Errorf("jsonl line %d: %w", jr.line, err)
		}
		if l.Key == "" {
			return Flag{}, fmt.Errorf("jsonl line %d: missing key", jr.line)
		}
		return Flag{Key: l.Key, Enabled: l.Enabled, Rollout: l.Rollout, Description: l.Description}, nil
	}
	if err := jr.scanner.Err(); err != nil {
		return Flag{}, fmt.Errorf("jsonl line %d: %w", jr.line+1, err)
	}
	return Flag{}, io.EOF
}

type jsonlWriter struct {
	w *bufio.Writer
}

func newJSONLWriter(w io.Writer) *jsonlWriter {
	return &jsonlWriter{w: bufio.NewWriter(w)}
}

func (jw *jsonlWriter) Write(f Flag) error {
	l := jsonlLine{Key: f.Key, Enabled: f.Enabled, Rollout: f.Rollout, Description: f.Description}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if _, err := jw.w.Write(b); err != nil {
		return err
	}
	return jw.w.WriteByte('\n')
}

func (jw *jsonlWriter) Close() error {
	return jw.w.Flush()
}

// --- CSV ---
//
// A flat table so flags can be reviewed or edited in a spreadsheet.
// encoding/csv already reads and writes one record at a time under the
// hood, so the reader and writer here just add the header contract and
// the string<->Flag conversions.

var csvHeader = []string{"key", "enabled", "rollout", "description"}

type csvReaderT struct {
	r *csv.Reader
}

func newCSVReader(r io.Reader) (*csvReaderT, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = len(csvHeader)
	header, err := cr.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("csv input has no header row")
	}
	if err != nil {
		return nil, fmt.Errorf("csv header: %w", err)
	}
	if len(header) != len(csvHeader) {
		return nil, fmt.Errorf("csv header: want %d columns %v, got %v", len(csvHeader), csvHeader, header)
	}
	for i, name := range csvHeader {
		if header[i] != name {
			return nil, fmt.Errorf("csv header: column %d is %q, want %q", i, header[i], name)
		}
	}
	return &csvReaderT{r: cr}, nil
}

func (cr *csvReaderT) Read() (Flag, error) {
	record, err := cr.r.Read()
	if err != nil {
		return Flag{}, err // io.EOF passes through unchanged
	}
	key, enabledStr, rolloutStr, description := record[0], record[1], record[2], record[3]
	if key == "" {
		return Flag{}, fmt.Errorf("csv row: missing key")
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return Flag{}, fmt.Errorf("csv row %q: enabled column: %w", key, err)
	}
	rollout, err := strconv.Atoi(rolloutStr)
	if err != nil {
		return Flag{}, fmt.Errorf("csv row %q: rollout column: %w", key, err)
	}
	if rollout < 0 || rollout > 100 {
		return Flag{}, fmt.Errorf("csv row %q: rollout %d out of range 0-100", key, rollout)
	}
	return Flag{Key: key, Enabled: enabled, Rollout: rollout, Description: description}, nil
}

type csvWriterT struct {
	w *csv.Writer
}

func newCSVWriter(w io.Writer) (*csvWriterT, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return nil, err
	}
	return &csvWriterT{w: cw}, nil
}

func (cw *csvWriterT) Write(f Flag) error {
	record := []string{f.Key, strconv.FormatBool(f.Enabled), strconv.Itoa(f.Rollout), f.Description}
	return cw.w.Write(record)
}

func (cw *csvWriterT) Close() error {
	cw.w.Flush()
	return cw.w.Error()
}
