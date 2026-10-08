#!/bin/sh
# ENGMODEL-OWNER-UNIT: FU-TOOLS
# deploy.sh — build the release app (all four FPGA images embedded) and install it
# on the instrument through the OTA agent.
#
#   tools/deploy.sh            # build + update-app + status
#   DEV=192.168.1.209 tools/deploy.sh
#   tools/deploy.sh rollback A # activate the previous slot
#
# The staging dir must be under the U-disk (/tmp is read-only on the device).
set -eu
DEV=${DEV:-192.168.1.209}
PORT=${PORT:-5900}
STAGE=${STAGE:-/usr/bin/siglent/usr/media/U-disk0/agent-slots/staging}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OTACTL=$ROOT/ota/dist/otactl

if [ ! -x "$OTACTL" ]; then
	echo "deploy: building otactl"
	make -C "$ROOT/ota" otactl
fi

case "${1:-deploy}" in
rollback)
	SLOT=${2:?"rollback needs a slot (A|B)"}
	exec "$OTACTL" -tcp "$DEV:$PORT" activate "$SLOT"
	;;
power-cycle)
	exec "$OTACTL" -shelly "${SHELLY:-192.168.1.223}" power cycle
	;;
deploy)
	make -C "$ROOT/app" app-release
	echo "deploy: update-app -> $DEV:$PORT (stage $STAGE)"
	"$OTACTL" -tcp "$DEV:$PORT" -stage "$STAGE" update-app "$ROOT/app/dist/app-arm"
	sleep 3
	"$OTACTL" -tcp "$DEV:$PORT" status
	;;
*)
	echo "usage: $0 [deploy|rollback A|B|power-cycle]" >&2
	exit 2
	;;
esac
