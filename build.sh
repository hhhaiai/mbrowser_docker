#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${GOSUMDB:-}" ]]; then
  export GOSUMDB="sum.golang.org"
fi

go mod tidy
go build -o miui_proxy .
echo "Built ./miui_proxy"

