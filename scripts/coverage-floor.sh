#!/bin/sh
# Fail when total statement coverage in a Go cover profile drops below the floor.
# Usage: scripts/coverage-floor.sh [profile]   (COVER_MIN overrides the floor)
#
# The floor sits a little under the measured total so ordinary churn passes and
# a change that lands untested code in bulk does not. Raise it when coverage rises.
set -eu

profile="${1:-coverage/total.coverprofile}"
floor="${COVER_MIN:-72}"

total="$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $3); print $3 }')"
if [ -z "$total" ]; then
	echo "coverage-floor: no total in $profile" >&2
	exit 1
fi

if awk -v t="$total" -v f="$floor" 'BEGIN { exit !(t + 0 < f + 0) }'; then
	echo "coverage-floor: total ${total}% is below the ${floor}% floor" >&2
	exit 1
fi
echo "coverage-floor: total ${total}% (floor ${floor}%)"
