# flagconv

Converts feature flag data between JSONL and CSV.

Every flag service seems to export in its own shape. The ones built around
a database cursor tend to dump newline-delimited JSON, one flag object per
line. The moment someone wants to review or bulk-edit those flags in a
spreadsheet, you need CSV instead, and then you need to turn the edited
CSV back into JSONL to feed it to whatever ingests it. This tool does that
conversion in both directions.

The formats:

JSONL, one object per line:

```json
{"key":"new-checkout","enabled":true,"rollout":100,"description":"redesigned checkout flow"}
{"key":"dark-mode","enabled":false,"rollout":0,"description":""}
```

CSV, with a fixed header:

```csv
key,enabled,rollout,description
new-checkout,true,100,redesigned checkout flow
dark-mode,false,0,
```

`rollout` is a percentage from 0 to 100.

## Usage

Build it:

```sh
go build -o flagconv .
```

Convert a JSONL export to CSV for review in a spreadsheet:

```sh
./flagconv -from jsonl -to csv -in flags.jsonl -out flags.csv
```

Convert an edited CSV back to JSONL:

```sh
./flagconv -from csv -to jsonl -in flags.csv -out flags.jsonl
```

Both `-in` and `-out` are optional and default to stdin/stdout, so it
works in a pipeline too:

```sh
cat flags.jsonl | ./flagconv -from jsonl -to csv > flags.csv
```

## Why streaming matters here

A large org can easily have a flag export with hundreds of thousands of
entries once you count every environment and every A/B test variant ever
created. `flagconv` never reads the whole input into a slice before
converting it: JSONL is read one line at a time with a bounded scanner
buffer, and CSV is read one record at a time through `encoding/csv`. Each
flag is decoded, translated, and written before the next one is read, so
memory use stays flat regardless of input size.

## Status

Early. The flag schema currently covers key, enabled, rollout percentage,
and description. See the roadmap for what's missing (targeting rules,
variant payloads, multiple environments per flag).
