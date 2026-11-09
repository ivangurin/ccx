#!/bin/bash
# diag-daemon-death.sh — sandboxed reproduction harness for the silent daemon
# death (2026-11-09: the daemon serving an openai-protocol provider died
# every 1-24 min with zero traces — no signal log, no panic, no system
# records — leaving Claude Code sessions on ECONNREFUSED).
#
# What it runs, all inside throwaway CCX_HOMEs (the live ~/.ccx and
# ~/.claude/settings.json are never touched):
#   1. a supervisor loop per daemon: `ccx serve --provider <key> --port <N>
#      --debug` in the foreground with output captured, recording every exit
#      with its code and timestamp (0 = clean exit — with the instrumented
#      build /admin/stop also logs WHY; 137 = SIGKILL; 129 = SIGHUP; 2 =
#      panic); one daemon per CCX_HOME (daemon.lock), the loaded and the
#      control daemon get separate homes;
#   2. a health watcher: /health every 2s; on the first refusal it snapshots
#      ps/lsof/netstat + the daemon log tail next to the incident line;
#   3. a load generator imitating Claude Code against the LOADED daemon:
#      concurrent workers, a ~160k-token context, stream/non-stream mix,
#      mid-stream client aborts, count_tokens calls, wrong-model probes,
#      idle gaps of 1-5 min;
#   4. a CONTROL daemon: same provider, same binary, own home, NO load — if
#      the control dies too, the trigger is environmental, not
#      request-shaped.
#
# Usage: scripts/diag-daemon-death.sh [provider_key]   (default: x5)
# Stop:  touch <sandbox>/STOP          (the sandbox dir is printed at start)
#
# The provider entry (base_url + credentials) is copied verbatim from the
# LIVE providers.json — the upstream's real behavior is part of the
# reproduction surface. Requests burn real quota: the default load is ~3
# large requests per minute; tune WORKERS/BIG_EVERY_MS before long runs.

set -u

PROVIDER_KEY="${1:-x5}"
PORT=4599                # loaded daemon
CONTROL_PORT=4598        # idle control daemon
WORKERS=3                # concurrent load workers
BIG_EVERY_MS=180000      # one ~160k-token request per worker per interval
                         # (~1/min total: the shape matters, not the volume —
                         # real quota burns with every big request)
SMALL_EVERY_MS=3000      # small requests between the big ones
IDLE_EVERY=25            # every N big rounds a worker idles 1-5 min
HEALTH_EVERY=2           # watcher cadence, seconds

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/ccx"
SBOX="$(mktemp -d "${TMPDIR:-/tmp}/ccx-diag.XXXXXX")"
LOADED_HOME="$SBOX/loaded-home"
CONTROL_HOME="$SBOX/control-home"
mkdir -p "$LOADED_HOME" "$CONTROL_HOME"

REPORT="$SBOX/report.log"
SUPERV="$SBOX/superv.log"

log() { printf '%s %s\n' "$(date -Iseconds)" "$*" | tee -a "$REPORT"; }

# --- providers.json: copy the target provider verbatim (credentials stay
# inside the sandbox, 0600, gone with it) -------------------------------
for home in "$LOADED_HOME" "$CONTROL_HOME"; do
    jq --arg key "$PROVIDER_KEY" '{providers: {($key): .providers[$key]}}' \
        "$HOME/.ccx/providers.json" > "$home/providers.json"
    chmod 600 "$home/providers.json"
done
if ! jq -e --arg key "$PROVIDER_KEY" '.providers[$key].protocol' "$LOADED_HOME/providers.json" >/dev/null; then
    echo "provider '$PROVIDER_KEY' not found in the live providers.json" >&2
    exit 1
fi

MODEL="$(jq -r --arg key "$PROVIDER_KEY" '.providers[$key].models[0].id // .providers[$key].model // ""' "$LOADED_HOME/providers.json")"
[ -n "$MODEL" ] || MODEL="copilot-code-large"
# The client credential, exactly what Claude Code sends after a switch
# (daemonEnv precedence: auth_token > api_key). openai is pass-through: the
# daemon forwards THIS bearer upstream — without it every probe is a 401
# and burns nothing (verified live: 210 × 401, zero tokens).
TOKEN="$(jq -r --arg key "$PROVIDER_KEY" '.providers[$key].auth_token // .providers[$key].api_key // "ccx"' "$LOADED_HOME/providers.json")"
AUTH=(-H "Authorization: Bearer $TOKEN")
log "sandbox: $SBOX (provider=$PROVIDER_KEY model=$MODEL port=$PORT control=$CONTROL_PORT)"
log "stop with: touch $SBOX/STOP"

# --- the ~160k-token payload (string doubling — never passed through
# argv: a 650KB argument dies with "Argument list too long"; curl reads it
# from a file) ------------------------------------------------------------
PAYLOAD="$SBOX/payload-big.json"          # %STREAM% template
PAYLOAD_STREAM="$SBOX/payload-stream.json"
PAYLOAD_SMALL="$SBOX/payload-small.json"
{
    s="x"
    for _ in $(seq 1 20); do s="$s$s"; done
    s="${s:0:650000}"                               # ~161k tokens of context
    printf '{"model":"%s","max_tokens":64,"stream":%%STREAM%%,"messages":[{"role":"user","content":"Reply with the single word ok. %s"}]}' \
        "$MODEL" "$s"
} > "$PAYLOAD"
sed 's/%STREAM%/true/' "$PAYLOAD" > "$PAYLOAD_STREAM"
printf '{"model":"%s","max_tokens":16,"stream":false,"messages":[{"role":"user","content":"hi"}]}' \
    "$MODEL" > "$PAYLOAD_SMALL"

sleep_awhile() { # $1 = milliseconds, STOP-aware
    # two separate locals: bash expands every word of a multi-assignment
    # local BEFORE assigning, so inlining the arithmetic kills the function
    # under set -u ("ms: unbound variable" — verified live in the harness)
    local ms="${1:-1000}"
    local end=$((SECONDS + ms / 1000))
    while [ $SECONDS -lt $end ] && [ ! -f "$SBOX/STOP" ]; do sleep 1; done
}

# --- supervisor: restart-on-death with exit codes -----------------------
# The daemon runs FOREGROUND (no --daemon): its logs land on stderr, which
# lands in <home>/daemon-<name>.log. Any SIGHUP is now HANDLED by the
# instrumented binary (logged "signal — shutting down") — an unexplained
# exit can no longer hide.
supervise() { # $1 = home, $2 = port, $3 = name
    local home="$1" port="$2" name="$3"
    while [ ! -f "$SBOX/STOP" ]; do
        CCX_HOME="$home" CCX_SETTINGS="$home/settings.json" \
            "$BIN" serve --provider "$PROVIDER_KEY" --port "$port" --debug \
            >> "$home/daemon-$name.log" 2>&1
        code=$?
        printf 'exit=%s at=%s name=%s\n' "$code" "$(date -Iseconds)" "$name" >> "$SUPERV"
        log "INCIDENT: daemon $name exited code=$code"
        sleep 2
    done
}

# --- health watcher: snapshot the state at the first refusal ------------
watch_health() { # $1 = home, $2 = port, $3 = name
    local home="$1" port="$2" name="$3" alive=true
    while [ ! -f "$SBOX/STOP" ]; do
        if ! curl -fsS -m 2 "http://127.0.0.1:$port/health" >/dev/null 2>&1; then
            if [ "$alive" = true ]; then
                local shot="$SBOX/incident-$(date +%H%M%S)-$name.txt"
                {
                    echo "== $name health failure at $(date -Iseconds)"
                    pid="$(cat "$home/daemon.pid" 2>/dev/null || echo none)"
                    echo "pidfile: $pid"
                    [ "$pid" != none ] && ps -o pid,ppid,stat,etime,%cpu,%mem,comm -p "$pid"
                    echo "--- lsof ---"; lsof -nP -a -p "$pid" 2>/dev/null | head -40
                    echo "--- netstat ---"; netstat -anv -p tcp 2>/dev/null | grep ".$port" | head -20
                    echo "--- daemon log tail ---"; tail -n 30 "$home/daemon-$name.log" 2>/dev/null
                } > "$shot" 2>&1
                log "INCIDENT: $name /health refused — snapshot $shot"
                alive=false
            fi
        else
            [ "$alive" = false ] && log "RECOVERED: $name /health answering"
            alive=true
        fi
        sleep "$HEALTH_EVERY"
    done
}

# --- load generator: one Claude-Code-like worker ------------------------
worker() { # $1 = worker id
    local id="$1" round=0
    while [ ! -f "$SBOX/STOP" ]; do
        round=$((round + 1))
        # one big streaming request (the agent turn on a grown context)
        curl -sS -m 120 -o /dev/null -X POST "http://127.0.0.1:$PORT/v1/messages" \
            -H 'Content-Type: application/json' "${AUTH[@]}" \
            --data-binary "@$PAYLOAD_STREAM" >>"$SBOX/worker-$id.log" 2>&1
        # a mid-stream abort: the client walks away (Esc, retry, background
        # probe cancelled) — the daemon must notice and release the upstream
        if [ $((round % 2)) -eq 0 ]; then
            curl -sS -m 1 -o /dev/null -X POST "http://127.0.0.1:$PORT/v1/messages" \
                -H 'Content-Type: application/json' "${AUTH[@]}" \
                --data-binary "@$PAYLOAD_STREAM" >/dev/null 2>&1
        fi
        sleep_awhile "$BIG_EVERY_MS"
        for _ in $(seq 1 10); do
            [ -f "$SBOX/STOP" ] && break
            curl -sS -m 60 -o /dev/null -X POST "http://127.0.0.1:$PORT/v1/messages" \
                -H 'Content-Type: application/json' "${AUTH[@]}" \
                --data-binary "@$PAYLOAD_SMALL" >/dev/null 2>&1
            # count_tokens (the client's cheap probe)
            curl -sS -m 10 -o /dev/null -X POST "http://127.0.0.1:$PORT/v1/messages/count_tokens" \
                -H 'Content-Type: application/json' "${AUTH[@]}" \
                --data-binary '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}]}' >/dev/null 2>&1
            # a wrong-model probe (the 400/403 paths the gateway answers)
            if [ $((round % 7)) -eq 0 ]; then
                curl -sS -m 10 -o /dev/null -X POST "http://127.0.0.1:$PORT/v1/messages" \
                    -H 'Content-Type: application/json' "${AUTH[@]}" \
                    --data-binary '{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}' >/dev/null 2>&1
            fi
            sleep_awhile "$SMALL_EVERY_MS"
        done
        # an idle gap: both observed deaths followed a short idle period
        if [ $((round % IDLE_EVERY)) -eq 0 ]; then
            sleep_awhile $((60000 + RANDOM % 240000))
        fi
    done
}

# --- boot everything -----------------------------------------------------
( supervise "$CONTROL_HOME" "$CONTROL_PORT" control ) &
( supervise "$LOADED_HOME" "$PORT" loaded ) &
sleep 3
( watch_health "$CONTROL_HOME" "$CONTROL_PORT" control ) &
( watch_health "$LOADED_HOME" "$PORT" loaded ) &
for i in $(seq 1 "$WORKERS"); do ( worker "$i" ) & done

log "harness up: workers=$WORKERS"
wait
