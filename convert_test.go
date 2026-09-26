package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name         string
		from         string
		input        string
		wantBad      int
		wantErrText  string // substring expected in errW output
		wantSetupErr string
	}{
		{
			name:  "all records valid",
			from:  "jsonl",
			input: `{"key":"a","environments":[{"name":"production","enabled":true,"rollout":100}]}` + "\n",
		},
		{
			name: "one bad record among good ones is reported and reading continues",
			from: "jsonl",
			input: `{"key":"a","environments":[{"name":"production","enabled":true,"rollout":100}]}` + "\n" +
				`{"key":"b","environments":[]}` + "\n" +
				`{"key":"c","environments":[{"name":"production","enabled":true,"rollout":50}]}` + "\n",
			wantBad:     1,
			wantErrText: "flag has no environments",
		},
		{
			name:         "unknown format is a setup error, not a per-record one",
			from:         "yaml",
			input:        "",
			wantSetupErr: "unknown format",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errBuf bytes.Buffer
			bad, err := Validate(&errBuf, strings.NewReader(tc.input), tc.from)
			if tc.wantSetupErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantSetupErr) {
					t.Fatalf("got error %v, want one containing %q", err, tc.wantSetupErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bad != tc.wantBad {
				t.Fatalf("got %d bad records, want %d", bad, tc.wantBad)
			}
			if tc.wantErrText != "" && !strings.Contains(errBuf.String(), tc.wantErrText) {
				t.Fatalf("errW output %q does not contain %q", errBuf.String(), tc.wantErrText)
			}
		})
	}
}
