#!/usr/bin/env bash
# Сценарий 03-transient-spike: canary здоровый (1%), но имеет один короткий
# всплеск через 90 секунд после старта. Flagger ловит ложно-положительный
# rollback; StatGate тоже среагирует, если параметры спайка достаточно сильные —
# см. README, обсуждение границ применимости.

set -euo pipefail

NS="statgate-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../_flagger.sh
source "$SCRIPT_DIR/../_flagger.sh"

statgate_setup_env() {
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
        statgate_setup_env
        kubectl apply -f "$SCRIPT_DIR/statgate-rollout.yaml"
        echo "Готово. Запусти: ./bin/statctl watch demo-rollout -n $NS"
        echo "ВАЖНО: спайк начинается на 90-й секунде после старта POD'а."
        ;;
    flagger)
        flagger_create_topology
        flagger_apply_canary "$SCRIPT_DIR"
        flagger_trigger_canary \
            ERROR_RATE_START=0.01 ERROR_RATE_END=0.01 \
            ERROR_RATE_RAMP_SECONDS- \
            ERROR_SPIKE_AT_SECONDS=90 ERROR_SPIKE_DURATION_SECONDS=15 \
            ERROR_SPIKE_RATE=0.4
        echo "Готово. Спайк сработает через 90с после первого canary-пода."
        ;;
    reset)
        kubectl -n "$NS" delete canaryrollout demo-rollout --ignore-not-found
        if kubectl -n "$NS" get canary demo >/dev/null 2>&1; then
            flagger_reset
        else
            kubectl -n "$NS" set env deployment/demo-stable \
                ERROR_RATE_START- ERROR_RATE_END- ERROR_RATE_RAMP_SECONDS- \
                ERROR_SPIKE_AT_SECONDS- ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE-
            kubectl -n "$NS" set env deployment/demo-canary \
                ERROR_RATE_START- ERROR_RATE_END- ERROR_RATE_RAMP_SECONDS- \
                ERROR_SPIKE_AT_SECONDS- ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- \
                ERROR_RATE=0.3
        fi
        ;;
    *)
        echo "Usage: $0 {statgate|flagger|reset}" >&2
        exit 1
        ;;
esac
