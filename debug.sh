#!/bin/bash
set -eux
clear
ADDONS_CATALOGUE_GITHUB_TOKEN=$(cat github-token) go run . scrape \
    --in imports.json \
    --in addons.csv \
    --skip-search \
    --use-expired-cache \
    --log-level debug \
    --filter "$1"
