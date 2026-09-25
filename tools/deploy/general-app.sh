#!/bin/sh
# General acquisition release owns FPGA loading and its LCD progress display.
set -eu
bundle=/usr/bin/siglent/usr/media/U-disk0/agent-slots/staging
export SCOPE_CAPTURE=default-sram
export SCOPE_EDMA=1
export SCOPE_INTERLEAVE_CAL="$bundle/panel-interleave-cal.json"
exec "$bundle/general-release-app"
