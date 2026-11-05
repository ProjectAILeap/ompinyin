#!/usr/bin/env bash
# T4 pre-tag smoke (§15). Every check here needs the REAL host — systemd's
# Restart=always, the session bus, and the filesystem topology — which is
# exactly what the T0 stub suite normalises away by construction.
#
#   make smoke
#
# It converges this machine and briefly restarts fcitx5, so run it on your own
# or a throwaway Omarchy host, never on someone else's.
set -euo pipefail

ok()  { printf '\033[32m✓\033[0m %s\n' "$*"; }
red() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; }
die() { red "$*"; exit 1; }

UNIT=omarchy-fcitx5.service
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Evidence files, NOT under the work dir: a failing run must leave behind the
# output that says WHY. Swallowing it with `>/dev/null` once cost a whole
# debugging session on an unreproducible failure.
LOG=$(mktemp -t ompinyin-smoke.XXXXXX.log)
echo "  verbose log: $LOG"

command -v ompinyin >/dev/null || die "ompinyin is not in PATH"
grep -q '^ID=omarchy' /etc/os-release || die "not an Omarchy host (§15 T4)"

# Any command that must converge in ONE run: no retry, but print the failing
# lines instead of hiding them.
must() {
  local what=$1; shift
  if ! "$@" >>"$LOG" 2>&1; then
    red "$what failed (exit $?)"
    grep -E '\[失败\]' "$LOG" || true
    echo "  full log: $LOG" >&2
    exit 1
  fi
}

# ---------------------------------------------------------------- idempotence
# A converged host must report no work at all (§3 幂等零扰动).
needs=$(ompinyin install --dry-run --json | grep -o '"needsApply": *[a-z]*' | cut -d: -f2 | tr -d ' ')
[ "$needs" = "false" ] || die "converged host reports needsApply=$needs — run 'ompinyin install -y' first"
ok "converged re-run reports no work"

# ------------------------------------------- fcitx5 start path (stray / unit)
# What this step proves in both Omarchy generations: install leaves EXACTLY ONE
# fcitx5, and it is the supervised unit's own process holding
# org.fcitx.Fcitx5. HOW that is proved differs, because 4.5 changed D-Bus
# activation itself:
#
#   pre-4.5: any D-Bus call while the unit is down activates an UNMANAGED fcitx5
#     that owns the bus name; the unit's instances then exit 0 immediately and
#     Restart=always loops them into start-limit-hit. `systemctl start` still
#     returns 0, so install has to clear the stray instead of reporting success.
#   4.5+:    Omarchy installs ~/.local/share/dbus-1/services/
#     org.fcitx.Fcitx5.service with `SystemdService=omarchy-fcitx5.service`, so
#     activation starts the SUPERVISED unit — a stray is practically impossible.
#     The assertion flips to "activation is routed to the unit".
DBUS_UNIT_FILE="$HOME/.local/share/dbus-1/services/org.fcitx.Fcitx5.service"
if [ -f "$DBUS_UNIT_FILE" ] && grep -qx "SystemdService=$UNIT" "$DBUS_UNIT_FILE"; then
  # ---- 4.5+: D-Bus activation brings the managed unit back, not a stray ----
  systemctl --user stop "$UNIT"
  for _ in $(seq 1 50); do   # stop is asynchronous — wait, never sleep-and-guess
    [ "$(systemctl --user is-active "$UNIT")" = "active" ] || break
    sleep 0.1
  done
  [ "$(systemctl --user is-active "$UNIT")" != "active" ] \
    || die "$UNIT did not stop — cannot exercise D-Bus activation"

  fcitx5-remote >/dev/null 2>&1 || true   # the D-Bus call, now routed to the unit
  for _ in $(seq 1 50); do   # ~5s budget
    [ "$(systemctl --user is-active "$UNIT")" = "active" ] && break
    sleep 0.1
  done
  [ "$(systemctl --user is-active "$UNIT")" = "active" ] \
    || die "D-Bus activation did not start $UNIT within ~5s (is SystemdService= in $DBUS_UNIT_FILE stale?)"

  owner=$(busctl --user status org.fcitx.Fcitx5 | awk -F= '/^PID=/{print $2}')
  main=$(systemctl --user show -p MainPID --value "$UNIT")
  [ -n "$owner" ] && [ "$owner" = "$main" ] \
    || die "org.fcitx.Fcitx5 owner=${owner:-none} != unit MainPID=$main (activation spawned an unmanaged instance)"
  procs=$(pgrep -c -x fcitx5 || true)
  [ "$procs" = "1" ] || die "$procs fcitx5 processes alive after D-Bus activation, want exactly 1"

  # install must stay idempotent on this path too (§3 幂等零扰动)
  must "install (after D-Bus activation)" ompinyin install -y
  [ "$(systemctl --user is-active "$UNIT")" = "active" ] || die "unit down after install"
  procs=$(pgrep -c -x fcitx5 || true)
  [ "$procs" = "1" ] || die "$procs fcitx5 processes alive after install, want exactly 1"
  ok "D-Bus activation starts the supervised unit (4.5+: a stray cannot be produced); install stayed idempotent"
else
  # ---- pre-4.5: stage a stray and prove install clears it ----
  systemctl --user stop "$UNIT"
  sleep 1
  fcitx5-remote >/dev/null 2>&1 || true   # the D-Bus call that *creates* the stray
  # The activation is asynchronous — wait for a real owner instead of guessing.
  owner=""
  for _ in $(seq 1 50); do
    owner=$(busctl --user status org.fcitx.Fcitx5 2>/dev/null | awk -F= '/^PID=/{print $2}')
    [ -n "$owner" ] && break
    sleep 0.1
  done
  [ "$(systemctl --user is-active "$UNIT")" = "active" ] && die "unit came back on its own — cannot stage a stray"
  [ -n "$owner" ] && [ "$(pgrep -c -x fcitx5)" -ge 1 ] || die "no stray was activated — cannot stage a stray"
  must "install (clearing the stray)" ompinyin install -y
  [ "$(systemctl --user is-active "$UNIT")" = "active" ] || die "unit still down after install — the stray was not cleared"
  owner=$(busctl --user status org.fcitx.Fcitx5 | awk -F= '/^PID=/{print $2}')
  main=$(systemctl --user show -p MainPID --value "$UNIT")
  [ -n "$owner" ] && [ "$owner" = "$main" ] || die "org.fcitx.Fcitx5 owner=${owner:-none} != unit MainPID=$main"
  [ "$(pgrep -c -x fcitx5)" = "1" ] || die "$(pgrep -c -x fcitx5) fcitx5 processes alive, want exactly 1"
  ok "stray cleared; the unit owns org.fcitx.Fcitx5 (no Restart=always flap)"
fi

must "doctor" ompinyin doctor
ok "doctor clean"

# --------------------------------------------------- 4.5 hand-over invariants
# Cheap, GUI-free checks for the two ways a converged 4.5 host can still show the
# WRONG thing: a leftover drop-in reviving fcitx5's own SNI item (two bar icons),
# or our retired theme generator fighting omarchy-theme-set-fcitx5.
INDICATOR=/usr/share/omarchy/default/input-methods/indicator.py
if [ -e "$INDICATOR" ]; then
  DROPDIR="$HOME/.config/systemd/user/$UNIT.d"
  [ ! -e "$DROPDIR/ompinyin-notificationitem.conf" ] \
    || die "4.5 host still carries our notificationitem drop-in: $DROPDIR/ompinyin-notificationitem.conf"
  # NB: `if`/`fi`, not `grep && die` — under `set -e` a failing AND-list is fatal.
  if [ -f "$DROPDIR/override.conf" ] && grep -qE '^ExecStart=/?usr/bin/fcitx5( |$)' "$DROPDIR/override.conf"; then
    die "override.conf overrides Omarchy's ExecStart (fcitx5 publishes its own tray icon → two icons): $(grep -n ExecStart= "$DROPDIR/override.conf")"
  fi
  # the file-level check is not enough: systemd applies the drop-ins, and the
  # EFFECTIVE ExecStart is what decides whether fcitx5 announces itself.
  execstart=$(systemctl --user show "$UNIT" -p ExecStart --value)
  case "$execstart" in
    *"--disable notificationitem"*) ;;
    *) die "effective ExecStart lost --disable notificationitem: $execstart"
       ;;
  esac
  ok "Omarchy owns the tray: no stale drop-in/override, ExecStart keeps --disable notificationitem"

  # The candidate-window theme comes from omarchy-theme-set-fcitx5; our retired
  # generator's artifacts must be gone while Omarchy's files stay.
  THEMEDIR="$HOME/.local/share/fcitx5/themes/omarchy"
  [ -f "$THEMEDIR/theme.conf" ] || die "Omarchy's fcitx5 theme.conf is missing: $THEMEDIR"
  # Our generator wrote REGULAR files here; Omarchy's theme uses symlinks into
  # /usr/share/fcitx5/themes/default. So a regular file is ours (must be gone), a
  # symlink is Omarchy's (fine) — this stays truthful even if upstream starts
  # shipping a background.svg of its own.
  for f in background.svg highlight.svg; do
    if [ -f "$THEMEDIR/$f" ] && [ ! -L "$THEMEDIR/$f" ]; then
      die "our retired theme artifact is back: $THEMEDIR/$f (should be Omarchy's symlink or absent)"
    fi
  done
  ok "candidate window: Omarchy's theme only (no ompinyin hook artifacts)"
else
  echo "! pre-4.5 Omarchy (no $INDICATOR) — skipping the 4.5 hand-over invariants"
fi

# ------------------------------------------- self-upgrade across filesystems
# The release artifact path, and the bug that shipped in v1.2.1–v1.2.2: the
# download stages in $TMPDIR (tmpfs) while the binary lives on another mount
# (btrfs), and rename(2) cannot cross filesystems. Build a scratch binary
# labelled far in the past so the engine really replaces it, keep it on $HOME's
# filesystem, and require the result to match the published checksums.
work=$(mktemp -d "$HOME/.ompinyin-smoke.XXXXXX")
trap 'rm -rf "$work"' EXIT
arch=$(uname -m); [ "$arch" = "x86_64" ] && arch=amd64
asset=ompinyin_linux_$arch
scratch=$work/bin/ompinyin-selftest
mkdir -p "$work/bin"
go build -C "$ROOT" -ldflags "-X github.com/ProjectAILeap/ompinyin/internal/catalog.Version=0.0.1" -o "$scratch" ./cmd/ompinyin
if [ "$(df --output=source "$work" | tail -1)" = "$(df --output=source /tmp | tail -1)" ]; then
  echo "! $work and /tmp share a filesystem — this host cannot exercise the cross-mount case"
fi
must "update --self" "$scratch" update --self -y
ver=$("$scratch" version | awk '{print $2}')
latest=$(curl -fsSI -o /dev/null -w '%{redirect_url}' https://github.com/ProjectAILeap/ompinyin/releases/latest | sed 's#.*/tag/v##')
[ "$ver" = "$latest" ] || die "update --self left version $ver, want $latest"
want=$(curl -fsSL "https://github.com/ProjectAILeap/ompinyin/releases/download/v$latest/checksums.txt" | awk -v a="$asset" '$2==a{print $1}')
got=$(sha256sum "$scratch" | awk '{print $1}')
[ "$want" = "$got" ] || die "replaced binary sha256=$got, want $want"
[ -x "$scratch" ] || die "replaced binary lost its executable bit"
[ -z "$(find "$(dirname "$scratch")" -name '.*.new' -print -quit)" ] || die "staging litter left next to the binary"
ok "update --self replaced the binary across filesystems (0.0.1 → v$latest, sha256 verified)"

# `update --self` converges BEFORE upgrading, and the X11 publisher unit bakes
# the RUNNING binary's path (os.Executable) into ExecStart (§6.7). Run from the
# scratch path, that rewrites the unit to point inside $work — which the trap
# below deletes, leaving a watcher that cannot exec and a login publish that
# fails silently. Re-converge with the installed binary (the one users run) and
# assert the unit is back on a path that exists.
UNITFILE="$HOME/.config/systemd/user/ompinyin-x11-hidpi.service"
must "restore the publisher unit" ompinyin install -y
if grep -qF "$work" "$UNITFILE" 2>/dev/null; then
  die "publisher unit still points into the scratch dir: $(grep ExecStart= "$UNITFILE")"
fi
if [ -f "$UNITFILE" ]; then
  exec_path=$(sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$UNITFILE" | head -1)
  [ -x "$exec_path" ] || die "publisher ExecStart is not executable: $exec_path"
fi
must "doctor (after --self)" ompinyin doctor
ok "publisher unit restored to $exec_path; doctor still clean"

# ---------------------------------------------------------------- manual items
cat <<'EOF'
  still manual (§15), Omarchy 4.5+: F4 schema switch, Super+I cycles the input method,
  left Shift enters/leaves it, Ctrl+Space is left to the terminal (no IME toggle),
  EXACTLY ONE input-method indicator on the bar (not two), SIGINT inside a stop window
  restarts fcitx5, backup rollback, --dsp grammar in both schemas.
EOF
ok "T4 smoke passed"
