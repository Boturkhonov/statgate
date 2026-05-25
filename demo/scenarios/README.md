# Сценарии сравнения StatGate vs Flagger

Три воспроизводимых кейса, каждый демонстрирует отдельное свойство SPRT относительно threshold-based подхода Flagger.

| #  | Сценарий                                        | Свойство SPRT                                    | Ожидаемый исход                     |
|----|-------------------------------------------------|--------------------------------------------------|-------------------------------------|
| 00 | [Baseline (happy path)](00-baseline/README.md)  | Корректное поведение в норме (α-гарантия)        | StatGate Promoted, Flagger Succeeded |
| 01 | [Borderline](01-borderline/README.md)           | False-negative resistance                        | StatGate Aborted, Flagger Succeeded |
| 02 | [Slow drift](02-slow-drift/README.md)           | Adaptive detection / accumulation                | StatGate Aborted, Flagger Succeeded |
| 03 | [Transient spike](03-transient-spike/README.md) | False-positive resistance (границы применимости) | Зависит от параметров — см. README  |

Порядок презентации: сначала **00-baseline** (подтверждение корректности — оба инструмента promote здоровый релиз), затем **01-borderline** (главный кейс — на тех же данных противоположные решения), потом 02-slow-drift и 03-transient-spike.

## Общая схема прогона

> **Важно про `eval $(minikube docker-env)`.** Эта команда меняет env только в **текущем терминале**. Если открыть новую вкладку и сделать оттуда `helm install`, docker CLI снова смотрит на хост — образы окажутся не в Minikube-демоне и kubelet выдаст `pull access denied`. Делай сборку и `helm install` в **одной сессии**.

### 1. Базовая инфраструктура — один раз для всех кейсов

```bash
# 1.1. Кластер + Istio
minikube start --cpus=4 --memory=8192 --driver=docker
istioctl install --set profile=demo -y
# istio-injection включается автоматически из demo/manifests/00-namespace.yaml
# (на ns statgate-demo). Дополнительный label на default не нужен.

# 1.2. Переключить docker CLI на демон Minikube
eval $(minikube docker-env)
docker info | grep Name   # должно быть "Name: minikube" — проверка

# 1.3. Собрать образ КОНТРОЛЛЕРА (без него helm install упадёт с pull access denied)
make docker-build IMG=statgate-controller:latest

# 1.4. Собрать ОБА образа demo-приложения через make target — он передаёт
# --build-arg VERSION для v1 и v2. Прямой docker build БЕЗ build-arg даст
# два одинаковых образа с APP_VERSION=v1 → метрики stable и canary смешаются
# под одним label, SPRT не сможет различать версии.
make docker-build-demo

# 1.5. Подтвердить, что образы видны Minikube
minikube image ls | grep -E "statgate-controller|statgate-demo"

# 1.6. Установить контроллер через Helm (CRD устанавливается тем же чартом)
helm upgrade --install statgate helm/statgate/ \
  --namespace statgate-system --create-namespace \
  --set image.repository=statgate-controller \
  --set image.tag=latest \
  --set image.pullPolicy=IfNotPresent

# 1.7. Базовые демо-манифесты через kustomize (-k). Grafana-дашборд
# генерируется из demo/manifests/grafana-dashboard.json. CanaryRollout сюда НЕ входит.
kubectl apply -k demo/manifests/

# 1.8. Дождаться готовности
kubectl wait --for=condition=Ready pod -l app=demo \
    -n statgate-demo --timeout=180s
kubectl wait --for=condition=Available deployment/statgate-controller-manager \
    -n statgate-system --timeout=120s

# 1.9. Собрать CLI для наблюдения
make build-cli   # → bin/statctl
export PATH="$PATH:$(pwd)/bin"

# 1.10. Получить INGRESS_IP (одно из двух)
# Вариант А: port-forward (работает всегда)
kubectl port-forward svc/istio-ingressgateway -n istio-system 8080:80 &
export INGRESS_IP=localhost:8080
# Вариант Б: minikube service
# export INGRESS_IP=$(minikube service istio-ingressgateway -n istio-system --url | head -1 | sed 's|^http://||')
```

### 2. Прогон одного сценария

```bash
cd demo/scenarios/01-borderline

# 2.1. StatGate
./apply.sh statgate
statctl watch demo-rollout -n statgate-demo &
k6 run --env BASE_URL=http://$INGRESS_IP ../../loadtest/load-test.js
# зафиксировать финальное состояние (см. раздел "Что фиксировать")
./apply.sh reset

# 2.2. Flagger (требует установленного Flagger — см. ниже)
./apply.sh flagger
kubectl describe canary demo -n statgate-demo
k6 run --env BASE_URL=http://$INGRESS_IP ../../loadtest/load-test.js
./apply.sh reset
```

> **Важно про топологию.** StatGate и Flagger используют принципиально разные
> топологии:
>
> - **StatGate** работает с двумя готовыми Deployment'ами (`demo-stable` +
>   `demo-canary`) и сам управляет весами в `demo-vs`.
> - **Flagger** ожидает ОДИН Deployment (`demo`), который сам клонирует в
>   `demo-primary`/`demo-canary` и создаёт собственный VirtualService с
>   именем `demo`.
>
> Поэтому `./apply.sh flagger` **временно демонтирует** StatGate-инфру
> (удаляет `demo-stable/demo-canary/demo-vs`), создаёт единый Deployment
> `demo` со стабильными env'ами, применяет Flagger Canary CR, дожидается
> создания `demo-primary`, и затем триггерит canary deploy через
> `kubectl set env deployment/demo …`.
>
> `./apply.sh reset` **восстанавливает StatGate-инфру** обратно через
> `kubectl apply -k demo/manifests/`. После Flagger-прогона обязательно
> сделай reset перед следующим запуском StatGate-режима, иначе сценарий
> упадёт ("deployment demo-stable not found").
>
> Grafana-дашборд продолжает работать одинаково в обоих режимах: метрики
> `http_requests_total{version=...}` разделяются по env-var `APP_VERSION`
> приложения (а не по физическому имени Deployment), поэтому панели 1-3,
> 9-12 показывают `stable` vs `canary` корректно в любом случае.

### 3. Между прогонами — сброс Prometheus (опционально)

Чтобы StatGate начинал с чистых счётчиков (иначе baseline p₀ будет искажён историей):

```bash
kubectl rollout restart deployment/prometheus-server -n monitoring
kubectl rollout status deployment/prometheus-server -n monitoring
```

## Установка Flagger (один раз)

```bash
kubectl create namespace flagger-system
helm repo add flagger https://flagger.app
helm upgrade -i flagger flagger/flagger \
    --namespace=flagger-system \
    --set meshProvider=istio \
    --set metricsServer=http://prometheus-server.monitoring.svc.cluster.local:9090
```

## Что фиксировать после каждого прогона

1. **Финальное состояние** (Promoted/Aborted у StatGate, Succeeded/Failed у Flagger).
2. **Время** от старта rollout до решения.
3. **Скриншот Grafana** dashboard `statgate-canary-sprt`:
   - Панель "SPRT Log-Likelihood Λ" с границами A и B
   - Панель "Error Rate by Version"
   - Панель "Rollout Phase"
4. **`kubectl get events`** за период rollout.
5. **YAML-снимок:**
   ```bash
   kubectl get canaryrollout demo-rollout -n statgate-demo -o yaml > result-statgate.yaml
   kubectl get canary demo -n statgate-demo -o yaml > result-flagger.yaml
   ```

Результаты использовать как материал для слайдов защиты и таблицы в [`demo/flagger/comparison.md`](../flagger/comparison.md).

## Env vars demo-приложения

Кейсы используют расширенный набор переменных окружения в `demo/app`:

| Variable                       | Type  | Smysl                                                                  |
|--------------------------------|-------|------------------------------------------------------------------------|
| `ERROR_RATE`                   | float | Backwards-compatible static rate (используется если `_START` не задан) |
| `ERROR_RATE_START`             | float | Начальный rate (для ramp)                                              |
| `ERROR_RATE_END`               | float | Конечный rate (для ramp)                                               |
| `ERROR_RATE_RAMP_SECONDS`      | int   | Длительность линейного перехода start→end                              |
| `ERROR_SPIKE_AT_SECONDS`       | int   | Момент начала всплеска (от старта пода)                                |
| `ERROR_SPIKE_DURATION_SECONDS` | int   | Длительность всплеска                                                  |
| `ERROR_SPIKE_RATE`             | float | Rate во время всплеска                                                 |

Логика: спайк имеет приоритет над ramp; ramp — над статикой. Реализация в [`demo/app/error_inject.go`](../app/error_inject.go).
