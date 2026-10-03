## Context

All HTTP goes through one `http.Client` whose transport is `FileCachingRequest` (`main.go:740`). For each request, the transport:

- serves a valid cache entry without touching the network;
- otherwise takes a slot from `HTTPSem`, which allows 50 requests in flight, and sends the request;
- caches only `2xx` responses.

`.zip` requests skip both the cache and `HTTPSem`. On a non-`2xx` response, the transport reads the body for a DEBUG log and returns the response with that body already consumed. This is why the failure log shows `body=""`.

Rate limiting is handled one level up, in `github_download_with_retries_and_backoff`. It calls `throttled` (any `403`) and `wait` (sleep until `X-RateLimit-Reset`, or 60 seconds when the reset is missing or more than 120 seconds away). Each goroutine sleeps on its own, and nothing records that a resource is paused.

`search_slice` probes each slice with `per_page=1` before `fetch_all_pages` fetches it at `per_page=100`. Search responses are cached for 2 hours, keyed by the full URL.

See proposal.md for why this is changing and specs/ for the required behaviour.

## Goals / Non-Goals

**Goals:**

- Classification and pause calculation are pure functions of status, headers, the current time and backoff state, so unit tests need no network access.
- One place decides when a request may be sent. Retry loops count attempts and do not sleep themselves.
- Cache hits cost nothing: no gate, no pacing, no concurrency slot.

**Non-Goals:**

- Conditional requests (`ETag` / `If-None-Match`). A `304` does not count against the primary limit, so this would be a worthwhile follow-up. It changes how the cache works, though, and is a separate change.
- GraphQL, and moving off the REST search endpoints.
- Changing how search slices are bisected, beyond the shape of the probe.

## Decisions

### 1. A per-resource rate gate inside the transport

The data is a **map from rate-limit resource to its state**. Each state holds:

- `paused_until`: no request before this time;
- `next_send`: the earliest time pacing allows the next request;
- `remaining`, `reset` and `limit`: from the most recent response;
- `backoff_level`: how many secondary limits in a row.

A single mutex guards the map. Every request to a resource uses the same state, which is what lets one throttled response pause every other request to that resource.

The gate is held on `STATE` and used by `LimitedRequest`, a `RoundTripper` that sends requests over the network. `FileCachingRequest` uses `LimitedRequest` on a cache miss, on the `.zip` branch, and as the transport of the client that follows redirects. For each request it sends, `LimitedRequest`:

1. `resource_of(url)` maps the URL to a resource: `/search/code` → `code_search`, other `/search/*` → `search`, any other `api.github.com` path → `core`. Any other host → none, which means no gate.
2. Before sending: wait until the later of `paused_until` and `next_send`. Claim the next pacing slot, then take a concurrency slot.
3. After the response: update `remaining`, `reset` and `limit` from the headers. If the response is `403` or `429`, classify it and set `paused_until`. Otherwise, a success resets `backoff_level`.

The resource comes from the URL, not from `X-RateLimit-Resource`, because the gate needs it before the request is sent. When the header disagrees with the URL mapping, a DEBUG message is logged.

It is a decorator around `http.DefaultTransport`. `FileCachingRequest` keeps the cache-miss decision and delegates only the network step. This means every network request goes through the gate, including each hop of a redirect, and no slot is held while another is taken.

*Alternatives:* keep the sleeping in the retry loop, as now. That would leave each goroutine to discover the pause by being refused, which is the behaviour this change exists to remove. Putting the gate inline in `FileCachingRequest.RoundTrip` was the first plan, but the `.zip` branch and the redirect client would each have needed a copy of it.

### 2. Pure functions for classification, pause and pacing

- `classify_limit(status, headers) → none | primary | secondary`: implements the classification table in specs/github-requests.
- `limit_pause(kind, headers, now, backoff_level) → duration`. For a primary limit: until one second after the reset (the header is truncated to the second), with 60 seconds as the fallback and one hour as the cap. For a secondary limit: `Retry-After`, otherwise `min(60s × 2^backoff_level, 15m)`.
- `pace_interval(resource, remaining, limit, reset, now) → duration`. Search resources: `(reset + 1s − now) / max(remaining, 1)`, so the last request of a window lands after the reset. The first live run was refused at exactly the reset second without this margin. `core`: zero while `remaining` is above 10% of `limit`, the same formula below that.

These replace `throttled` and `wait`. Each takes `now` as an argument, so tests do not depend on the wall clock.

### 3. Retry loop only counts attempts

`github_download_with_retries_and_backoff` keeps its role of turning a URL into a `2xx` response, a `404` (`ErrNotFound`) or an error. It no longer sleeps for rate limits, because the gate will block the next attempt for as long as needed. For a non-`2xx` that is not a rate limit (for example `5xx`), it sleeps for a short fixed backoff itself, because the gate has no state for that case. Attempts go up from 5 to 6, so a run of secondary-limit backoffs (60, 120, 240, 480 and 900 seconds) can complete before the request is abandoned. A primary-limit WARN is downgraded to INFO, because being throttled by the primary limit is expected. A secondary limit is logged at WARN, because it means the pacing is not working.

### 4. Probe at full page size and rely on the cache

`search_slice` probes with `fetch(endpoint, sliced_query, 1, 100)`. When the slice fits, `fetch_all_pages` asks for the same URL, and `FileCachingRequest` serves it from the entry the probe wrote. No change is needed in `fetch_all_pages` or the `search_fetcher` type.

*Alternative:* pass the probed page into `fetch_all_pages`. That works without the cache, but it widens the `search_fetcher` contract and adds a code path. The cache already makes the reuse free, and it was the approach chosen during discussion.

### 5. Concurrency limit lowered and applied to every network request

`HTTPSem` goes down from 50 to 10. `LimitedRequest` takes a slot for the time it takes to receive the response headers, so the `.zip` branch and each redirect hop are covered too. Ten keeps release fetching reasonably fast on cache misses and stays far below GitHub's limit of 100. A redirect hop takes its own slot after the first request has released its slot, so a request never holds two slots at once.

### 6. Refused responses keep their body

When the transport reads a non-`2xx` body for logging, it puts the bytes back as `io.NopCloser(bytes.NewReader(...))`. The retry loop can then log the body together with the rate-limit headers. `truncate(…, 256)` stays.

### 7. Standard headers

`github_download` adds `Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2022-11-28` when the URL is on the API host. The version is a named constant. `2022-11-28` is what GitHub currently serves by default, so responses do not change. Release asset downloads (`.zip` files and `release.json`) are on github.com, not the API, so they do not get these headers. An API `Accept` header there could change what is served.

## Risks / Trade-offs

- [Pacing makes runs longer. Code search at about 6 seconds per request is slower than bursting and then waiting.] → The total for a full minute is the same, and the current burst-then-wait approach is what led to the run aborting. Cache hits are not paced.
- [The cache write fails, so a probed page is fetched twice.] → This costs the same as today's `per_page=1` probe. The transport already logs a WARN for it.
- [URL-to-resource mapping disagrees with GitHub, for example if a new resource is introduced.] → A DEBUG log on a mismatch. A wrong mapping only affects pacing, and `paused_until` is still set from the refusal itself.
- [The 10% `core` reserve is too small or too large.] → It is a named constant. The `core` budget is 5000 an hour and rarely the problem.
- [A genuine permission `403` is now treated as a secondary limit and waits up to about 30 minutes before failing.] → GitHub returns permission failures on these endpoints as `404`, which still fails at once. A real `403` from a mistake in the token is visible in the WARN body after the first refusal.

## Migration Plan

No data or format changes. The cache layout is the same, so cached search entries made with the old `per_page=1` probes simply expire. Rolling back means reverting the commit.
