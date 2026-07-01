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
# The dev PAM service (newtron-test) is pam_permit.so — it accepts any
# password — so the placeholder below is fine for local/dev. Against a
# real PAM stack, each user needs a real account and matching password.
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
