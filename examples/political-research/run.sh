#!/usr/bin/env sh
set -eu
name="$(basename "$(pwd)")"
factvault example load "$name"
