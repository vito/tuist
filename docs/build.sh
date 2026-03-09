#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
go run . -i lit/index.lit -o . --html-templates html "$@"
