#!/usr/bin/env bash
# A scripted stand-in for the `workshop` snap, honouring the status model of
# design §2.2 so that the whole of §5.3 can run hermetically.
#
# State lives in $WT_STUB_DIR:
#   status  one of off|ready|stopped|pending|waiting|error
#   source  the current host-source of the mount
#   log     one line per invocation, in order
set -u

dir="${WT_STUB_DIR:?WT_STUB_DIR must be set}"
name="${WT_STUB_NAME:-tools-dev}"
status_file="$dir/status"
source_file="$dir/source"

[[ -f "$status_file" ]] || echo off >"$status_file"
[[ -f "$source_file" ]] || echo "" >"$source_file"

status=$(<"$status_file")
host_source=$(<"$source_file")

# Record the invocation with the -p flag stripped, so assertions stay readable.
argv=()
for a in "$@"; do
	argv+=("$a")
done
echo "$*" >>"$dir/log"

cmd="${1:-}"
shift || true

# Drop -p <dir> wherever it appears.
args=()
while (($# > 0)); do
	case "$1" in
	-p | --project)
		shift 2
		;;
	*)
		args+=("$1")
		shift
		;;
	esac
done

die() {
	echo "workshop: $1" >&2
	exit 1
}

case "$cmd" in
list)
	# Off workshops are synthesised from the definition file (§2.2).
	printf '%s  %s  -\n' "$name" "$status"
	;;
info)
	[[ "$status" == off ]] && die "workshop \"$name\" does not exist"
	printf 'name:      %s\n' "$name"
	printf 'base:      ubuntu@24.04\n'
	printf 'project:   %s\n' "${WT_STUB_PROJECT:-$PWD}"
	if [[ "$status" != stopped ]]; then
		printf 'hostname:  %s.wp\n' "$name"
	fi
	printf 'status:    %s\n' "$status"
	printf 'notes:     --\n'
	printf 'sdks:\n'
	printf '  vscode-remote:\n'
	printf '    tracking:  latest/stable\n'
	printf '    installed: 1.2.3 2026-07-21 (42)\n'
	if [[ -n "$host_source" ]]; then
		printf '    mounts:\n'
		printf '      git-dir:\n'
		printf '        host-source:      %s\n' "$host_source"
		printf '        workshop-target:  %s\n' "${WT_STUB_TARGET:-$host_source}"
	fi
	;;
launch)
	[[ "$status" == off ]] || die "workshop \"$name\" is already launched"
	echo ready >"$status_file"
	# Newly bound plugs get an auto-allocated host directory (§1.3).
	echo "~/.local/share/workshop/id/ABCD/$name/mount/vscode-remote/git-dir" >"$source_file"
	echo "launched $name"
	;;
start)
	[[ "$status" == stopped ]] || die "workshop \"$name\" is not stopped (status $status)"
	echo ready >"$status_file"
	;;
stop)
	[[ "$status" == ready ]] || die "workshop \"$name\" is not started (status $status)"
	echo stopped >"$status_file"
	;;
refresh)
	[[ "$status" == ready ]] || die "cannot refresh a workshop in status $status"
	if [[ -z "$host_source" ]]; then
		echo "~/.local/share/workshop/id/ABCD/$name/mount/vscode-remote/git-dir" >"$source_file"
	fi
	;;
remount)
	# A populated source can only be swapped while stopped (C2).
	[[ "$status" == stopped ]] || die "cannot remount a populated source while status is $status"
	target="${args[0]:-}"
	src="${args[1]:-}"
	[[ "$target" == "$name/vscode-remote:git-dir" ]] || die "unknown mount \"$target\""
	[[ -n "$src" ]] || die "remount needs a source"
	echo "$src" >"$source_file"
	;;
*)
	die "unknown command \"$cmd\""
	;;
esac
exit 0
