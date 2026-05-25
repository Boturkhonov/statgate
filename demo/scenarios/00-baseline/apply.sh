#!/usr/bin/env bash
# Сценарий 00-baseline: canary работает корректно (0% ошибок, как stable).
# Это happy-path для слайда — показывает, что оба инструмента в нормальной
# ситуации спокойно promote новую версию. Демонстрирует, что StatGate не
# ложно-положителен и не блокирует здоровые релизы.

set -euo pipefail

NS="statgate-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../_flagger.sh
source "$SCRIPT_DIR/../_flagger.sh"

statgate_setup_env() {
    # Stable: 0% ошибок — чистый baseline.
    kubectl -n "$NS" set env deployment/demo-stable \
        ERROR_RATE_START=0 ERROR_RATE_END=0 \
        ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
        ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE- ERROR_RATE-

    # Canary: 0% ошибок — здоровая новая версия.
    kubectl -n "$NS" set env deployment/demo-canary \
        ERROR_RATE_START=0 ERROR_RATE_END=0 \
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
        echo "Ожидается: rollout пройдёт все шаги, финальная фаза Promoted."
        ;;
    flagger)
        flagger_create_topology
        flagger_apply_canary "$SCRIPT_DIR"
        # Для baseline canary деплоится с теми же 0%, что и primary, —
        # триггер всё равно нужен, чтобы Flagger зафиксировал "новую версию"
        # и запустил анализ. APP_VERSION=canary создаёт separate label
        # в Prometheus, без него все запросы идут в version=stable и query
        # делителя обнуляет результат.
        flagger_trigger_canary \
            ERROR_RATE_START=0 ERROR_RATE_END=0 \
            ERROR_RATE_RAMP_SECONDS- ERROR_SPIKE_AT_SECONDS- \
            ERROR_SPIKE_DURATION_SECONDS- ERROR_SPIKE_RATE-
        echo "Готово. Ожидается: Flagger проходит шаги 20→40→60 и Promotion completed."
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
