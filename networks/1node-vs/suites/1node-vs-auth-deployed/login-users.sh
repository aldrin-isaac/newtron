#!/bin/sh
# Cache a Bearer session for every identity this suite references via
# `as:`, so `bin/newtrun start 1node-vs-auth-deployed` can authenticate
# each scenario. Two identities:
#
#   ron     — global super-user (server started with --super-users ron);
#             drives the positive cross-engine scenarios (00, 10).
#   mallory — authenticated but ungranted; the negative scenario (20)
#             expects her actuated write to be denied (403).
#
# Both ron and mallory are nologin OS accounts (/usr/sbin/nologin): they
# authenticate to the newtron API through PAM but must never grant an
# interactive host shell — see README.md "Identities" for the rationale
# and the useradd recipe that creates them.
#
# Each identity has its OWN password (per-user env override below). Under a
# real PAM stack (pam_unix/pam_sss — e.g. the newt-server service) the value
# sent must match each account's actual credential; a single shared password
# only ever worked under the pam_permit test service, which verifies nothing.
# Note: hosts with a password-quality policy may refuse short passwords at
# chpasswd time (e.g. "shorter than 8 characters") — set the account password
# to something compliant and pass it via the matching env var.
#
# Usage:
#   sh networks/1node-vs/suites/1node-vs-auth-deployed/login-users.sh [server-url]

set -eu

SERVER="${1:-http://127.0.0.1:18080}"
RON_PASSWORD="${NEWTRON_RON_PASSWORD:-ronthenewt}"
MALLORY_PASSWORD="${NEWTRON_MALLORY_PASSWORD:-test123}"

login() {
    printf '%s\n' "$2" | bin/newtron --server "$SERVER" auth login --user "$1" \
        || { echo "login failed for $1" >&2; exit 1; }
}

login ron     "$RON_PASSWORD"
login mallory "$MALLORY_PASSWORD"

echo "Sessions cached. Run the suite with:"
echo "  NEWTRON_USER=ron bin/newtrun start 1node-vs-auth-deployed"
