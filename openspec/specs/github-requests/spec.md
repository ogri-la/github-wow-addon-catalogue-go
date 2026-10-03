# github-requests

## Purpose

Defines how the program behaves as a client of the GitHub API: how it recognises and waits out rate limits, how it paces and limits its requests, and what it reports when GitHub refuses a request.

## Requirements

### Requirement: Rate-limited responses are classified

A `403` or `429` response from GitHub SHALL be classified from its headers as either a primary rate limit or a secondary rate limit. A response with `X-RateLimit-Remaining: 0` and no `Retry-After` header is a primary rate limit. A response with a `Retry-After` header, or with remaining budget, is a secondary rate limit.

#### Scenario: Primary budget exhausted

- **WHEN** a `403` response carries `X-RateLimit-Remaining: 0` and no `Retry-After` header
- **THEN** it is classified as a primary rate limit

#### Scenario: Retry-After present

- **WHEN** a `403` or `429` response carries a `Retry-After` header
- **THEN** it is classified as a secondary rate limit, whatever `X-RateLimit-Remaining` says

#### Scenario: Refused with budget remaining

- **WHEN** a `403` or `429` response carries `X-RateLimit-Remaining` greater than zero and no `Retry-After` header
- **THEN** it is classified as a secondary rate limit

#### Scenario: Refused without rate-limit headers

- **WHEN** a `403` or `429` response carries neither `X-RateLimit-Remaining` nor `Retry-After`
- **THEN** it is classified as a secondary rate limit

### Requirement: Primary rate limits wait for the reset

After a primary rate limit, the next request to the same rate-limit resource SHALL NOT be sent before the time given in `X-RateLimit-Reset`. Because that header is truncated to the second, the wait SHALL end one second after it. A reset time that is missing, unreadable or more than one second in the past SHALL be replaced by a wait of 60 seconds. A reset time more than one hour away SHALL be capped at one hour and logged at WARN.

#### Scenario: Reset time in the near future

- **WHEN** a primary rate limit gives a reset time 40 seconds away
- **THEN** no request to that resource is sent for 40 seconds

#### Scenario: Reset time missing

- **WHEN** a primary rate limit has no readable `X-RateLimit-Reset` header
- **THEN** the program waits 60 seconds before the next request to that resource

### Requirement: Secondary rate limits back off

After a secondary rate limit, the next request to the same rate-limit resource SHALL NOT be sent before `Retry-After` seconds have passed. When `Retry-After` is absent, the wait SHALL be at least 60 seconds and SHALL double with each consecutive secondary rate limit on that resource, up to a cap. A successful response to that resource SHALL reset the backoff. A secondary rate limit SHALL be logged at WARN.

#### Scenario: Retry-After given

- **WHEN** a secondary rate limit carries `Retry-After: 90`
- **THEN** no request to that resource is sent for 90 seconds

#### Scenario: Consecutive secondary limits without Retry-After

- **WHEN** three consecutive requests to a resource each receive a secondary rate limit without `Retry-After`
- **THEN** the waits before the following requests are 60, 120 and 240 seconds

#### Scenario: Backoff resets after success

- **WHEN** a request to a resource succeeds after a secondary rate limit
- **THEN** the next secondary rate limit on that resource waits 60 seconds again

### Requirement: Throttling is shared across concurrent requests

Rate-limit state SHALL be kept per GitHub rate-limit resource (for example `core`, `search` and `code_search`) and shared by every request to that resource. When one request is throttled, other requests to the same resource SHALL wait with it and SHALL NOT be sent during the pause. Requests to other resources SHALL NOT be affected.

#### Scenario: Concurrent requests during a pause

- **WHEN** one `core` request receives a rate limit while other `core` requests are waiting to be sent
- **THEN** none of the waiting `core` requests is sent before the pause ends

#### Scenario: Different resource

- **WHEN** the `code_search` resource is paused
- **THEN** `core` requests continue to be sent

### Requirement: Requests are paced to the remaining budget

Requests to a rate-limited resource SHALL be spaced using that resource's most recent `X-RateLimit-Remaining` and `X-RateLimit-Reset` values, so that the remaining budget is not used up before the reset. The last request of a window SHALL NOT be sent before one second after the reset. Search resources SHALL always be paced. The `core` resource SHALL be paced once its remaining budget falls below a reserve.

#### Scenario: Code search budget

- **WHEN** the `code_search` resource reports 10 requests remaining and a reset 60 seconds away
- **THEN** consecutive `code_search` requests are sent about 6 seconds apart

#### Scenario: Last request of a window

- **WHEN** a resource reports 1 request remaining and a reset 5 seconds away
- **THEN** the next request to that resource is not sent for 6 seconds

#### Scenario: Plenty of core budget

- **WHEN** the `core` resource reports more remaining budget than its reserve
- **THEN** `core` requests are not delayed by pacing

#### Scenario: Responses served from the cache

- **WHEN** a request is answered from the local cache
- **THEN** it is not paced, not limited by concurrency, and does not change rate-limit state

### Requirement: Concurrent requests are limited

The number of GitHub requests in flight at the same time SHALL be limited to a fixed number well below GitHub's limit of 100. The limit SHALL apply to every network request, including `.zip` range requests and followed redirects.

#### Scenario: Many repositories parsed at once

- **WHEN** thousands of repositories are parsed concurrently and none are cached
- **THEN** no more than the configured number of network requests are in flight at any time

### Requirement: Retries are bounded

A request refused with a rate limit, or answered with a non-`2xx` status other than `404`, SHALL be retried a fixed number of times, waiting as these requirements describe between attempts. When the attempts are exhausted, the failure SHALL be logged at ERROR with the URL, the last status and the last response body, and an error SHALL be returned. A `404` SHALL NOT be retried.

#### Scenario: Persistent secondary limit

- **WHEN** every attempt for a URL receives a secondary rate limit
- **THEN** an ERROR is logged with the URL, status and body, and an error is returned after the final attempt

#### Scenario: Not found

- **WHEN** a request receives `404`
- **THEN** it is not retried and a not-found error is returned at once

### Requirement: Refused responses are reported in full

When GitHub refuses a request, the response body and the rate-limit headers (`Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `X-RateLimit-Resource`) SHALL be available to the caller and included in the log message for the refusal. Reading the body for logging SHALL NOT remove it from the response.

#### Scenario: Secondary limit with a message

- **WHEN** GitHub returns `403` with a JSON body explaining a secondary rate limit
- **THEN** the WARN for that refusal includes the body text and the rate-limit headers

### Requirement: Requests identify the client and API version

Every request to the GitHub API SHALL carry the program's `User-Agent`, an `Accept: application/vnd.github+json` header, and an `X-GitHub-Api-Version` header naming a fixed API version.

#### Scenario: API request headers

- **WHEN** any request is sent to `api.github.com`
- **THEN** it carries the `User-Agent`, `Accept` and `X-GitHub-Api-Version` headers
