## ADDED Requirements

### Requirement: Slice probes cost no extra request

A search slice's size SHALL be checked by fetching its first page at the full page size, not with a smaller probe request. When the slice is not split, that first page SHALL be reused as the slice's first page of results and SHALL NOT be fetched from GitHub a second time while the response cache is writable.

#### Scenario: Slice within the split threshold

- **WHEN** a slice's first page reports a total at or below the split threshold and the response cache is writable
- **THEN** GitHub receives exactly one request for that slice's first page

#### Scenario: Slice over the split threshold

- **WHEN** a slice's first page reports a total above the split threshold
- **THEN** the slice is split with no further requests for that slice

#### Scenario: Probe request shape

- **WHEN** a slice is probed
- **THEN** the request uses the same page size as every other page of results
