# github-wow-addon-catalogue

Searches for WoW addons hosted on Github.

A translation of the Python [layday/github-wow-addon-catalogue](https://github.com/layday/github-wow-addon-catalogue) to Go.

Addon metadata is read from a repository's [release.json](https://github.com/ogri-la/release.json-specification)
file, falling back to the `.toc` files inside the release assets.

## Installation

```
git clone https://github.com/ogri-la/github-wow-addon-catalogue-go
cd github-wow-addon-catalogue-go
./manage.sh build
export ADDONS_CATALOGUE_GITHUB_TOKEN=<your-token>
```

A Github token is required for the `scrape` subcommand. The other subcommands
read the local cache and need no token.

## Usage

All functionality sits behind a subcommand:

    $ ./github-wow-addon-catalogue <scrape|dump-release-dot-json|find-duplicates|cache-stats|cache-prune>

Global flags are `--help`, `--version` and `--log-level` (one of `debug`,
`info`, `warn`, `error`, defaulting to `info`).

### `scrape`

The catalogue is written to `stdout` and logging to `stderr`:

    $ ./github-wow-addon-catalogue scrape

CSV and JSON output formats are supported, chosen by file extension, with JSON
as the default. `--out` may be given more than once:

    $ ./github-wow-addon-catalogue scrape --out addons.csv --out addons.json

To build upon the results of a previous run. `--in` may also be given more than
once and its results are merged with the search results:

    $ ./github-wow-addon-catalogue scrape --in old.csv --out new.csv

To use the input files only and not search Github at all:

    $ ./github-wow-addon-catalogue scrape --in old.csv --skip-search

To always use cached responses, even if they've expired:

    $ ./github-wow-addon-catalogue scrape --use-expired-cache

To limit parsing to just addons with names matching a pattern:

```bash
$ ./github-wow-addon-catalogue scrape --filter '^AdiAddons'
Aug 15 22:16:30.107 INF final addons num=3 num-input-files=1 filtered=true
Aug 15 22:16:30.107 INF de-duplicated addons num=3 unique=3
Aug 15 22:16:30.107 INF parsing addons
Aug 15 22:16:30.107 INF parsing repo repo=AdiAddons/AdiBags
Aug 15 22:16:30.107 INF parsing repo repo=AdiAddons/AdiButtonAuras
Aug 15 22:16:30.108 INF parsing repo repo=AdiAddons/LibPlayerSpells-1.0
Aug 15 22:16:30.111 INF parsing .toc filename=LibPlayerSpells-1.0/LibPlayerSpells-1.0.toc
Aug 15 22:16:30.112 INF parsing .toc filename=AdiButtonAuras/AdiButtonAuras.toc
Aug 15 22:16:30.118 INF parsing .toc filename=AdiBags/AdiBags.toc
Aug 15 22:16:30.118 INF parsing .toc filename=AdiBags_Config/AdiBags_Config.toc
Aug 15 22:16:30.118 INF addons parsed num=3 viable=3
[
	{
		"id": 639034,
		"name": "AdiBags",
		"full_name": "AdiAddons/AdiBags",
		"html_url": "https://github.com/AdiAddons/AdiBags",
		"description": "WoW Addon — Adirelle's bag addon.",
		"updated-date": "2024-09-08T02:20:23Z",
		"flavor-list": [
			"mainline",
			"vanilla",
			"tbc",
			"wrath"
		],
		"has-release-json": true,
		"downloads": 1099644,
		"release-count": 56
	},
	{
		"id": 14010348,
		"name": "AdiButtonAuras",
		"full_name": "AdiAddons/AdiButtonAuras",
		"html_url": "https://github.com/AdiAddons/AdiButtonAuras",
		"description": "WoW addon - Display auras on action buttons.",
		"project-id-map": {
			"x-curse-project-id": "68441"
		},
		"updated-date": "2025-10-31T15:59:16Z",
		"flavor-list": [
			"mainline"
		],
		"has-release-json": true,
		"downloads": 35938,
		"release-count": 24
	},
	{
		"id": 14625469,
		"name": "LibPlayerSpells-1.0",
		"full_name": "AdiAddons/LibPlayerSpells-1.0",
		"html_url": "https://github.com/AdiAddons/LibPlayerSpells-1.0",
		"description": "WoW addon library - additional information about player spells.",
		"project-id-map": {
			"x-curse-project-id": "72228"
		},
		"updated-date": "2024-10-19T20:50:02Z",
		"flavor-list": [
			"mainline"
		],
		"has-release-json": true,
		"downloads": 2728,
		"release-count": 4
	}
]
```

The `flavor-list` values are `mainline`, `vanilla`, `tbc`, `wrath`, `cata` and
`mists`. The CSV form of the above uses the columns `id`, `name`, `full_name`,
`url`, `description`, `last_updated`, `flavors`, `curse_id`, `wago_id`,
`wowi_id`, `has_release_json`, `last_seen`, `downloads` and `release_count`.

### `find-duplicates`

Reports addons sharing a project ID across different owners, which usually
means a fork has kept the original's Curse, Wago or WoWInterface ID. Bundles
under a single owner and a list of known cases are skipped:

    $ ./github-wow-addon-catalogue find-duplicates --in addons.csv

### `dump-release-dot-json`

Emits the `release.json` files held in the cache as JSON lines, for use by the
[release.json-validator](https://github.com/ogri-la/release.json-specification):

    $ ./github-wow-addon-catalogue dump-release-dot-json > dump.jsonl

### `cache-stats` and `cache-prune`

HTTP responses are cached to `./output`. Search results expire after two hours
and other responses after 24 hours, while zipfile entries, `release.json` files
and release page listings never expire.

To report the size and age of the cache, broken down by entry type:

    $ ./github-wow-addon-catalogue cache-stats

To remove expired entries. This is a dry run unless `--delete=true` is given:

    $ ./github-wow-addon-catalogue cache-prune
    $ ./github-wow-addon-catalogue cache-prune --delete=true

## Development

`manage.sh` handles the routine tasks:

    $ ./manage.sh build     # build the binary
    $ ./manage.sh test      # run the tests
    $ ./manage.sh lint      # go mod tidy and go fix
    $ ./manage.sh update    # update the catalogue from its sources

`debug.sh` re-parses a single addon from the cache with debug logging:

    $ ./debug.sh '^AdiAddons/AdiBags$'

## Licence

Copyright © 2024 Torkus

Distributed under the GNU Affero General Public Licence.
