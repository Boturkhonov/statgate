# Кейс 02 — Slow drift (постепенная деградация)

## Гипотеза

Canary медленно деградирует со временем — error rate линейно растёт с 1% до 6% за 5 минут. В каждом отдельном 60-секундном окне Flagger видит локально-низкий error rate (1–3% в первой половине, 3–5% во второй), нигде явно не превышая порог. SPRT, наоборот, накапливает Λ через все окна и реагирует на устойчивое превышение baseline.

## Setup

| Параметр                  | Stable (v1) | Canary (v2) |
|---------------------------|-------------|-------------|
| `ERROR_RATE_START`        | 0.01        | 0.01        |
| `ERROR_RATE_END`          | 0.01        | 0.06        |
| `ERROR_RATE_RAMP_SECONDS` | —           | 300         |

Профиль canary: error_rate(t) = 0.01 + (0.05 · min(t / 300, 1)). Через 5 минут rate стабилизируется на 6%.

| Инструмент | Конфигурация                      |
|------------|-----------------------------------|
| StatGate   | α=β=0.05, delta=0.02              |
| Flagger    | thresholdRange.max=5, threshold=2 |

## Запуск

```bash
./apply.sh statgate
./bin/statctl watch demo-rollout -n statgate-demo &
k6 run --env BASE_URL=http://$INGRESS_IP demo/loadtest/load-test.js

./apply.sh reset

./apply.sh flagger
kubectl describe canary demo -n statgate-demo
```

## Ожидаемое поведение

**Flagger (по окнам):**

| t, c    | rate canary | Flagger window | Решение |
|---------|-------------|----------------|---------|
| 0–60    | ~1.0%       | 1.0% < 5%      | pass    |
| 60–120  | ~1.5%       | 1.5% < 5%      | pass    |
| 120–180 | ~2.5%       | 2.5% < 5%      | pass    |
| 180–240 | ~3.5%       | 3.5% < 5%      | pass    |
| 240–300 | ~4.5%       | 4.5% < 5%      | pass    |

Финал: **Succeeded**. Версия с 6% ошибок (6× baseline) идёт в прод.

**StatGate:**
- baseline в Prometheus обновляется каждый цикл (10s) — растёт вместе с canary, но с лагом, поскольку запросов на canary меньше из-за низкого веса.
- Поскольку delta=0.02 фиксирован, тест работает по гипотезам H₀: p₁=p₀ против H₁: p₁=p₀+0.02.
- Когда canary уплыл на 2+ п.п. над baseline (то есть после ~150 секунд работы), Λ начинает расти стабильно.
- Ожидаемый откат: **Aborted** во второй половине rollout (на шаге 25% или 50%).

## Критерий успеха

StatGate → `Aborted` до достижения шага 100%, Flagger → `Succeeded`.

## Что фиксировать

- Скриншот Grafana с панелями "Error Rate by Version" (видно плавный рост canary) и "SPRT Λ" (видно момент пересечения A).
- Точный момент, в который StatGate откатил.
- `kubectl get canaryrollout/canary -o yaml` после прогонов.

## Замечание о baseline

StatGate берёт p₀ из stable. Если в момент сравнения stable стабильно держит 1%, а canary "плывёт", то p̂_canary − p̂_stable растёт. Если же baseline тоже шумит (например, из-за низкого трафика), p₀ может оказаться завышенным, и SPRT задетектит деградацию позже. Поэтому в этом сценарии важна стабильная нагрузка от k6 (не менять stages).
