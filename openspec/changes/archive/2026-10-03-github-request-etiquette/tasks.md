## 1. Pure rate-limit functions

- [x] 1.1 Add `resource_of(url)`, mapping a URL to `code_search`, `search`, `core` or none
- [x] 1.2 Add `classify_limit(status, headers)` returning none, primary or secondary, following the classification table in specs/github-requests
- [x] 1.3 Add `limit_pause(kind, headers, now, backoff_level)`: primary waits until reset (60s fallback, 1h cap with WARN); secondary uses `Retry-After`, otherwise `min(60s × 2^level, 15m)`
- [x] 1.4 Add `pace_interval(resource, remaining, limit, reset, now)`: always paced for search resources; for `core`, paced only below a 10% reserve
- [x] 1.5 Unit-test 1.1–1.4 with `given`/`expected`/`actual` tables, covering every spec scenario for classification, pause and pacing

## 2. Rate gate and transport

- [x] 2.1 Add the per-resource rate gate (map of resource to state under one mutex) and hold it on `STATE`
- [x] 2.2 In `FileCachingRequest.RoundTrip`, after a cache miss: wait on the gate, claim a pacing slot, take an `HTTPSem` slot, send the request, then update the gate from the response headers
- [x] 2.3 Apply the gate and `HTTPSem` to the `.zip` branch of `RoundTrip`
- [x] 2.4 Lower `HTTPSem` from 50 to 10
- [x] 2.5 Put the non-`2xx` response body back after reading it for logging
- [x] 2.6 Log a DEBUG message when `X-RateLimit-Resource` disagrees with `resource_of`
- [x] 2.7 Test the gate: a pause set by one caller blocks a second caller on the same resource and does not block a different resource (short durations, injected clock where practical)

## 3. Retry loop and headers

- [x] 3.1 Replace `throttled`/`wait` in `github_download_with_retries_and_backoff`: rely on the gate for rate-limit waits, keep a short fixed sleep for other non-`2xx` responses, raise attempts to 6, keep `404` → `ErrNotFound`
- [x] 3.2 Log primary limits at INFO and secondary limits at WARN, including the body and rate-limit headers; the final failure logs at ERROR with the URL, status and body
- [x] 3.3 Add `Accept: application/vnd.github+json` and `X-GitHub-Api-Version` (named constant `2022-11-28`) to requests from `github_download` to the API host; release asset downloads on github.com (`github_zip_download`, `release.json`) do not get them
- [x] 3.4 Remove `throttled` and `wait` once nothing calls them

## 4. Search probe

- [x] 4.1 Change the `search_slice` probe to `per_page=100` and update its comment to explain the cache reuse
- [x] 4.2 Update bisection tests whose assertions depend on the probe's `per_page` or the call sequence; add a test that a slice that fits makes its page 1 request with the same `per_page` as the later pages
- [x] 4.3 Run `go test ./...` and `go vet ./...`

## 5. Verification against the live API

- [x] 5.1 Run `./manage.sh update` with an expired search cache and confirm it completes without a secondary-limit abort
- [x] 5.2 Confirm from the DEBUG logs that `code_search` requests are spaced about 6 seconds apart and that page 1 of an unsplit slice is a cache HIT
- [x] 5.3 Compare the resulting catalogue with the previous `addons.csv`: no unexplained removals
