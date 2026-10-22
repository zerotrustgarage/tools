#!/bin/sh
# makensis !finalize and !uninstfinalize hook: sign one file in place through sign.mjs.
# It inherits the signing step's environment (credentials, ZTG_SIGN_NAME, ZTG_SIGN_URL).
set -eu
exec node "$(dirname "$0")/sign.mjs" sign "$1"
