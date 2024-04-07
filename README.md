# github-wow-addon-catalogue

Scrapes WoW addon information from Github repositories.

A translation of the Python [layday/github-wow-addon-catalogue](https://github.com/layday/github-wow-addon-catalogue) to Go.

## Usage

    ADDONS_CATALOGUE_GITHUB_TOKEN=<your-token> ./github-wow-addon-catalogue > addons.csv

The catalogue is written to `stdout` and logging to `stderr`.

To build upon the results of a previous scrape:

    ADDONS_CATALOGUE_GITHUB_TOKEN=<your-token> ./github-wow-addon-catalogue --in old.csv > new.csv

## Licence

Copyright © 2024 Torkus

Distributed under the GNU Affero General Public Licence.
