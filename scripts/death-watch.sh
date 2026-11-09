#!/bin/bash
# death-watch.sh — a PASSIVE external observer for the LIVE ccx daemon
# (~/.ccx, port from the same resolution as ccx stop: daemon.port > 4567).
# It changes nothing and interferes with nothing; it only watches and logs
# into <CCX_HOME>/logs/death-watch.log.
#
# Why it exists: the 2026-11-09 silent-death hunt showed the daemon dying
# with zero output of its own. The instrumented daemon now logs every
# graceful exit, signal and admin request — but a SIGKILL kills it before
# it can say ANYTHING. Only an outside observer can timestamp that death,
# and an exact timestamp is what makes the system unified log queryable
# afterwards (`log show --start <ts> --end <ts>+10s`).
#
# State machine (a line is written only on TRANSITIONS, not every tick):
#   daemon up pid=<N>           — the pidfile appeared / pid changed
#   health refused pid=<N>      — process alive, /health not answering
#   daemon DIED at <ts> pid=<N> — the pid vanished while pidfile remained
#   daemon stopped (pidfile removed) — a clean exit or a switch stop
#   snapshot: ...               — ps/lsof captured at DIED/refused moments
#
# Usage: scripts/death-watch.sh        (run in the background, Ctrl-C/kill
# to stop; safe to restart — it re-syncs from the pidfile)

set -u

HOME_DIR="${CCX_HOME:-$HOME/.ccx}"
PIDFILE="$HOME_DIR/daemon.pid"
OUT="$HOME_DIR/logs/death-watch.log"
PORT="$(jq -r '.daemon.port // 4567' "$HOME_DIR/providers.json" 2>/dev/null || echo 4567)"

mkdir -p "$HOME_DIR/logs"

wlog() { printf '%s %s\n' "$(date -Iseconds)" "$*" >> "$OUT"; }

snapshot() { # $1 = pid
    {
        echo "snapshot: ps pid=$1"
        ps -o pid,ppid,stat,etime,%cpu,%mem,comm -p "$1" 2>/dev/null
        echo "snapshot: lsof count"
        lsof -nP -a -p "$1" 2>/dev/null | wc -l
        echo "snapshot: newest daemon-*.log tail"
        # the daemon log is daemon-<launch time>.log (one file per launch) —
        # tail the newest one, a flat daemon.log no longer exists
        tail -n 5 "$(ls -t "$HOME_DIR/logs/"daemon-*.log 2>/dev/null | head -n 1)" 2>/dev/null
    } >> "$OUT" 2>&1
}

pid_alive() { # $1 = pid
    [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null
}

read_pid() {
    [ -f "$PIDFILE" ] || { echo ""; return; }
    tr -dc '0-9' < "$PIDFILE"
}

known_pid=""
health_bad_logged=false

wlog "watcher started (port=$PORT pidfile=$PIDFILE)"

while true; do
    pid="$(read_pid)"

    if [ -z "$pid" ]; then
        # no pidfile: either nothing runs (a direct profile — normal) or
        # the daemon exited cleanly (its defer removed the file)
        if [ -n "$known_pid" ]; then
            wlog "daemon stopped (pidfile removed) — clean exit or switch, pid=$known_pid"
            known_pid=""
            health_bad_logged=false
        fi
    elif ! pid_alive "$pid"; then
        # the pid died WITHOUT removing its pidfile: SIGKILL or a crash —
        # the exact moment the 2026-11-09 forensics was missing
        wlog "daemon DIED pid=$pid — pidfile remains, process gone (kill/crash class)"
        snapshot "$pid"
        wlog "NOTE: query the unified log around $(date -Iseconds) for the killer"
        known_pid=""
        health_bad_logged=false
    elif [ "$pid" != "$known_pid" ]; then
        known_pid="$pid"
        health_bad_logged=false
        wlog "daemon up pid=$pid port=$PORT"
    elif ! curl -fsS -m 1 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
        # alive but not answering — log once per episode
        if [ "$health_bad_logged" = false ]; then
            wlog "health REFUSED while process alive pid=$pid port=$PORT (wedged?)"
            snapshot "$pid"
            health_bad_logged=true
        fi
    elif [ "$health_bad_logged" = true ]; then
        wlog "health recovered pid=$pid"
        health_bad_logged=false
    fi

    sleep 2
done
