# Search size bisection

## Why

GitHub's code search API caps every query at 1000 retrievable results, and the `sort`/`order` parameters the program relies on to reach the "other" results are silently ignored on `/search/code` (verified empirically: all four sort/order combinations return byte-identical pages). Two of the three code queries now exceed the cap (`bigwigsmods packager` at ~1726, `CF_API_KEY` at ~2014), so addons are being missed and the `CF_API_KEY` query aborts the whole update with a hard `> 2000` error.

## What Changes

- Code searches are partitioned by the `size:` qualifier (matched file size in bytes) using recursive bisection, so every result is retrievable regardless of how large the total grows.
- The `sort`/`order` permutation loop is removed from code searches: it is a no-op that fetches the same 1000 results four times.
- The hard "results exceed 2000" exit is removed; a query that outgrows a slice splits the slice instead. The replacement invariant is per-slice: a slice that cannot be split further yet still saturates the 1000-result window logs an ERROR.
- `total_count` is treated as an estimate (observed under-reporting of ~5-8% on sliced queries): splitting uses a conservative threshold and saturation is detected from fetched pages, not the claimed count.
- Repository searches (`topic:` queries) are unchanged: `/search/repositories` honours `sort`/`order`, and unlike code search it reports `total_count` accurately. `topic:wow-addon` has since passed the window (1245 results) and truncates on every sort/order pass; that loss is knowingly out of scope here and needs its own partition, most likely `created:` date ranges.

## Capabilities

### New Capabilities

- `github-search`: discovery of addon repositories via GitHub search — code-search partitioning by file size with recursive bisection, complete result retrieval past the 1000-result API window, and repository-search pagination.

### Modified Capabilities

<!-- none: this is the first spec in the project -->

## Impact

- `main.go`: `search_github` (code-search path), `more_pages`, and the constants/log messages around search truncation.
- Request volume is roughly unchanged (~30-40 requests per over-1000 query versus 40 today), but coverage rises from 1000 to the full result set.
- Search response caching (`CACHE_DURATION_SEARCH`) keys on URL, so sliced URLs cache naturally; no cache changes needed.
