#!/bin/sh
# Create the OS accounts the 1node-vs-auth suite references via
# scenario-level `as: <user>`. Every user is created with /usr/sbin/nologin
# as their shell — these are PAM-authentication targets only, never
# accounts meant for an interactive login. The matching PAM service
# (`newtron-test`) uses pam_permit.so so no password is set; whatever
# `login-all.sh` sends is accepted.
#
# Idempotent: if a user already exists, the script leaves it alone
# rather than reconfiguring the shell on a possibly-real account.
#
# Usage:
#   sudo sh networks/1node-vs-auth/suites/1node-vs-auth/create-test-users.sh
#
# After this script runs, the matching tear-down (e.g. for CI cleanup)
# is `sudo userdel -r <name>` per user.

set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "must run as root (use: sudo $0)" >&2
    exit 1
fi

NOLOGIN="/usr/sbin/nologin"
if [ ! -x "$NOLOGIN" ] && [ -x /sbin/nologin ]; then
    NOLOGIN=/sbin/nologin
fi

USERS="alice arch-anna bob charlie dave dev-dora iam-ian intf-isaac mallory svc-sam"

for user in $USERS; do
    if id "$user" >/dev/null 2>&1; then
        existing_shell=$(getent passwd "$user" | cut -d: -f7)
        if [ "$existing_shell" = "$NOLOGIN" ] || [ "$existing_shell" = "/bin/false" ]; then
            echo "  $user: exists (shell=$existing_shell, ok)"
        else
            echo "  $user: exists (shell=$existing_shell, NOT nologin — leaving alone; reconfigure manually if needed)"
        fi
        continue
    fi
    useradd --create-home --shell "$NOLOGIN" "$user"
    echo "  $user: created (shell=$NOLOGIN)"
done

echo ""
echo "Verification:"
for user in $USERS root; do
    shell=$(getent passwd "$user" 2>/dev/null | cut -d: -f7)
    if [ -z "$shell" ]; then
        echo "  $user: MISSING"
    else
        echo "  $user: shell=$shell"
    fi
done
