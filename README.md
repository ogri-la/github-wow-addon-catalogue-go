# github-wow-addon-catalogue

Searches for WoW addons hosted on Github.

A translation of the Python [layday/github-wow-addon-catalogue](https://github.com/layday/github-wow-addon-catalogue) to Go.

## Installation

```
git clone https://github.com/ogri-la/github-wow-addon-catalogue-go
cd github-wow-addon-catalogue-go
go build .
export ADDONS_CATALOGUE_GITHUB_TOKEN=<your-token>
```

## Usage

The catalogue is written to `stdout` and logging to `stderr`:

    $ ./github-wow-addon-catalogue

CSV and JSON output formats are supported, with JSON as the default.

    $ ./github-wow-addon-catalogue --out addons.csv

To build upon the results of a previous run:

    $ ./github-wow-addon-catalogue --in old.csv --out new.csv

To always use cached responses, even if they've expired:

    $ ./github-wow-addon-catalogue --use-expired-cache

To limit parsing to just addons with names matching a pattern:

```bash
$ ./github-wow-addon-catalogue --filter '^AdiAddons'
May  4 00:51:01.342 INF searching for addons
May  4 00:51:02.483 INF found addons num=3
May  4 00:51:02.483 INF de-duplicating addons num=3
May  4 00:51:02.483 INF unique addons num=3
May  4 00:51:02.483 INF parsing addons
May  4 00:51:02.483 INF parsing repo repo=AdiAddons/AdiBags
May  4 00:51:02.483 INF parsing repo repo=AdiAddons/AdiButtonAuras
May  4 00:51:02.483 INF parsing repo repo=AdiAddons/LibPlayerSpells-1.0
May  4 00:51:02.484 INF parsing .toc filename=AdiButtonAuras/AdiButtonAuras.toc
May  4 00:51:02.484 INF parsing .toc filename=AdiButtonAuras_Config/AdiButtonAuras_Config.toc
May  4 00:51:02.484 INF parsing .toc filename=LibPlayerSpells-1.0/LibPlayerSpells-1.0.toc
May  4 00:51:02.485 INF parsing .toc filename=AdiBags/AdiBags.toc
May  4 00:51:02.485 INF parsing .toc filename=AdiBags_Config/AdiBags_Config.toc
May  4 00:51:02.485 INF addons parsed num=3 viable=3
[
	{
		"id": 639034,
		"name": "AdiBags",
		"full_name": "AdiAddons/AdiBags",
		"html_url": "https://github.com/AdiAddons/AdiBags",
		"description": "WoW Addon — Adirelle's bag addon.",
		"updated-date": "2024-04-12T20:22:01Z",
		"flavor-list": [
			"mainline",
			"vanilla",
			"tbc",
			"wrath"
		],
		"has-release-json": true
	},
	{
		"id": 14010348,
		"name": "AdiButtonAuras",
		"full_name": "AdiAddons/AdiButtonAuras",
		"html_url": "https://github.com/AdiAddons/AdiButtonAuras",
		"description": "WoW addon - Display auras on action buttons.",
		"updated-date": "2024-01-26T20:13:52Z",
		"flavor-list": [
			"mainline"
		],
		"project-id-map": {
			"x-curse-project-id": "68441"
		},
		"has-release-json": true
	},
	{
		"id": 14625469,
		"name": "LibPlayerSpells-1.0",
		"full_name": "AdiAddons/LibPlayerSpells-1.0",
		"html_url": "https://github.com/AdiAddons/LibPlayerSpells-1.0",
		"description": "WoW addon library - additional information about player spells.",
		"updated-date": "2023-05-06T19:54:08Z",
		"flavor-list": [
			"mainline"
		],
		"project-id-map": {
			"x-curse-project-id": "72228"
		},
		"has-release-json": true
	}
]
```

## Licence

Copyright © 2024 Torkus

Distributed under the GNU Affero General Public Licence.
