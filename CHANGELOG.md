# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Download counts and release counts are now captured for each addon
  - available as `downloads` and `release-count` in JSON, and as two new columns appended to CSV output
- `cache-stats` subcommand, reporting cache size and age statistics broken down by entry type
- `cache-prune` subcommand, removing expired cache entries. Runs as a dry-run unless `--delete=true` is given
- `find-duplicates` subcommand, reporting addons that share a project ID across different owners
- `dump-release-dot-json` subcommand, emitting cached `release.json` files for the `release.json-validator` project
- Support for the Mists of Pandaria game flavour
- Repositories whose latest release is unusable can now fall back to the release before it
- A warning is issued when an addon's project IDs cannot be determined with confidence
- `manage.sh` for building, testing, linting and releasing, replacing `update.sh`
- `debug.sh` for inspecting individual addons

### Changed

- **BREAKING:** all functionality now sits behind subcommands
  - scraping is no longer the default, `./github-wow-addon-catalogue` becomes `./github-wow-addon-catalogue scrape`
- Search results exceeding Github's 1000-result limit are now detected and reported rather than silently truncated
- Concurrent requests to Github are limited to 50, and the wait after being throttled has increased to 60 seconds
- Cache writes now take an exclusive file lock, so concurrent runs no longer corrupt entries
- `release.json` schema updated to the 1.0.2 specification, which requires an `interface` value
- Dependencies updated and the Go toolchain raised to 1.26
- Doc comments audited across the codebase, correcting several that no longer matched their code

### Fixed

- Addons whose latest release is broken are no longer skipped entirely, reviving the repository exceptions list
- Writing CSV no longer panics when an addon has no last-seen date
- Project IDs with a value of `0` are ignored, as several addons set this by mistake
- The `X-RateLimit-Reset` header is now read correctly outside of search results

## [1.0.0] - 2024-05-05

### Added

- Initial release. Searches Github for World of Warcraft addons and writes a catalogue as CSV or JSON
- Addon metadata is read from `release.json` files, falling back to the `.toc` files inside release assets
- Game flavours are detected from `.toc` filenames and interface versions, with aliases normalised
- Results from previous runs can be supplied with `--in` to build upon, and `--filter` limits a run to matching addons
- HTTP responses are cached to disk, with `--use-expired-cache` to reuse entries past their expiry
- Repository blacklist, excluding forks, templates, addon bundles and projects that are not addons
