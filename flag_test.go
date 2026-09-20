package main

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

// readAllFlags drains a FlagReader, returning every flag read before EOF or
// the first non-EOF error.
func readAllFlags(fr FlagReader) ([]Flag, error) {
	var got []Flag
	for {
		f, err := fr.Read()
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		got = append(got, f)
	}
}

func TestValidateFlag(t *testing.T) {
	cases := []struct {
		name    string
		flag    Flag
		wantErr string // substring expected in the error; empty means no error
	}{
		{
			name: "valid single environment",
			flag: Flag{Key: "k", Environments: []Environment{{Name: "production", Enabled: true, Rollout: 50}}},
		},
		{
			name:    "no environments",
			flag:    Flag{Key: "k"},
			wantErr: "no environments",
		},
		{
			name:    "environment missing name",
			flag:    Flag{Key: "k", Environments: []Environment{{Enabled: true}}},
			wantErr: "missing name",
		},
		{
			name: "duplicate environment name",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production"}, {Name: "production"},
			}},
			wantErr: "duplicate environment",
		},
		{
			name:    "rollout below range",
			flag:    Flag{Key: "k", Environments: []Environment{{Name: "production", Rollout: -1}}},
			wantErr: "out of range",
		},
		{
			name:    "rollout above range",
			flag:    Flag{Key: "k", Environments: []Environment{{Name: "production", Rollout: 101}}},
			wantErr: "out of range",
		},
		{
			name: "variant missing key",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Variants: []Variant{{}}},
			}},
			wantErr: "variant missing key",
		},
		{
			name: "duplicate variant key",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Variants: []Variant{{Key: "a"}, {Key: "a"}}},
			}},
			wantErr: "duplicate variant key",
		},
		{
			name: "rule missing attribute",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Rules: []TargetingRule{{Operator: "equals", Values: []string{"x"}, Variant: "a"}}},
			}},
			wantErr: "missing attribute",
		},
		{
			name: "rule unknown operator",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Rules: []TargetingRule{{Attribute: "country", Operator: "matches", Values: []string{"x"}, Variant: "a"}}},
			}},
			wantErr: "unknown operator",
		},
		{
			name: "rule no values",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Rules: []TargetingRule{{Attribute: "country", Operator: "equals", Variant: "a"}}},
			}},
			wantErr: "no values",
		},
		{
			name: "rule missing variant",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Rules: []TargetingRule{{Attribute: "country", Operator: "equals", Values: []string{"x"}}}},
			}},
			wantErr: "missing variant",
		},
		{
			name: "rule references unknown variant",
			flag: Flag{Key: "k", Environments: []Environment{
				{
					Name:     "production",
					Variants: []Variant{{Key: "a"}},
					Rules:    []TargetingRule{{Attribute: "country", Operator: "equals", Values: []string{"x"}, Variant: "b"}},
				},
			}},
			wantErr: "unknown variant",
		},
		{
			name: "rule variant matches a declared variant",
			flag: Flag{Key: "k", Environments: []Environment{
				{
					Name:     "production",
					Variants: []Variant{{Key: "a"}},
					Rules:    []TargetingRule{{Attribute: "country", Operator: "equals", Values: []string{"x"}, Variant: "a"}},
				},
			}},
		},
		{
			// No variants declared means there's nothing to check a rule's
			// variant against, so it's allowed through rather than rejected.
			name: "rule allowed when environment declares no variants",
			flag: Flag{Key: "k", Environments: []Environment{
				{Name: "production", Rules: []TargetingRule{{Attribute: "country", Operator: "equals", Values: []string{"x"}, Variant: "a"}}},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFlag(tc.flag)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestJSONLReader(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    []Flag
		wantErr string
	}{
		{
			name: "reads multiple lines and skips blank lines",
			input: "\n" +
				`{"key":"new-checkout","description":"redesigned checkout","environments":[{"name":"staging","enabled":true,"rollout":100},{"name":"production","enabled":true,"rollout":50}]}` + "\n" +
				"\n" +
				`{"key":"dark-mode","environments":[{"name":"staging","enabled":false,"rollout":0}]}` + "\n" +
				"\n",
			want: []Flag{
				{
					Key:         "new-checkout",
					Description: "redesigned checkout",
					Environments: []Environment{
						{Name: "staging", Enabled: true, Rollout: 100},
						{Name: "production", Enabled: true, Rollout: 50},
					},
				},
				{
					Key:          "dark-mode",
					Environments: []Environment{{Name: "staging", Enabled: false, Rollout: 0}},
				},
			},
		},
		{
			name:    "invalid json",
			input:   "not json\n",
			wantErr: "jsonl line 1",
		},
		{
			name:    "missing key",
			input:   `{"environments":[{"name":"production","enabled":true,"rollout":0}]}` + "\n",
			wantErr: "missing key",
		},
		{
			name:    "validation error is propagated with line number",
			input:   `{"key":"k","environments":[]}` + "\n",
			wantErr: "jsonl line 1: flag has no environments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAllFlags(newJSONLReader(strings.NewReader(tc.input)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func readAllCSVFlags(input string) ([]Flag, error) {
	r, err := newCSVReader(strings.NewReader(input))
	if err != nil {
		return nil, err
	}
	return readAllFlags(r)
}

func TestCSVReader(t *testing.T) {
	header := strings.Join(csvHeader, ",") + "\n"

	cases := []struct {
		name    string
		input   string
		want    []Flag
		wantErr string
	}{
		{
			name:    "empty input has no header row",
			input:   "",
			wantErr: "no header row",
		},
		{
			name:    "wrong number of header columns",
			input:   "key,environment\n",
			wantErr: "csv header",
		},
		{
			name:    "wrong header column name",
			input:   "id,environment,enabled,rollout,description,variants,rules\n",
			wantErr: "csv header: column 0",
		},
		{
			name:  "contiguous rows for the same key fold into one flag",
			input: header + "flag-a,staging,true,100,desc,,\n" + "flag-a,production,false,0,desc,,\n",
			want: []Flag{
				{
					Key:         "flag-a",
					Description: "desc",
					Environments: []Environment{
						{Name: "staging", Enabled: true, Rollout: 100},
						{Name: "production", Enabled: false, Rollout: 0},
					},
				},
			},
		},
		{
			// Documents the folding rule: rows for the same key that aren't
			// adjacent are read back as separate flags instead of merged.
			name: "rows for the same key split by another key are read as separate flags",
			input: header +
				"flag-a,staging,true,100,,,\n" +
				"flag-b,staging,true,0,,,\n" +
				"flag-a,production,true,50,,,\n",
			want: []Flag{
				{Key: "flag-a", Environments: []Environment{{Name: "staging", Enabled: true, Rollout: 100}}},
				{Key: "flag-b", Environments: []Environment{{Name: "staging", Enabled: true, Rollout: 0}}},
				{Key: "flag-a", Environments: []Environment{{Name: "production", Enabled: true, Rollout: 50}}},
			},
		},
		{
			name:    "missing key",
			input:   header + ",staging,true,0,,,\n",
			wantErr: "missing key",
		},
		{
			name:    "invalid enabled column",
			input:   header + "flag-a,staging,notabool,0,,,\n",
			wantErr: "enabled column",
		},
		{
			name:    "invalid rollout column",
			input:   header + "flag-a,staging,true,notanumber,,,\n",
			wantErr: "rollout column",
		},
		{
			name:    "invalid variants column",
			input:   header + "flag-a,staging,true,0,,not-json,\n",
			wantErr: "variants column",
		},
		{
			name:    "invalid rules column",
			input:   header + "flag-a,staging,true,0,,,not-json\n",
			wantErr: "rules column",
		},
		{
			name:    "description differs across environment rows",
			input:   header + "flag-a,staging,true,0,desc one,,\n" + "flag-a,production,true,0,desc two,,\n",
			wantErr: "description differs",
		},
		{
			name:    "validation error is propagated",
			input:   header + "flag-a,staging,true,200,,,\n",
			wantErr: "out of range",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAllCSVFlags(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCSVWriterRejectsFlagWithNoEnvironments(t *testing.T) {
	var buf bytes.Buffer
	w, err := newCSVWriter(&buf)
	if err != nil {
		t.Fatalf("newCSVWriter: %v", err)
	}
	err = w.Write(Flag{Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "no environments") {
		t.Fatalf("got error %v, want one containing %q", err, "no environments")
	}
}

func TestJSONLWriteReadRoundTrip(t *testing.T) {
	in := Flag{
		Key: "checkout-button-color",
		Environments: []Environment{
			{
				Name:    "production",
				Enabled: true,
				Rollout: 100,
				Variants: []Variant{
					{Key: "control", Payload: json.RawMessage(`{"color":"blue"}`)},
					{Key: "treatment", Payload: json.RawMessage(`{"color":"green"}`)},
				},
				Rules: []TargetingRule{
					{Attribute: "country", Operator: "in", Values: []string{"CA", "US"}, Variant: "treatment"},
				},
			},
		},
	}

	var buf bytes.Buffer
	w := newJSONLWriter(&buf)
	if err := w.Write(in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := newJSONLReader(&buf)
	got, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := r.Read(); err != io.EOF {
		t.Fatalf("expected EOF after one record, got %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, in)
	}
}

func TestCSVWriteReadRoundTrip(t *testing.T) {
	in := Flag{
		Key:         "checkout-button-color",
		Description: "changes the primary button color",
		Environments: []Environment{
			{Name: "staging", Enabled: true, Rollout: 100},
			{
				Name:     "production",
				Enabled:  true,
				Rollout:  50,
				Variants: []Variant{{Key: "control", Payload: json.RawMessage(`{"color":"blue"}`)}},
				Rules:    []TargetingRule{{Attribute: "country", Operator: "equals", Values: []string{"US"}, Variant: "control"}},
			},
		},
	}

	var buf bytes.Buffer
	w, err := newCSVWriter(&buf)
	if err != nil {
		t.Fatalf("newCSVWriter: %v", err)
	}
	if err := w.Write(in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := newCSVReader(&buf)
	if err != nil {
		t.Fatalf("newCSVReader: %v", err)
	}
	got, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := r.Read(); err != io.EOF {
		t.Fatalf("expected EOF after one record, got %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, in)
	}
}
