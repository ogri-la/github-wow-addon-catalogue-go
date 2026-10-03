## Why

`./manage.sh update` now aborts on GitHub's secondary rate limit: code-search requests return `403` after every wait for `X-RateLimit-Reset`, five times in a row, and the program exits. The retry logic only understands the primary limit. The size-slice bisection added more requests per query, mostly `per_page=1` probes. A probe costs one full request from the 10-per-minute code-search budget, and probably as much server work as a full page. Release fetching also starts one goroutine per repository with no limit on concurrent requests, which goes against GitHub's published guidance.

## What Changes

- The slice probe requests `per_page=100` instead of `per_page=1`. When the slice is not split, `fetch_all_pages` gets the same page 1 from the response cache, so no second request is made.
- A `403` or `429` is classified as a primary or secondary rate limit from its headers (`Retry-After`, `X-RateLimit-Remaining`). It is no longer assumed that every `403` is the primary limit.
- A secondary limit waits for `Retry-After` when present. Otherwise it waits at least 60 seconds and backs off exponentially between consecutive attempts.
- Rate-limit state is shared per GitHub rate-limit resource (`core`, `search`, `code_search`). A throttled response pauses every caller of that resource, not only the request that was refused.
- Requests to a resource are paced from `X-RateLimit-Remaining` and `X-RateLimit-Reset`, so the budget is spread over the window and not used up in a burst.
- The number of concurrent GitHub requests is limited. GitHub caps concurrent requests at 100, and the limit is set well below that.
- A refused request logs the rate-limit headers it received, so later failures show which limit was hit.

## Capabilities

### New Capabilities

- `github-requests`: how the program behaves towards the GitHub API as a client. It covers rate-limit classification, waiting and backoff, shared throttle state, pacing and limits on concurrency.

### Modified Capabilities

- `github-search`: slice probing uses a full page of results, and the page is reused rather than fetched again.

## Impact

- `main.go`: `throttled`, `wait` and `github_download_with_retries_and_backoff` are replaced or rewritten. `search_slice` changes its probe. `parse_repo_list` gets a concurrency limit. A new shared rate-limit gate is held on `STATE`.
- `github_zip_download` and the other GitHub calls go through the same gate and concurrency limit.
- `main_test.go`: new unit tests for rate-limit classification and pause calculation, written as pure functions over headers. Bisection tests are updated wherever they assert the probe's `per_page` or the call sequence.
- A run can take longer because requests are paced. It should finish where it currently aborts.
- No new dependencies.
