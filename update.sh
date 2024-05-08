#!/bin/bash
set -e
mkdir -p output
curl -s https://raw.githubusercontent.com/ogri-la/github-wow-addon-catalogue/develop/addons.csv > previous-addons.csv
curl -s https://raw.githubusercontent.com/layday/github-wow-addon-catalogue/main/addons.csv > layday-addons.csv
ADDONS_CATALOGUE_GITHUB_TOKEN=$(cat github-token) go run . \
    scrape \
    --in previous-addons.csv \
    --in layday-addons.csv \
    --out addons.csv \
    --out addons.json \
    $@
