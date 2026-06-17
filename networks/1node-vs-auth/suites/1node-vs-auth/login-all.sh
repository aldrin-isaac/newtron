#!/bin/sh
# Log in as every identity 1node-vs-auth's scenarios reference, so
# `bin/newtrun start 1node-vs-auth` has a session cached for every
# scenario's `as: <user>` reference.
#
# The PAM server in this suite's operator setup uses pam_permit.so
# (accepts any password), so the literal password below is a
# placeholder — replace with whatever your PAM stack expects. For
# every CI-style deployment with a real PAM module (pam_unix,
# pam_sss, etc.) each test user needs a real OS / directory account
# with the password you supply here.
#
# Usage:
#   sh networks/1node-vs-auth/suites/1node-vs-auth/login-all.sh [server-url]
#
# server-url defaults to http://127.0.0.1:18080.

set -eu

SERVER="${1:-http://127.0.0.1:18080}"

# Every distinct identity any scenario in this suite references via
# `as:`. Keep alphabetized so additions are obvious in diffs.
USERS="alice arch-anna bob charlie dave dev-dora iam-ian intf-isaac mallory root svc-sam"

# Placeholder password — every test user gets the same one because
# pam_permit accepts anything. Override with the env var
# NEWTRON_TEST_PASSWORD when running against a stricter PAM stack
# (and ensure every user account uses the same password, or replace
# this script with per-user logins).
PASSWORD="${NEWTRON_TEST_PASSWORD:-test123}"

for user in $USERS; do
    printf '%s\n' "$PASSWORD" | bin/newtron --server "$SERVER" auth login --user "$user" \
        || { echo "login failed for $user" >&2; exit 1; }
done

echo "All sessions cached. Run the suite with:"
echo "  bin/newtrun start 1node-vs-auth --no-deploy --network-id 1node-vs-auth"
