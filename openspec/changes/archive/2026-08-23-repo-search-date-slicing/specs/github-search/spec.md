## ADDED Requirements

### Requirement: Repository searches retrieve every result

Repository searches SHALL retrieve every result of a query, including queries whose totals exceed GitHub's 1000-result pagination window, by partitioning the query into disjoint repository-creation-date slices (`created:YYYY-MM-DD..YYYY-MM-DD`, inclusive) and fetching each slice independently. Repository search requests SHALL NOT carry `sort` or `order` parameters.

#### Scenario: Query total is within the split threshold

- **WHEN** a repository search query has at most the split threshold of results
- **THEN** all results are fetched with plain pagination and no further slicing occurs

#### Scenario: Query total is between the split threshold and the window

- **WHEN** a repository search query has more results than the split threshold but at most 1000
- **THEN** the query is split and every result is retrieved across the slices

#### Scenario: Query total exceeds the window

- **WHEN** a repository search query has more than 1000 results
- **THEN** the query is split into disjoint date slices, each fully paginated, and the union of the slices contains every result of the unsliced query

#### Scenario: A slice itself exceeds the window

- **WHEN** a date slice spans more than one day and returns 10 full pages
- **THEN** the slice is bisected and both halves are fetched in its place

#### Scenario: Repositories created on the day the search runs

- **WHEN** a matching repository was created on the day the search runs
- **THEN** some date slice includes that day and the repository is retrieved

#### Scenario: Single-day slice saturates

- **WHEN** a slice spanning a single day returns 10 full pages
- **THEN** an ERROR is logged naming the query and slice, and processing continues with the fetched results

#### Scenario: Fetching a repository search page

- **WHEN** any repository search page is requested
- **THEN** the request URL contains no `sort` or `order` parameter

## REMOVED Requirements

### Requirement: Repository searches paginate with sort permutations

**Reason**: Permuting the deprecated `sort`/`order` parameters caps reachable results at 2000 and already truncates `topic:wow-addon` (1245 results) on every pass. Date partitioning retrieves every result without deprecated parameters.
**Migration**: Repository searches are covered by the "Repository searches retrieve every result" requirement; results past the window that were previously warned about and lost are now retrieved.
