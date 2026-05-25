#!/usr/bin/env bash
# Сценарий 01-borderline: canary деградирует ниже порога Flagger (4.5% vs 5%),
# но статистически значимо относительно stable (2%). StatGate должен откатить,
# Flagger — пропустить.

set -euo pipefail

NS="statgate-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../_flagger.sh
source "$SCRIPT_DIR/../_flagger.sh"

# StatGate setup: устанавливаем env vars на оба deployment напрямую.
statgate_setup_env() {
    # Stable: 2% ошибок (baseline).
    kubectl -n "$NS" set env deployment/demo-stable \
        ERROR_RATE_START=0.02 ERROR_RATE_END=0.02 \
        ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
        ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- ERROR_RATE-

    # Canary: 4.5% ошибок (ниже порога Flagger, но выше baseline).
    kubectl -n "$NS" set env deployment/demo-canary \
        ERROR_RATE_START=0.045 ERROR_RATE_END=0.045 \
        ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
        ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- ERROR_RATE-

    kubectl -n "$NS" rollout status deployment/demo-stable --timeout=120s
    kubectl -n "$NS" rollout status deployment/demo-canary --timeout=120s
}

case "${1:-}" in
    statgate)
        statgate_setup_env
        kubectl apply -f "$SCRIPT_DIR/statgate-rollout.yaml"
        echo "Готово. Запусти: ./bin/statctl watch demo-rollout -n $NS"
        ;;
    flagger)
        flagger_create_topology
        flagger_apply_canary "$SCRIPT_DIR"
        # Запустить нагрузку ДО триггера, чтобы у primary накопились метрики
        # baseline (1%). Затем триггерим canary deploy.
        flagger_trigger_canary \
            ERROR_RATE_START=0.045 ERROR_RATE_END=0.045 \
            ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
            ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE-
        echo "Готово. Запусти: kubectl -n $NS describe canary demo"
        ;;
    reset)
        # Безопасный reset: убираем и StatGate-ресурсы, и (если был Flagger) — Flagger-инфру.
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
