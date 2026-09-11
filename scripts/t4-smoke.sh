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

# ------------------------------------------------- stray-instance self-heal
# The trap: ANY D-Bus call while the unit is down activates an unmanaged fcitx5
# which owns org.fcitx.Fcitx5, so the unit's own instances exit 0 immediately
# and Restart=always loops them into start-limit-hit. `systemctl start` still
# returns 0, so install must clear the stray rather than report success.
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

must "doctor" ompinyin doctor
ok "doctor clean"

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

# ---------------------------------------------------------------- manual items
cat <<'EOF'
  still manual (§15): F4 schema switch, Alt+Space tri-state, top-bar icon visible,
  SIGINT inside a stop window restarts fcitx5, backup rollback, --dsp grammar in both schemas.
EOF
ok "T4 smoke passed"
