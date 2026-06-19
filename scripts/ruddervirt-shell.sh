#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Login shell for the admin user. An interactive login (console or SSH) launches
# the RudderVirt setup menu; everything else (scp, sftp, `ssh host <cmd>`,
# `su -c`, or the menu's shell escape) behaves like a normal bash login shell.
set -uo pipefail

# Non-interactive command execution: `ruddervirt-shell -c "<cmd>"` (scp, the
# shell-based path of sftp, `ssh host <cmd>`, `su admin -c ...`). Run under bash.
if [ "${1:-}" = "-c" ]; then
  exec /bin/bash "$@"
fi

# Launch the setup menu only for a real interactive login that has not already
# escaped to a shell, and only when the menu is actually available.
if [ -z "${RUDDERVIRT_SHELL:-}" ] && [ -t 0 ] && [ -x /usr/local/bin/ruddervirt-setup ]; then
  exec /usr/local/bin/ruddervirt-setup
fi

# Fall back to a normal login shell.
exec /bin/bash -l "$@"
