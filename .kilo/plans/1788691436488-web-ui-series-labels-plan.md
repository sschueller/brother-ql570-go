# Web UI: series (numbered) labels

## Goal

Let the embedded label designer (`web/index.html`) print a series of numbered
labels by writing a range in the text field — e.g. `SW-{01..10}` prints 10
labels (`SW-01` … `SW-10`). Implemented entirely client-side; no backend
changes, because `/v1/print` already accepts a JSON **array** of jobs
(`ql.ParseJobs`, `PrintJobs` print one label per entry).

## Decisions

- **Syntax**: Bash-style brace range `{start..end}` with optional step
  `{start..end..step}` and zero-padding `{start..end}` where `start` has
  leading zeros (e.g. `{01..10}` → `01`…`10`).
  - `{1..10}` → 1…10
  - `{01..10}` → 01…10 (padding width = max digit width of start/end)
  - `{1..10..2}` → 1,3,5,7,9
  - `{10..1}` → 10…1 (descending when step omitted and start > end)
  - Integer ranges only; letters/decimals/empty ranges → clear error.
- **Fields expanded**: text lines, QR content, and barcode content.
- **Zip model**: multiple `{..}` tokens in one label must have equal counts and
  expand positionally (`{A..C} port {1..3}` is not supported — integer only —
  but `SW-{1..3} port {1..3}` → `SW-1 port 1`, `SW-2 port 2`, `SW-3 port 3`).
  Fields without a range stay constant across all labels.
- **Copies**: each expanded label keeps the UI "Copies" value (default 1).
  Series 1..10 + copies 1 = 10 labels; copies 2 = 20 labels (2 of each).
- **Cap**: maximum 1000 labels per series (constant); exceeding it shows an
  error. (Keeps a 1000-job array well under `/v1/print`'s 1 MB body limit.)
- **Preview**: shows the first label, with meta text "label 1 of N" when N > 1.

## Changes

### 1. `web/index.html` — series expansion + wiring

Add, next to `currentJob()` (~line 784):

- `parseRange(token)` — parse one `{..}` token; return the list of value
  strings and its count, or an error. Handle zero-padding and step/descending
  as above.
- `expandField(str)` — replace every `{..}` token in a single field string
  (zip positionally across tokens; enforce equal counts) → `string[]` (one per
  label, length 1 when no token).
- `buildSeriesJobs()` — start from `currentJob()`; expand each text line, the
  QR, and the barcode; compute `N` (max count, require every ranged field to
  equal `N`); return an array of `N` jobs (shallow copies with substituted
  `text`/`qr`/`barcode`). Throw on malformed token / mismatched counts / over
  cap.

Update `refreshPreview()`:
- Call `buildSeriesJobs()`; on error show it in `#preview-msg`.
- Preview `jobs[0]`; when `N > 1` append " · label 1 of N" to `#preview-meta`.

Update the print handler:
- Call `buildSeriesJobs()`; POST `JSON.stringify(jobs)` to `/v1/print`.
- Toast "Print job accepted — N labels" when N > 1.

Add a muted hint under the "Text lines" fieldset (e.g. "Series: use `{1..10}`
to print numbered labels — e.g. `SW-{01..10}`").

### 2. `README.md` — document the feature

Add a short "Series labels" note in the HTTP daemon section describing the
`{1..10}` / `{01..10}` / `{1..10..2}` syntax, that text/QR/barcode all expand,
that preview shows the first label, and that copies multiplies per label.

## Validation

- `make build` (compiles; `index.html` is embedded via `go:embed`).
- `make check` (go vet + tests — no Go changes, but confirm clean).
- Manual: run `./ql570 serve`, open the UI, enter `SW-{01..10}` in a text
  line; verify preview shows `SW-01` with "label 1 of 10"; verify Print posts
  10 labels (or use `curl` against `/v1/print` with the generated array body).
- Manual error cases: `{A..C}` (letters), `{1..3} x {1..2}` (count mismatch),
  `{1..}` (malformed), and a range exceeding the 1000 cap.

## Out of scope

- CLI `--text "SW-{1..10}"` expansion (CLI users can already generate a
  `jobs.json` array; a Go-side `ExpandSeries` helper could be added later if
  wanted).
- Letter (`a..z`) ranges.
