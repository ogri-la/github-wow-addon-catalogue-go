# Repository search date slicing

## Why

`topic:wow-addon` now has 1245 results, past GitHub's 1000-result pagination window. Repository searches still rely on permuting the deprecated `sort`/`order` parameters, which caps reachable results at 2000 (ascending plus descending over one dimension) and already loses the middle of any ordering dimension it does not permute cleanly. The size-bisection change fixed this for code searches and knowingly left repository searches out of scope.

## What Changes

- Repository searches are partitioned by the `created:` qualifier (repository creation date, `YYYY-MM-DD..YYYY-MM-DD`, inclusive) using recursive bisection, mirroring the size bisection used for code searches.
- The `sort`/`order` permutation loop is removed entirely: with both endpoints partitioned, no caller remains and the deprecated parameters are no longer sent.
- The slicing machinery introduced for code search (probe, split threshold, saturation detection, full pagination) is generalised over an integer range so both qualifiers (`size:` in bytes, `created:` in days) share it.
- A single-day slice that still saturates the window logs an ERROR, matching the single-byte-size invariant for code searches.

## Capabilities

### New Capabilities

<!-- none -->

### Modified Capabilities

- `github-search`: the "Repository searches paginate with sort permutations" requirement is replaced. Repository searches retrieve every result by date partitioning instead of warning and truncating past the window; no search request carries `sort` or `order` parameters.

## Impact

- `main.go`: `search_github` (repository path), the generalisation of `search_size_slice`/`split_size_slice`, removal of the sort/order loop.
- Request volume for repository searches drops: today each topic query is fetched four times (up to 40 pages); date slicing fetches each result once plus a handful of probe requests.
- `total_count` is accurate on `/search/repositories` (unlike code search), so probe-based splitting is reliable; the saturation backstop stays because it is shared machinery.
- Search response caching keys on URL; sliced URLs cache naturally, as with size slicing.
