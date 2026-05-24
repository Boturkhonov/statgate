#!/usr/bin/env bash
# Сценарий 02-slow-drift: canary деградирует постепенно с 1% до 6% за 5 минут.
# Flagger в каждом отдельном окне видит низкий error rate и пропускает.
# StatGate накапливает Λ и откатывает.

set -euo pipefail

NS="statgate-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

setup_env() {
    # Stable: 1% — стабильный baseline.
    kubectl -n "$NS" set env deployment/demo-stable \
        ERROR_RATE_START=0.01 ERROR_RATE_END=0.01 \
        ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
        ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- ERROR_RATE-

    # Canary: линейный рост с 1% до 6% за 300 секунд.
    kubectl -n "$NS" set env deployment/demo-canary \
        ERROR_RATE_START=0.01 ERROR_RATE_END=0.06 \
        ERROR_RATE_RAMP_SECONDS=300 \
        ERROR_SPIKE_AT_SECONDS- ERROR_SPIKE_DURATION_SECONDS- \
        ERROR_SPIKE_RATE- ERROR_RATE-

    kubectl -n "$NS" rollout status deployment/demo-stable --timeout=120s
    kubectl -n "$NS" rollout status deployment/demo-canary --timeout=120s
}

case "${1:-}" in
    statgate)
        setup_env
        kubectl apply -f "$SCRIPT_DIR/statgate-rollout.yaml"
        echo "Готово. Запусти: ./bin/statctl watch demo-rollout -n $NS"
        echo "ВАЖНО: ramp начинается с момента старта canary pod (не с rollout)."
        echo "При перезапуске пода timeline сбрасывается."
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
