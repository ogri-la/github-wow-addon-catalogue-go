# Tasks: search size bisection

## 1. Core implementation

- [x] 1.1 Add a size-slice probe helper: request a `size:lo..hi` slice with `per_page=1` and return its approximate `total_count`
- [x] 1.2 Add a slice fetcher: paginate a `size:lo..hi` slice fully (no `sort`/`order`), returning page blobs plus a saturated flag (10 full pages)
- [x] 1.3 Add the bisection driver: probe a slice, split above the threshold (800) or on saturation, recurse on halves, ERROR-log an unsplittable (`lo == hi`) saturated slice and keep its pages
- [x] 1.4 Route `search_github`'s "code" endpoint through the bisection driver starting from `size:0..999999`; keep the sort/order permutation loop for the "repositories" endpoint only
- [x] 1.5 Remove the `total_count > 2000` fatal and the truncation WARN from the code-search path

## 2. Tests

- [x] 2.1 Unit-test the bisection driver against a stubbed fetcher: within-window query needs no split; over-threshold probe bisects; saturated-despite-low-count slice bisects; single-value saturated slice logs ERROR and keeps pages
- [x] 2.2 Unit-test that generated code-search URLs carry the `size:` qualifier and no `sort`/`order` parameters
- [x] 2.3 Verify existing tests still pass (`go test ./...`)
- [x] 2.4 Fix `more_pages` dropping the final partial page (pre-existing: any query whose total was not a multiple of `per_page` lost up to 99 results)

## 3. Verification against the live API

- [x] 3.1 Run `./manage.sh update` and confirm the `CF_API_KEY` query completes, retrieving ~2000 results instead of aborting
- [x] 3.2 Confirm the `bigwigsmods packager` query now yields more than 1000 unique repos (recovering the previously truncated tail)
- [x] 3.3 Compare the resulting catalogue against `addons.csv` from the previous run: additions expected, no unexplained removals
- [x] 3.4 Fix `fetch_all_pages` stopping on `total_count`: Github serves results past the reported total (a slice reporting 483 served 693), so pagination now stops on a short page
