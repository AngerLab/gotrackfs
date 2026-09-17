#!/bin/sh
# Docker launcher for gotrackfs.
# Mirrors trackfs' launcher.sh: FUSE requires that the user who mounts the
# filesystem has an entry in /etc/passwd. Since the uid is only known at
# runtime (docker --user), we add a fake entry for the current uid on the fly.

my_uid="$(id -u)"
if ! getent passwd "${my_uid}" >/dev/null 2>&1; then
    echo "gotrackfs:x:${my_uid}:$(id -g)::/tmp:/sbin/nologin" >> /etc/passwd
fi

src="$1"
dst="$2"
shift 2

# -allow-other is required so that the host user (outside the container)
# can see the mount through the /dst bind mount.
exec /usr/local/bin/gotrackfs -allow-other "$@" "$src" "$dst"
