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
// around. Everything that varies by environment (enabled, rollout,
// variants, rules) lives on Environment instead of on Flag directly,
// because every flag service that matters ships a flag to staging before
// production and the two are rarely configured the same way.
type Flag struct {
	Key          string
	Description  string
	Environments []Environment
}

// Environment is one flag's configuration in one deployment environment
// (e.g. "production", "staging"). Name is service-defined; this tool
// doesn't assume a fixed set of environments.
type Environment struct {
	Name     string          `json:"name"`
	Enabled  bool            `json:"enabled"`
	Rollout  int             `json:"rollout,omitempty"` // percentage, 0-100
	Variants []Variant       `json:"variants,omitempty"`
	Rules    []TargetingRule `json:"rules,omitempty"`
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
// object or CSV row group can't enforce structurally: that a flag has at
// least one environment, rollout range, operator names, and that rules
// only point at variants that actually exist within their own environment.
func validateFlag(f Flag) error {
	if len(f.Environments) == 0 {
		return fmt.Errorf("flag has no environments")
	}
	envNames := make(map[string]bool, len(f.Environments))
	for _, e := range f.Environments {
		if e.Name == "" {
			return fmt.Errorf("environment missing name")
		}
		if envNames[e.Name] {
			return fmt.Errorf("duplicate environment %q", e.Name)
		}
		envNames[e.Name] = true
		if e.Rollout < 0 || e.Rollout > 100 {
			return fmt.Errorf("environment %q: rollout %d out of range 0-100", e.Name, e.Rollout)
		}
		variantKeys := make(map[string]bool, len(e.Variants))
		for _, v := range e.Variants {
			if v.Key == "" {
				return fmt.Errorf("environment %q: variant missing key", e.Name)
			}
			if variantKeys[v.Key] {
				return fmt.Errorf("environment %q: duplicate variant key %q", e.Name, v.Key)
			}
			variantKeys[v.Key] = true
		}
		for _, r := range e.Rules {
			if r.Attribute == "" {
				return fmt.Errorf("environment %q: targeting rule missing attribute", e.Name)
			}
			if !validRuleOperators[r.Operator] {
				return fmt.Errorf("environment %q: targeting rule on %q: unknown operator %q", e.Name, r.Attribute, r.Operator)
			}
			if len(r.Values) == 0 {
				return fmt.Errorf("environment %q: targeting rule on %q: no values", e.Name, r.Attribute)
			}
			if r.Variant == "" {
				return fmt.Errorf("environment %q: targeting rule on %q: missing variant", e.Name, r.Attribute)
			}
			if len(variantKeys) > 0 && !variantKeys[r.Variant] {
				return fmt.Errorf("environment %q: targeting rule on %q: references unknown variant %q", e.Name, r.Attribute, r.Variant)
			}
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
// worth of bytes off the wire. A flag's environments nest inside that same
// line, since JSON has no trouble expressing that directly.

type jsonlLine struct {
	Key          string        `json:"key"`
	Description  string        `json:"description,omitempty"`
	Environments []Environment `json:"environments"`
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
		f := Flag{Key: l.Key, Description: l.Description, Environments: l.Environments}
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
	l := jsonlLine{Key: f.Key, Description: f.Description, Environments: f.Environments}
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
// Environments don't fit a flag-per-row table any better than they fit a
// single JSON object without nesting, so each CSV row is one flag's
// configuration in one environment instead: rows sharing a key are the
// same flag. The reader and writer here are responsible for folding that
// group of rows back into a single Flag and unfolding a Flag's
// environments back out into rows, one CSV record at a time either way.
//
// Variants and rules still don't fit a flat table, so they ride along as
// JSON text in their own columns, same as before.

var csvHeader = []string{"key", "environment", "enabled", "rollout", "description", "variants", "rules"}

type csvReaderT struct {
	r       *csv.Reader
	pending []string // row already read from r that belongs to the next flag
	done    bool
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

// parseCSVRow turns one CSV record into an Environment plus the
// description carried on that row (description is flag-level, but has to
// live somewhere in a flat row, so every row for a flag repeats it).
func parseCSVRow(record []string) (env Environment, description string, err error) {
	name, enabledStr, rolloutStr := record[1], record[2], record[3]
	description = record[4]
	variantsStr, rulesStr := record[5], record[6]
	if name == "" {
		return Environment{}, "", fmt.Errorf("missing environment name")
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return Environment{}, "", fmt.Errorf("environment %q: enabled column: %w", name, err)
	}
	rollout, err := strconv.Atoi(rolloutStr)
	if err != nil {
		return Environment{}, "", fmt.Errorf("environment %q: rollout column: %w", name, err)
	}
	var variants []Variant
	if variantsStr != "" {
		if err := json.Unmarshal([]byte(variantsStr), &variants); err != nil {
			return Environment{}, "", fmt.Errorf("environment %q: variants column: %w", name, err)
		}
	}
	var rules []TargetingRule
	if rulesStr != "" {
		if err := json.Unmarshal([]byte(rulesStr), &rules); err != nil {
			return Environment{}, "", fmt.Errorf("environment %q: rules column: %w", name, err)
		}
	}
	return Environment{Name: name, Enabled: enabled, Rollout: rollout, Variants: variants, Rules: rules}, description, nil
}

// Read folds however many consecutive rows share a key into one Flag. Rows
// for the same key are expected to be contiguous, which is how the CSV
// writer produces them and how a spreadsheet export naturally groups them
// after a sort on the key column; rows for the same key split apart by
// other rows are read back as separate flags instead of being merged.
func (cr *csvReaderT) Read() (Flag, error) {
	row := cr.pending
	cr.pending = nil
	if row == nil {
		if cr.done {
			return Flag{}, io.EOF
		}
		var err error
		row, err = cr.r.Read()
		if err == io.EOF {
			return Flag{}, io.EOF
		}
		if err != nil {
			return Flag{}, fmt.Errorf("csv row: %w", err)
		}
	}

	key := row[0]
	if key == "" {
		return Flag{}, fmt.Errorf("csv row: missing key")
	}
	f := Flag{Key: key}

	for {
		env, description, err := parseCSVRow(row)
		if err != nil {
			return Flag{}, fmt.Errorf("csv row %q: %w", key, err)
		}
		if description != "" {
			if f.Description != "" && f.Description != description {
				return Flag{}, fmt.Errorf("csv row %q: description differs across environment rows", key)
			}
			f.Description = description
		}
		f.Environments = append(f.Environments, env)

		next, err := cr.r.Read()
		if err == io.EOF {
			cr.done = true
			break
		}
		if err != nil {
			return Flag{}, fmt.Errorf("csv row: %w", err)
		}
		if next[0] != key {
			cr.pending = next
			break
		}
		row = next
	}

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
	if len(f.Environments) == 0 {
		return fmt.Errorf("csv row %q: flag has no environments", f.Key)
	}
	for _, e := range f.Environments {
		var variantsStr string
		if len(e.Variants) > 0 {
			b, err := json.Marshal(e.Variants)
			if err != nil {
				return fmt.Errorf("csv row %q: environment %q: variants column: %w", f.Key, e.Name, err)
			}
			variantsStr = string(b)
		}
		var rulesStr string
		if len(e.Rules) > 0 {
			b, err := json.Marshal(e.Rules)
			if err != nil {
				return fmt.Errorf("csv row %q: environment %q: rules column: %w", f.Key, e.Name, err)
			}
			rulesStr = string(b)
		}
		record := []string{f.Key, e.Name, strconv.FormatBool(e.Enabled), strconv.Itoa(e.Rollout), f.Description, variantsStr, rulesStr}
		if err := cw.w.Write(record); err != nil {
			return err
		}
	}
	return nil
}

func (cw *csvWriterT) Close() error {
	cw.w.Flush()
	return cw.w.Error()
}
