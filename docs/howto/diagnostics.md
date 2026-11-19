# Location-aware SQL diagnostics

`sqlc generate` and `sqlc compile` can append a precise diagnostic after the
existing one-line error. The feature is off by default. Enable it with
`--diagnostics`:

```sh
sqlc generate --diagnostics
```

The legacy line is unchanged and still comes first:

```text
query.sql:2:8: column "usr_id" does not exist
  location: query.sql:2:8 (byte 30-36)
  snippet: SELECT usr_id FROM users WHERE id = $1;
                  ^^^^^^
  suggestion: did you mean "user_id"? (edit distance 1)
  related: schema.sql:1:1: table "users" is defined here
```

Positions are computed from parser byte offsets. A column in the diagnostic is
a 1-based byte column. The appended `byte N-M` range is 0-based and can be
passed directly to an editor or language-server API.

## Machine-readable output

Use JSON to emit one object per diagnostic after the legacy line:

```sh
sqlc generate --diagnostics-format=json
```

The same setting is available through `SQLC_DIAGNOSTICS`:

```sh
SQLC_DIAGNOSTICS=1 sqlc generate
SQLC_DIAGNOSTICS=json sqlc generate
```

The JSON object contains `file`, `line`, `column`, `byte_offset`,
`end_byte_offset`, `has_location`, `kind`, `message`, `snippet`, ranked
`suggestions`, ambiguity `candidates`, and cross-file `related` locations.

## Behavior

- Suggestions use Levenshtein distance, are sorted by distance and spelling,
  and are limited to three. Ties are deterministic across runs.
- An unqualified table that exists in exactly one schema is resolved to that
  schema. If it exists in more than one schema, the query fails and all
  qualified candidates are listed instead of choosing one silently.
- Column, relation, function, and view errors are located through the parsed
  AST. Comments and string literals are never searched for an error location.
- Errors on views include the view definition location from the schema file.
  References to a dropped relation include the `DROP TABLE` location.
- Every collected error is printed. One syntax error no longer hides later
  files or statements.
- Errors without a parser or analysis location, such as code generation
  failures, say `no location information` instead of inventing a line.
- If diagnostic rendering itself fails, the original error remains intact and
  a `diagnostic error` note is appended.
