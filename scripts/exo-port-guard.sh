#!/bin/bash
# exo-port-guard: lets only this Mac and your cluster's Macs reach exo's API port.
#
# exo serves its API, admin routes and cluster traffic on one port (52415) on
# every network interface, without a password, and its /state route lists the
# prompts of running requests. This script adds a macOS packet filter (pf) rule
# that allows that port only from localhost and the addresses you name (your
# other exo Macs; give them DHCP reservations so their addresses don't change),
# and drops it for everyone else on your network. It doesn't change exo or any
# other pf rule, and it runs again at every start through a small LaunchDaemon.
#
#   sudo ./exo-port-guard.sh install 192.168.1.21 192.168.1.22   # the other Macs
#   sudo ./exo-port-guard.sh status
#   sudo ./exo-port-guard.sh remove
#
# Run it on every Mac in the cluster, each time naming the others.
# Environment: EXO_PORT (default 52415).
set -euo pipefail

PORT=${EXO_PORT:-52415}
ANCHOR=com.apple/podtorrent-exo-guard
RULES=/etc/podtorrent/exo-port-guard.conf
LABEL=com.podtorrent.exo-port-guard
PLIST=/Library/LaunchDaemons/$LABEL.plist

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
need_root() { [ "$(id -u)" = 0 ] || { echo "run with sudo" >&2; exit 1; }; }

valid_addr() {
	# IPv4 or IPv6 address, optionally with a /prefix: digits, hex, dots, colons.
	[[ $1 =~ ^[0-9A-Fa-f:.]+(/[0-9]{1,3})?$ ]]
}

load() {
	pfctl -E >/dev/null 2>&1 || true # enable pf (stays on until restart)
	pfctl -a "$ANCHOR" -f "$RULES"
}

case "${1:-}" in
install)
	need_root
	shift
	peers=()
	for a in "$@"; do
		valid_addr "$a" || { echo "not an IP address: $a" >&2; exit 1; }
		peers+=("$a")
	done
	mkdir -p "$(dirname "$RULES")"
	{
		echo "# Written by exo-port-guard.sh. Allows exo's port $PORT only from these addresses."
		echo "table <exo_cluster> const { 127.0.0.1, ::1${peers[*]:+, $(IFS=,; echo "${peers[*]}" | sed 's/,/, /g')} }"
		echo "pass in quick on lo0 proto tcp to any port $PORT"
		echo "pass in quick proto tcp from <exo_cluster> to any port $PORT"
		echo "block drop in quick proto tcp to any port $PORT"
	} >"$RULES"
	chmod 644 "$RULES"
	pfctl -n -a "$ANCHOR" -f "$RULES" # check the rules before loading them
	cat >"$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key>
	<array><string>/bin/sh</string><string>-c</string><string>/sbin/pfctl -E; /sbin/pfctl -a $ANCHOR -f $RULES</string></array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
PLIST
	chmod 644 "$PLIST"
	launchctl bootout system "$PLIST" 2>/dev/null || true
	launchctl bootstrap system "$PLIST"
	load
	echo "exo port $PORT now allowed only from: localhost${peers[*]:+ ${peers[*]}}"
	;;
status)
	need_root
	if [ -f "$RULES" ]; then echo "rules file: $RULES"; else echo "not installed"; exit 1; fi
	echo "pf: $(pfctl -s info 2>/dev/null | awk '/Status:/ {print $2}')"
	echo "loaded rules:"
	pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/  /'
	pfctl -a "$ANCHOR" -t exo_cluster -T show 2>/dev/null | sed 's/^/  allowed: /'
	;;
remove)
	need_root
	launchctl bootout system "$PLIST" 2>/dev/null || true
	rm -f "$PLIST"
	pfctl -a "$ANCHOR" -F all 2>/dev/null || true
	rm -f "$RULES"
	echo "removed; exo's port is open to your network again"
	;;
*) usage ;;
esac
