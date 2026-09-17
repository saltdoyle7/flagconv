# flagconv

Converts feature flag data between JSONL and CSV.

Every flag service seems to export in its own shape. The ones built around
a database cursor tend to dump newline-delimited JSON, one flag object per
line. The moment someone wants to review or bulk-edit those flags in a
spreadsheet, you need CSV instead, and then you need to turn the edited
CSV back into JSONL to feed it to whatever ingests it. This tool does that
conversion in both directions.

A flag's `enabled`, `rollout`, `variants`, and `rules` are all set per
environment, since a flag is rarely configured the same way in staging and
production. `key` and `description` are the only things shared across a
flag's environments.

The formats:

JSONL, one object per line, environments nested inside:

```json
{"key":"new-checkout","description":"redesigned checkout flow","environments":[{"name":"staging","enabled":true,"rollout":100},{"name":"production","enabled":true,"rollout":50}]}
{"key":"dark-mode","description":"","environments":[{"name":"staging","enabled":false,"rollout":0},{"name":"production","enabled":false,"rollout":0}]}
```

CSV, with a fixed header, one row per flag per environment:

```csv
key,environment,enabled,rollout,description,variants,rules
new-checkout,staging,true,100,redesigned checkout flow,,
new-checkout,production,true,50,redesigned checkout flow,,
dark-mode,staging,false,0,,,
dark-mode,production,false,0,,,
```

Rows for the same flag must be contiguous - the CSV reader folds them back
into one flag by watching for the key to change, the same way it writes
them out. `rollout` is a percentage from 0 to 100.

An environment can also carry variant payloads and targeting rules:

```json
{
  "key": "checkout-button-color",
  "environments": [
    {
      "name": "production",
      "enabled": true,
      "rollout": 100,
      "variants": [
        {"key": "control", "payload": {"color": "blue"}},
        {"key": "treatment", "payload": {"color": "green"}}
      ],
      "rules": [
        {"attribute": "country", "operator": "in", "values": ["CA", "US"], "variant": "treatment"}
      ]
    }
  ]
}
```

`variants` holds arbitrary JSON payloads per variant key. `rules` route a
context attribute to a variant using one of `equals`, `not_equals`, `in`,
`not_in`; each rule's `variant` must match a key in that environment's
`variants`. CSV has no way to nest data, so the same information rides
along as JSON text in the `variants` and `rules` columns instead of being
split into their own rows.

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

Early. The flag schema covers key, description, and a list of
environments each with enabled, rollout percentage, variant payloads, and
targeting rules. Still missing: table-driven edge case tests, a
validate-only mode, gzip support, and a YAML target.
