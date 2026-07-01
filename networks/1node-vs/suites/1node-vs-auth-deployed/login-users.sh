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
# The dev PAM service (newtron-test) is pam_permit.so — it accepts any
# password without consulting the OS account database — so the placeholder
# below is fine for local/dev and the accounts need not even exist. Against
# a real PAM stack (pam_unix/pam_sss), each user needs a real nologin
# account whose password matches the one sent here.
#
# Usage:
#   sh networks/1node-vs/suites/1node-vs-auth-deployed/login-users.sh [server-url]

set -eu

SERVER="${1:-http://127.0.0.1:18080}"
PASSWORD="${NEWTRON_TEST_PASSWORD:-test123}"

for user in ron mallory; do
    printf '%s\n' "$PASSWORD" | bin/newtron --server "$SERVER" auth login --user "$user" \
        || { echo "login failed for $user" >&2; exit 1; }
done

echo "Sessions cached. Run the suite with:"
echo "  NEWTRON_USER=ron bin/newtrun start 1node-vs-auth-deployed"
