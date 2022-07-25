# Copyright (c) 2020 SMARTX
# All rights reserved.

#!/usr/bin/env bash

set -e

mkdir -p /host

/usr/sbin/csi-driver "$@"
