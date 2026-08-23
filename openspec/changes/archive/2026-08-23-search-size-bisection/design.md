# Design: search size bisection

## Context

See `proposal.md - Why` for motivation. Empirical findings against the live API (2026-08-22) that constrain the design:

- `/search/code` ignores `sort`/`order`; ordering is fixed best-match. The existing four-permutation loop in `search_github` fetches identical pages four times.
- The `size:` qualifier filters on the matched file's size in bytes. `lo..hi` ranges are inclusive. Disjoint adjacent slices were verified item-level to be leak-free: every item of a covering range appears in the union of its sub-slices.
- `total_count` on sliced queries under-reports badly and Github keeps serving results past it. Measured live: `size:977..1952` reported 483 results and served 693 across 7 pages. A slice claiming 982 returned 1000+. Counts also wobble between adjacent probes; child totals can exceed the parent's. Pagination must therefore stop on a short page, never on the reported total.
- Comparison forms (`size:>=n`, `size:<n`) fail through URL encoding in this client; explicit `lo..hi` ranges always work. All current matches sit under 100 KB; workflow files cluster at 300-2000 bytes.
- Code search has a tight secondary rate limit, already handled by `github_download_with_retries_and_backoff` and the HTTP cache (`CACHE_DURATION_SEARCH`). Cache keys derive from the URL, so sliced URLs cache independently with no changes.

## Goals / Non-Goals

Goals:

- Complete retrieval for code queries of any size, with request volume comparable to today's 40-per-query.
- Preserve the existing return shape of `search_github` (list of raw JSON page blobs) so downstream parsing is untouched.

Non-Goals:

- Changing repository (`topic:`) searches. `/search/repositories` honours sort/order and reports `total_count` accurately (measured: `topic:warcraft-addon` claimed 256 and served 256), so the code-search defects do not apply there. `topic:wow-addon` does now exceed the window at 1245 results and truncates, but fixing that needs a different partition (`created:` date ranges) and is left for its own change.
- Deduplication across slices — the existing `repo_idx` map in `get_projects` already dedupes by repo ID.
- Retiring the deprecated sort/order pagination for repository searches.

## Decisions

**Bisection over fixed buckets.** Fixed size buckets (0-999, 1000-1999, ...) would need retuning as the corpus grows and its size distribution shifts. Recursive bisection adapts automatically: a slice that reports too many results splits at its midpoint. Alternative considered: partitioning by workflow filename (`path:...release.yml`) — rejected, the filename tail is unbounded so completeness cannot be guaranteed; and by repo-name prefix — rejected, no code-search qualifier can express it.

**Probe, then fetch.** Each slice is first probed with `per_page=1` to read its (approximate) total. Totals above a split threshold bisect without fetching pages; totals below it fetch all pages. This costs one request per interior node but avoids fetching up to 10 pages that saturation would discard. The probe is cheap relative to the 10-page fetch and both share the cache.

**Pagination follows pages, not counts.** A page shorter than `per_page` ends the slice; an empty page ends it too. `total_count` is never used to decide whether to fetch another page, because it under-reports by as much as 40%. This is the same principle as the saturation backstop, applied to the page loop rather than only the split decision.

**Split threshold 500, saturation as backstop.** The threshold only avoids the wasted pages of an obviously oversized slice; it cannot be trusted to decide correctness given the under-reporting. 500 is deliberately conservative. What actually decides a split is saturation: a slice returning 10 full pages is split regardless of its claimed total.

**Initial range `0..999999`.** Covers everything observed (nothing above 100 KB) by three orders of magnitude. A generous bound costs nothing: the first probe of an oversize corpus immediately bisects, and empty upper halves cost one probe each.

**Midpoint bisection on byte values.** `lo + (hi-lo)/2`. No attempt to balance splits against the actual size distribution: the distribution is unknown without extra requests, and unbalanced splits only cost a few extra probes. A slice with `lo == hi` cannot split; if it still saturates, log ERROR and keep what was fetched (spec: unsplittable saturated slice).

**Fatal exit replaced, not relocated.** The `> 2000` fatal in `search_github` protected against silent catalogue shrinkage. Bisection removes the underlying limit, so the guard is deleted rather than raised. The remaining loss modes each log: unsplittable saturated slice (ERROR), and failed page fetches (existing ERROR + fatal, unchanged).

## Risks / Trade-offs

- [GitHub changes `size:` semantics or drops it from legacy code search] → The saturation backstop detects silent truncation (10 full pages) and the ERROR path names the query; the catalogue also cross-covers via the other queries and input files. Not silently absorbable, by design.
- [Estimate error exceeds the 200-result margin] → Saturation detection catches it: the slice fetches 10 full pages, splits, and re-fetches. Costs requests, loses nothing.
- [Deeper corpora inflate request counts] → Interior probes are one request each and leaf count grows logarithmically with corpus size; at 10x today's corpus the request count stays under ~150 per query, within throttling tolerances.

## Migration Plan

Single deploy; no data migration. The output CSV shape is unchanged. First run after deploy will fetch more results than previous runs (recovering the truncated tails of both over-1000 queries), which appears as an increase in catalogue size — expected, not anomalous. Rollback is a revert.
