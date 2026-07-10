#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Recreate the ISO's passwd users inside the Layer-2 test container so it behaves
# like a freshly-installed RudderVirt OS (admin login, default password, wheel/sudo)
# instead of a bare root shell. On the real ISO these users come from the Ignition
# `passwd` section, not from the file overlay, so `make test-container` has to add
# them itself.
#
# Users are passed in via $USERS_SPEC, one per line, pipe-separated:
#     name|comma,separated,groups|login-shell|password-hash
# The Makefile derives this from the *generated* out/server.ign, so the test
# container never drifts from what actually ships. '|' is used (not tab) so that
# `read` keeps empty fields instead of collapsing runs of IFS whitespace.
set -euo pipefail

# The base image ships no mail spool (MAIL_DIR in /etc/login.defs); create it so
# useradd doesn't emit a confusing "Creating mailbox file: No such file or
# directory" warning for every user.
mkdir -p /var/spool/mail

while IFS='|' read -r name groups shell hash; do
  [ -n "$name" ] || continue

  # root already exists in the base image; only its password needs setting.
  if [ "$name" != root ] && ! id "$name" >/dev/null 2>&1; then
    useradd -m "$name"
  fi

  if [ -n "$groups" ]; then
    IFS=, read -ra glist <<< "$groups"
    for g in "${glist[@]}"; do
      getent group "$g" >/dev/null 2>&1 || groupadd "$g"
    done
    usermod -aG "$groups" "$name"
  fi

  [ -n "$shell" ] && usermod -s "$shell" "$name"
  [ -n "$hash" ] && printf '%s:%s\n' "$name" "$hash" | chpasswd -e
done <<< "${USERS_SPEC:-}"

# `while read` returns 1 at EOF; end on a clean status so callers chaining with
# `&&` (the Makefile recipe) don't treat a successful run as a failure.
exit 0
