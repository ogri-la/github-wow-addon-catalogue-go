# github-search

## Purpose

Discovers World of Warcraft addon repositories on GitHub by searching workflow file contents and repository topics, retrieving every match even when a query's total exceeds GitHub's 1000-result pagination window.

## ADDED Requirements

### Requirement: Code searches retrieve every result

Code searches SHALL retrieve every result of a query, including queries whose totals exceed GitHub's 1000-result pagination window, by partitioning the query into disjoint file-size slices (`size:lo..hi`, bytes, inclusive) and fetching each slice independently.

#### Scenario: Query total is within a single window

- **WHEN** a code search query has at most 1000 results
- **THEN** all results are fetched with plain pagination and no further slicing occurs

#### Scenario: Query total exceeds the window

- **WHEN** a code search query has more than 1000 results
- **THEN** the query is split into disjoint size slices, each fully paginated, and the union of the slices contains every result of the unsliced query

#### Scenario: A slice itself exceeds the window

- **WHEN** a size slice spans more than one byte value and returns 10 full pages
- **THEN** the slice is bisected and both halves are fetched in its place

### Requirement: Reported totals are treated as estimates

The system SHALL NOT rely on GitHub's `total_count` for completeness decisions, because sliced code-search queries under-report it and continue serving results past the reported total. Code-search pagination MUST continue until a page returns fewer results than requested, and saturation MUST be detected from the fetched pages themselves.

#### Scenario: Under-reported slice

- **WHEN** a slice claims fewer than 1000 results but pagination returns 10 full pages
- **THEN** the slice is treated as saturated and split, regardless of the claimed total

#### Scenario: Results served past the reported total

- **WHEN** a slice reports fewer results than it actually serves
- **THEN** pagination continues until a short page is returned, and every served result is retrieved

### Requirement: Unsplittable saturated slice is an error

A slice spanning a single byte value that still saturates the pagination window cannot be subdivided; results are then genuinely unreachable. The system SHALL log this at ERROR level, identifying the query and slice, and continue with the results it has.

#### Scenario: Single-value slice saturates

- **WHEN** a slice with `lo == hi` returns 10 full pages
- **THEN** an ERROR is logged naming the query and slice, and processing continues with the fetched results

### Requirement: Code searches do not use sort or order parameters

Code search requests SHALL NOT carry `sort` or `order` parameters: `/search/code` ignores them, so permuting them re-fetches identical pages and wastes rate-limited requests.

#### Scenario: Fetching a code search page

- **WHEN** any code search page is requested
- **THEN** the request URL contains no `sort` or `order` parameter

### Requirement: Repository searches paginate with sort permutations

Repository searches (`/search/repositories`) SHALL retain sort/order permutation pagination, which that endpoint honours, and SHALL warn when a query's total approaches the coverage those permutations provide.

#### Scenario: Repository query within coverage

- **WHEN** a repository search query has at most 1000 results
- **THEN** all results are fetched and no warning is emitted

#### Scenario: Repository query exceeds coverage

- **WHEN** a repository search query has more than 1000 results
- **THEN** a WARN names the query and its total, and the results that were reachable are kept
