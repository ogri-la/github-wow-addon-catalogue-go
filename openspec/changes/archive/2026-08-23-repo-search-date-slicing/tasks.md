# Repository search date slicing — tasks

## 1. Generalise the bisection machinery

- [x] 1.1 Extract the qualifier rendering from `search_size_slice`/`split_size_slice` into a `func(lo, hi int) string` parameter, with `size_qualifier` as the code-search renderer
- [x] 1.2 Confirm the existing bisection tests pass unchanged against the generalised functions

## 2. Date qualifier

- [x] 2.1 Add `created_qualifier`: renders `[lo..hi]` day offsets from epoch `2007-01-01` as `created:YYYY-MM-DD..YYYY-MM-DD`
- [x] 2.2 Compute the repository-search upper bound as days from the epoch to `time.Now().UTC()` plus one day

## 3. Wire up repository searches

- [x] 3.1 Route the `repositories` endpoint in `search_github` through the generalised bisection with `created_qualifier`
- [x] 3.2 Remove the `sort`/`order` permutation loop and the truncation WARN it carried
- [x] 3.3 Ensure the single-day saturated slice logs an ERROR naming the query and slice

## 4. Tests

- [x] 4.1 Unit test `created_qualifier` boundary rendering (epoch day, same-day range, today's upper bound)
- [x] 4.2 Extend the stubbed-fetcher bisection tests to cover a repository corpus keyed by creation day, including the over-1000 single-query case
- [x] 4.3 Assert no repository search URL contains `sort` or `order` parameters

## 5. Verify

- [x] 5.1 Run `go test ./...`
- [x] 5.2 Run a live `./manage.sh update` (or the search phase alone) and confirm `topic:wow-addon` retrieves ~1245 results with no truncation WARN
