# Кейс 01 — Borderline error rate

**Главный кейс презентации.** Показывает фундаментальную разницу между статистическим тестом (SPRT) и абсолютным порогом.

## Гипотеза

Canary имеет реальную деградацию — 4.5% ошибок против 2% у stable. Это:
- **Ниже** абсолютного порога Flagger (max = 5%) → Flagger пропустит.
- **Выше** baseline на статистически значимую величину → StatGate с `delta=0.02` обнаружит.

## Setup

| Параметр                              | Stable (v1) | Canary (v2)     |
|---------------------------------------|-------------|-----------------|
| `ERROR_RATE_START` / `ERROR_RATE_END` | 0.02        | 0.045           |
| Описание                              | baseline 2% | деградация 4.5% |

| Инструмент | Конфигурация решения                                 |
|------------|------------------------------------------------------|
| StatGate   | α=β=0.05, delta=0.02 (МДЭ = 2 п.п. над baseline)     |
| Flagger    | thresholdRange.max=5, threshold=2 (2 подряд провала) |

## Запуск

```bash
# Прогон 1: StatGate
./apply.sh statgate
./bin/statctl watch demo-rollout -n statgate-demo &
k6 run --env BASE_URL=http://$INGRESS_IP demo/loadtest/load-test.js

# Сброс
./apply.sh reset

# Прогон 2: Flagger (требует установленного Flagger в кластере)
./apply.sh flagger
kubectl describe canary demo -n statgate-demo
k6 run --env BASE_URL=http://$INGRESS_IP demo/loadtest/load-test.js
```

## Ожидаемое поведение

**Flagger:**
В каждом 60-секундном окне видит canary error rate ≈ 4.5% < 5%. Все iteration passes, canary продвигается 5% → 25% → 45% → 65% → 85% → 100%. Финальное состояние: **Succeeded**. Деградированная версия в проде.

**StatGate:**
- Границы Вальда: A = ln(0.95/0.05) ≈ 2.944, B = -A ≈ -2.944.
- Per-failure прирост Λ: ln(0.04/0.02) = ln(2) ≈ 0.693.
- Per-success прирост Λ: ln(0.96/0.98) ≈ -0.0206.
- Ожидаемый прирост Λ на 100 запросов canary при p=4.5%: ≈ 4.5·0.693 + 95.5·(-0.0206) ≈ +1.15.
- Достижение A ≈ 2.944 требует ~250–300 canary-запросов.
- При 20 VUs нагрузки k6 и 5% веса (~0.7 RPS на canary) это ~6 минут; при 25% веса (~3.5 RPS) — около минуты.
- Финальное состояние: **Aborted** на 2-м или 3-м шаге.

## Критерий успеха

StatGate → `Aborted`, Flagger → `Succeeded`. **Прямая противоположность исходов на одних и тех же данных** — самая сильная иллюстрация для слайда.

## Что фиксировать

- Скриншот Grafana: панель "SPRT Log-Likelihood Λ" (видно как растёт и пересекает A).
- Скриншот Grafana: панель "Error Rate by Version" (видна разница 2% vs 4.5%).
- `kubectl get canaryrollout demo-rollout -n statgate-demo -o yaml | grep -A5 status:`
- `kubectl describe canary demo -n statgate-demo | grep -A5 "Phase\|Events"`
- Время от старта rollout до решения.
