#!/bin/sh
# checkdev.sh — verify the open-sds1000cml boot stick + agent are healthy on a
# live device, over the agent's TCP control port (no on-device shell needed).
#
# It confirms the chain the takeover depends on:
#   vendor firmware  ->  runs startup.sh from the FAT32 USB stick  ->  agent  ->  app
#
# USAGE
#   ota/checkdev.sh <ip>[:port]         (port defaults to 5900)
#
# Exits non-zero if any check fails, so it can gate a deploy.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
otactl="$here/dist/otactl"
[ -x "$otactl" ] || { echo "checkdev: build otactl first (make -C ota otactl)" >&2; exit 2; }

addr=${1:-}
[ -n "$addr" ] || { echo "usage: checkdev.sh <ip>[:port]" >&2; exit 2; }
case "$addr" in *:*) : ;; *) addr="$addr:5900" ;; esac
D="-tcp $addr -timeout 10"

pass=0 fail=0
ok()   { echo "  PASS  $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $*"; fail=$((fail+1)); }
# otactl sh appends an "[exit N]" trailer — strip it so it doesn't leak into captures.
dsh()  { $otactl $D sh "$1" 2>/dev/null | sed 's/\[exit [0-9-]*\]//g'; }

echo "== open-sds1000cml device check: $addr =="

# 1. Agent reachable on the control port (this alone proves startup.sh ran the agent).
if $otactl $D ping >/dev/null 2>&1; then
  ok "agent answers on $addr (startup.sh -> agent chain is up)"
else
  bad "agent does NOT answer on $addr — stick not booted, wrong IP, or agent crashed"
  echo "     hint: power-cycle with the stick inserted; check the same subnet."
  echo "== $pass passed, $fail failed =="; exit 1
fi

status=$($otactl $D status 2>/dev/null || echo '{}')
getj() { printf '%s' "$status" | python3 -c "import sys,json;d=json.load(sys.stdin)
p='$1'.split('/');
for k in p:
  d=d.get(k,{}) if isinstance(d,dict) else {}
print(d if not isinstance(d,dict) else '')" 2>/dev/null; }

# 2. FAT32 USB stick mounted at the vendor U-disk mountpoint.
mnt=$(dsh 'mount | grep " /usr/bin/siglent/usr/media/U-disk0 "' | grep -i vfat | head -1)
if [ -n "$mnt" ]; then
  ok "FAT32 stick mounted at U-disk0 ($(printf '%s' "$mnt" | awk '{print $1}'))"
else
  bad "no FAT32 stick at /usr/bin/siglent/usr/media/U-disk0"
fi

# 3. Required boot files present on the stick.
missing=$(dsh '
  d=/usr/bin/siglent/usr/media/U-disk0
  for f in startup.sh commands ota/agent.env ota/agent.active agent-slots; do
    [ -e "$d/$f" ] || echo "$f"
  done')
if [ -z "$(printf '%s' "$missing" | tr -d '[:space:]')" ]; then
  ok "stick has startup.sh + ota/ + agent-slots/"
else
  bad "stick missing: $(printf '%s' "$missing" | tr '\n' ' ')"
fi

# 4. Boot log shows startup.sh reached the agent.
if $otactl $D logs boot 2>/dev/null | grep -q agent-start; then
  ok "boot.log shows agent-start (startup.sh ran to completion)"
else
  bad "boot.log has no agent-start marker"
fi

# 5. Agent status fields (active slot + watchdog) — informational + sanity.
active=$(getj slots/active); wd=$(getj watchdog/armed); taken=$(getj taken_over)
echo "  info  active app slot: ${active:-?} | watchdog armed: ${wd:-?} | taken over: ${taken:-?}"
[ -n "$active" ] && ok "agent reports an active app slot ($active)" || bad "agent has no active app slot"

# 6. If taken over, the app should be serving the web UI.
if [ "${taken:-}" = "True" ] || [ "${taken:-}" = "true" ]; then
  ip=${addr%%:*}
  if curl -s -m 4 "http://$ip:8080/api/status" >/dev/null 2>&1; then
    ok "clean-room app web UI answers on http://$ip:8080/"
  else
    bad "taken over but the app web UI ($ip:8080) does not answer"
  fi
else
  echo "  info  not taken over (coexisting with factory app) — run: otactl $D takeover"
fi

echo "== $pass passed, $fail failed =="
[ "$fail" = 0 ]
