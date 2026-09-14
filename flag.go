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
	Variants    []Variant
	Rules       []TargetingRule
}

// Variant is one of the payloads a flag can serve. Payload is left as raw
// JSON because its shape is defined by whatever the flag controls, not by
// this tool.
type Variant struct {
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// TargetingRule routes a subset of evaluations to a specific variant based
// on a single context attribute. Rules are evaluated in order by whatever
// consumes the converted output; this tool only carries them through.
type TargetingRule struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"` // equals, not_equals, in, not_in
	Values    []string `json:"values"`
	Variant   string   `json:"variant"`
}

var validRuleOperators = map[string]bool{
	"equals":     true,
	"not_equals": true,
	"in":         true,
	"not_in":     true,
}

// validateFlag checks the parts of the schema that a single-line JSON
// object or CSV row can't enforce structurally: rollout range, operator
// names, and that rules only point at variants that actually exist.
func validateFlag(f Flag) error {
	if f.Rollout < 0 || f.Rollout > 100 {
		return fmt.Errorf("rollout %d out of range 0-100", f.Rollout)
	}
	variantKeys := make(map[string]bool, len(f.Variants))
	for _, v := range f.Variants {
		if v.Key == "" {
			return fmt.Errorf("variant missing key")
		}
		if variantKeys[v.Key] {
			return fmt.Errorf("duplicate variant key %q", v.Key)
		}
		variantKeys[v.Key] = true
	}
	for _, r := range f.Rules {
		if r.Attribute == "" {
			return fmt.Errorf("targeting rule missing attribute")
		}
		if !validRuleOperators[r.Operator] {
			return fmt.Errorf("targeting rule on %q: unknown operator %q", r.Attribute, r.Operator)
		}
		if len(r.Values) == 0 {
			return fmt.Errorf("targeting rule on %q: no values", r.Attribute)
		}
		if r.Variant == "" {
			return fmt.Errorf("targeting rule on %q: missing variant", r.Attribute)
		}
		if len(variantKeys) > 0 && !variantKeys[r.Variant] {
			return fmt.Errorf("targeting rule on %q: references unknown variant %q", r.Attribute, r.Variant)
		}
	}
	return nil
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
	Key         string          `json:"key"`
	Enabled     bool            `json:"enabled"`
	Rollout     int             `json:"rollout,omitempty"`
	Description string          `json:"description,omitempty"`
	Variants    []Variant       `json:"variants,omitempty"`
	Rules       []TargetingRule `json:"rules,omitempty"`
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
		f := Flag{Key: l.Key, Enabled: l.Enabled, Rollout: l.Rollout, Description: l.Description, Variants: l.Variants, Rules: l.Rules}
		if err := validateFlag(f); err != nil {
			return Flag{}, fmt.Errorf("jsonl line %d: %w", jr.line, err)
		}
		return f, nil
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
	l := jsonlLine{Key: f.Key, Enabled: f.Enabled, Rollout: f.Rollout, Description: f.Description, Variants: f.Variants, Rules: f.Rules}
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
//
// Variants and rules don't fit a flat table, so they ride along as JSON
// text in their own columns. encoding/csv quotes those fields for us since
// they contain commas and quotes, so a spreadsheet round-trips them as long
// as the JSON itself isn't hand-edited into something invalid.

var csvHeader = []string{"key", "enabled", "rollout", "description", "variants", "rules"}

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
	variantsStr, rulesStr := record[4], record[5]
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
	var variants []Variant
	if variantsStr != "" {
		if err := json.Unmarshal([]byte(variantsStr), &variants); err != nil {
			return Flag{}, fmt.Errorf("csv row %q: variants column: %w", key, err)
		}
	}
	var rules []TargetingRule
	if rulesStr != "" {
		if err := json.Unmarshal([]byte(rulesStr), &rules); err != nil {
			return Flag{}, fmt.Errorf("csv row %q: rules column: %w", key, err)
		}
	}
	f := Flag{Key: key, Enabled: enabled, Rollout: rollout, Description: description, Variants: variants, Rules: rules}
	if err := validateFlag(f); err != nil {
		return Flag{}, fmt.Errorf("csv row %q: %w", key, err)
	}
	return f, nil
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
	var variantsStr string
	if len(f.Variants) > 0 {
		b, err := json.Marshal(f.Variants)
		if err != nil {
			return fmt.Errorf("csv row %q: variants column: %w", f.Key, err)
		}
		variantsStr = string(b)
	}
	var rulesStr string
	if len(f.Rules) > 0 {
		b, err := json.Marshal(f.Rules)
		if err != nil {
			return fmt.Errorf("csv row %q: rules column: %w", f.Key, err)
		}
		rulesStr = string(b)
	}
	record := []string{f.Key, strconv.FormatBool(f.Enabled), strconv.Itoa(f.Rollout), f.Description, variantsStr, rulesStr}
	return cw.w.Write(record)
}

func (cw *csvWriterT) Close() error {
	cw.w.Flush()
	return cw.w.Error()
}
