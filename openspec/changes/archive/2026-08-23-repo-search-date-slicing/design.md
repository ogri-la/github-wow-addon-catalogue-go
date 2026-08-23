# Repository search date slicing — design

## Context

See `proposal.md` for motivation. The size-bisection change left this machinery in `main.go`:

- `search_size_slice` / `split_size_slice`: recursive bisection over an inclusive integer range `[lo..hi]`, probing each slice's `total_count` before fetching and splitting on either an oversized probe or a saturated fetch.
- `fetch_all_pages`: pagination that stops on a short page and reports saturation.
- `search_fetcher`: a function type separating the recursion from the network, used by the tests to run the bisection against a fake corpus.

The recursion is size-specific only in one place: `size_qualifier` renders the range as `size:lo..hi`. Everything else — probe, threshold, split, saturation — is qualifier-agnostic.

`/search/repositories` differs from `/search/code` in two ways that matter here: it reports `total_count` accurately, and it supports the `created:YYYY-MM-DD..YYYY-MM-DD` qualifier (inclusive, dates interpreted as UTC).

## Goals / Non-Goals

**Goals**

- Both endpoints share one bisection implementation; the qualifier is the only variation point.
- Repository searches lose the deprecated `sort`/`order` parameters and the 4× redundant fetch.

**Non-Goals**

- No change to code-search behaviour, dedup, caching, or the topic queries themselves.
- No handling of result sets past the window within a single day; the single-day ERROR invariant mirrors the single-byte-size one.

## Decisions

**Generalise the recursion over a qualifier renderer.** `search_size_slice` and `split_size_slice` take a `func(lo, hi int) string` (or equivalent) that renders `[lo..hi]` as a search qualifier; `size_qualifier` is one renderer, a new `created_qualifier` the other. Alternative — duplicating the recursion for dates — was rejected: the probe/saturation/split logic is subtle and already tested, and two copies would drift.

**Dates map to integers as days since an epoch.** `created_qualifier` converts slice bounds to dates with an epoch of `2007-01-01` (before GitHub's first repository) and formats them `YYYY-MM-DD`. The bisection stays purely integer; dates exist only at the rendering edge, which is also where the tests can stay integer-based.

**The upper bound is tomorrow, in UTC.** GitHub evaluates `created:` in UTC. Using the runtime's local "today" could exclude repositories created within the last hours around midnight; `time.Now().UTC()` plus one day costs nothing and removes the edge entirely. The "created on the day the search runs" spec scenario pins this.

**Thresholds are shared, not tuned per endpoint.** `SEARCH_SPLIT_THRESHOLD` (500) is conservative for an endpoint with accurate totals; a higher repository-specific threshold would save at most a handful of probe requests per run. One constant keeps the machinery uniform. Saturation detection stays as the backstop on both endpoints, so accuracy of `total_count` is never load-bearing.

## Risks / Trade-offs

- [More than 1000 matching repositories created on one day] → implausible for topic queries; the single-day slice logs an ERROR and keeps what it fetched, matching the code-search invariant.
- [Date-bearing URLs change daily, missing the search cache] → irrelevant in practice: `CACHE_DURATION_SEARCH` is 2 hours, so the cache only ever serves re-runs within a day, where the URLs are identical.
- [Fewer total requests but more request diversity] → request volume drops (one fetch per result set plus probes, versus four full passes today); rate-limit behaviour is strictly better.

## Migration Plan

None required. The change is internal to `search_github`; output format and downstream processing are untouched. The truncation WARN for repository searches disappears along with the truncation.
