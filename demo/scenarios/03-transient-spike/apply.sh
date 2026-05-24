#!/usr/bin/env bash
# Сценарий 03-transient-spike: canary здоровый (1%), но имеет один короткий
# всплеск 15 секунд с rate 0.4 через 90 секунд после старта пода.
# Flagger откатит ложно-положительно, StatGate переживёт всплеск.

set -euo pipefail

NS="statgate-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

setup_env() {
    # Stable: 1% baseline.
    kubectl -n "$NS" set env deployment/demo-stable \
        ERROR_RATE_START=0.01 ERROR_RATE_END=0.01 \
        ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
        ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- ERROR_RATE-

    # Canary: 1% baseline + spike 40% на 15 секунд через 90 секунд после старта.
    kubectl -n "$NS" set env deployment/demo-canary \
        ERROR_RATE_START=0.01 ERROR_RATE_END=0.01 \
        ERROR_RATE_RAMP_SECONDS- \
        ERROR_SPIKE_AT_SECONDS=90 ERROR_SPIKE_DURATION_SECONDS=15 \
        ERROR_SPIKE_RATE=0.4 ERROR_RATE-

    kubectl -n "$NS" rollout status deployment/demo-stable --timeout=120s
    kubectl -n "$NS" rollout status deployment/demo-canary --timeout=120s
}

case "${1:-}" in
    statgate)
        setup_env
        kubectl apply -f "$SCRIPT_DIR/statgate-rollout.yaml"
        echo "Готово. Запусти: ./bin/statctl watch demo-rollout -n $NS"
        echo "ВАЖНО: спайк начинается на 90-й секунде после старта POD'а,"
        echo "поэтому нужно сразу запускать rollout + load после kubectl apply."
        ;;
    flagger)
        setup_env
        kubectl apply -f "$SCRIPT_DIR/flagger-canary.yaml"
        echo "Готово. Запусти: kubectl -n $NS describe canary demo"
        ;;
    reset)
        kubectl -n "$NS" delete canaryrollout demo-rollout --ignore-not-found
        kubectl -n "$NS" delete canary demo --ignore-not-found
        kubectl -n "$NS" set env deployment/demo-stable \
            ERROR_RATE_START- ERROR_RATE_END- ERROR_RATE_RAMP_SECONDS- \
            ERROR_SPIKE_AT_SECONDS- ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE-
        kubectl -n "$NS" set env deployment/demo-canary \
            ERROR_RATE_START- ERROR_RATE_END- ERROR_RATE_RAMP_SECONDS- \
            ERROR_SPIKE_AT_SECONDS- ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- \
            ERROR_RATE=0.3
        ;;
    *)
        echo "Usage: $0 {statgate|flagger|reset}" >&2
        exit 1
        ;;
esac
