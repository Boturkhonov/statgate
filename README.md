# StatGate — Оператор прогрессивной доставки на основе SPRT

StatGate — Kubernetes-оператор канареечных развёртываний с математически обоснованным
анализом качества трафика на основе **Sequential Probability Ratio Test (SPRT)**.
Вместо жёстких порогов ("error_rate < 5 %") StatGate накапливает логарифмическое
отношение правдоподобия Λ и останавливается ровно тогда, когда собранных данных
достаточно для решения с заданными вероятностями ошибок α и β.

## Архитектура

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes API                           │
│                   (CanaryRollout CRD)                           │
└──────────────────────────┬──────────────────────────────────────┘
                           │ watch / update status
              ┌────────────▼────────────┐
              │   StatGate Controller   │
              │  (controller-runtime)   │
              └──┬──────────┬───────────┘
                 │          │ query PromQL
      patch      │          │
   VirtualService│    ┌─────▼──────┐
                 │    │ Prometheus │
   ┌─────────────▼──┐ └─────┬──────┘
   │ Istio VS        │       │ metrics
   │  stable: 70 %  │  ┌────▼────────────┐
   │  canary: 30 %  │  │  SPRT Analyzer  │
   └────────────────┘  │  Λ += k·ln(p1/p0│
                        │  + (n-k)·ln(...)│
                        │  Λ ≥ A→rollback │
                        │  Λ ≤ B→promote  │
                        └─────────────────┘
```

Контроллер отслеживает ресурс `CanaryRollout`, пошагово увеличивает вес трафика
через Istio VirtualService и во время каждой паузы периодически (каждые
`analysisIntervalSeconds`) запрашивает Prometheus. SPRT-аккумулятор Λ сохраняется
в `status.analysisState`, что позволяет пережить перезапуск контроллера.

## Фазы развёртывания

| Фаза       | Описание                                                         |
|------------|------------------------------------------------------------------|
| `Pending`  | Ресурс создан, ещё не запущен                                    |
| `Running`  | Идёт пошаговое переключение трафика, SPRT накапливает Λ          |
| `Paused`   | Приостановлено (`spec.paused: true`), Λ не меняется              |
| `Promoted` | Канарейка достигла 100 %, SPRT решил `promote` на всех метриках  |
| `Aborted`  | Откат на 100 % стабильной (`spec.abort: true` или SPRT rollback) |
| `Failed`   | Поды канарейки не готовы в течение тайм-аута                     |

## Предварительные требования

| Инструмент | Версия | Назначение |
|---|---|---|
| Go | 1.23+ | Сборка контроллера и `statctl` |
| Docker | 24+ | Сборка образов |
| Minikube | 1.33+ | Локальный Kubernetes-кластер |
| kubectl | 1.28+ | Управление кластером |
| Helm | 3.14+ | Установка контроллера |
| istioctl | 1.22+ | Установка Istio |
| k6 | 0.51+ | Нагрузочное тестирование (опционально) |

---

## Быстрый старт в Minikube

### 1. Запуск кластера и установка Istio

```bash
# Запустить Minikube с достаточными ресурсами для Istio
minikube start --cpus=4 --memory=8192 --driver=docker

# Установить Istio (profile demo включает ingress gateway и Prometheus)
istioctl install --set profile=demo -y

# Проверить, что все поды Istio запустились
kubectl get pods -n istio-system
```

### 2. Сборка образов внутри Minikube

Переключаем Docker-CLI на daemon внутри Minikube, чтобы образы были
доступны без push в registry.

```bash
eval $(minikube docker-env)
# Или если в Git Bash:
source <(minikube -p minikube docker-env --shell bash)

# Образ контроллера
make docker-build IMG=statgate-controller:latest

# Образы демо-приложения (v1 — стабильная, v2 — с инъекцией ошибок 30 %)
make docker-build-demo
```

> После `eval $(minikube docker-env)` все команды `docker build` в этом
> терминале работают с Docker-daemon Minikube. Не закрывайте сессию до
> окончания быстрого старта.

### 3. Установка контроллера через Helm

Helm устанавливает CRD и контроллер за один шаг. Не запускайте `make install`
перед этим — иначе CRD окажется без Helm-меток и повторный `helm upgrade`
упадёт с ошибкой `invalid ownership metadata`.

```bash
# Установить CRD + контроллер через Helm в отдельный namespace
helm upgrade --install statgate helm/statgate/ \
  --namespace statgate-system \
  --create-namespace \
  --set image.repository=statgate-controller \
  --set image.tag=latest \
  --set image.pullPolicy=IfNotPresent

# Убедиться, что под контроллера запущен
kubectl get pods -n statgate-system
```

> **Если CRD уже была установлена вручную** (`make install`), усыновите её в Helm:
> ```bash
> kubectl annotate crd canaryrollouts.statgate.io \
>   meta.helm.sh/release-name=statgate \
>   meta.helm.sh/release-namespace=statgate-system --overwrite
> kubectl label crd canaryrollouts.statgate.io \
>   app.kubernetes.io/managed-by=Helm --overwrite
> ```
> После этого повторите `helm upgrade --install` выше.

### 4. Развёртывание демо-приложения

```bash
# Применить все демо-манифесты (namespace, deployments, services, gateway,
# virtualservice, rollout, postgres, prometheus, grafana)
kubectl apply -f demo/manifests/

# Дождаться готовности подов (≈ 60 с)
kubectl wait --for=condition=Ready pod \
  -l app=demo -n statgate-demo --timeout=120s

# Проверить, что VirtualService создан
kubectl get virtualservice -n statgate-demo
```

### 5. Сборка и установка statctl

`statctl` — CLI для управления `CanaryRollout` ресурсами.

```bash
make build-cli
# бинарный файл: bin/statctl

# Добавить в PATH (опционально)
export PATH="$PATH:$(pwd)/bin"
```

### 6. Запуск канареечного развёртывания

Демо-манифест `05-rollout.yaml` уже применён на предыдущем шаге.
Контроллер начнёт первый шаг автоматически.

```bash
# Посмотреть список развёртываний
statctl list -n statgate-demo

# Открыть живую панель (обновляется каждые 2 с, работает как top)
statctl watch demo-rollout -n statgate-demo
```

Пример вывода `watch`:
```
statgate-demo/demo-rollout          Running    50%   step 2/4   45s ago
─────────────────────────────────────────────────────────────────────────
SPRT analysis
  α (false rollback)  = 0.05     A (reject H0) = +2.944
  β (missed failure)  = 0.05     B (accept H0) = -2.944
  analysis interval   = 10s

  error-rate          … pending       Λ = -0.518
  rollback ┤━━━━━━━━━━━┼━━━━━━━●━━━━━━━━━━━━━━━━┤ promote
           B=-2.94                           A=+2.94
  baseline p0 = 0.010     alternative p1=p0+Δ = 0.060
  canary observed: 312 requests, 3 failures

Steps
  [✔] step 0   5%  60s
  [✔] step 1  25%  60s
  [▶] step 2  50%  60s  ← current
  [ ] step 3 100%   0s
```

### 7. Нагрузочный тест (k6)

Откройте новый терминал (без `eval $(minikube docker-env)`):

```bash
# Получить IP ingress-gateway
INGRESS_IP=$(minikube service istio-ingressgateway \
  -n istio-system --url | head -1)

# Нормальный сценарий (проверяем что rollout проходит успешно)
k6 run --env BASE_URL=$INGRESS_IP demo/loadtest/load-test.js

# Сценарий с ошибками (SPRT должен обнаружить деградацию и откатить)
# Убедитесь, что ERROR_RATE=0.3 уже задан в demo-canary deployment
k6 run --env BASE_URL=$INGRESS_IP --env ERROR_SCENARIO=true \
  demo/loadtest/load-test.js
```

### 8. Мониторинг в Grafana

```bash
kubectl port-forward svc/grafana -n statgate-demo 3000:3000
```

Откройте [http://localhost:3000](http://localhost:3000) (логин: `admin` / `admin`).
Дашборд **StatGate — Canary Rollout** уже загружен автоматически через provisioning.

Панели:
- Request rate by version (stable vs canary)
- Error rate by version + линия порога откатa
- P95 latency
- SPRT log-likelihood Λ с границами A и B
- Canary weight %

### 9. Управление развёртыванием через statctl

```bash
# Приостановить
statctl pause demo-rollout -n statgate-demo

# Возобновить
statctl resume demo-rollout -n statgate-demo

# Принудительный откат
statctl abort demo-rollout -n statgate-demo

# Применить изменённый манифест
statctl apply -f demo/manifests/05-rollout.yaml

# Удалить
statctl delete demo-rollout -n statgate-demo
```

### 10. Очистка

```bash
kubectl delete -f demo/manifests/ --ignore-not-found
helm uninstall statgate -n statgate-system
make uninstall        # удалить CRD
minikube stop
```

---

## Справочник CRD

```yaml
apiVersion: statgate.io/v1alpha1
kind: CanaryRollout
metadata:
  name: my-rollout
  namespace: my-namespace
spec:
  targetRef:         my-canary-deployment   # Имя канареечного Deployment
  stableServiceRef:  my-stable-svc          # Service стабильных подов
  canaryServiceRef:  my-canary-svc          # Service канареечных подов
  virtualServiceRef: my-vs                  # Istio VirtualService

  prometheusURL: "http://prometheus-server.monitoring.svc.cluster.local:9090"

  # SPRT-анализ: формально гарантирует P(ложный откат) ≤ α, P(пропуск деградации) ≤ β
  analysis:
    alpha: 0.05                  # макс. вероятность ложного отката
    beta:  0.05                  # макс. вероятность пропуска деградации
    analysisIntervalSeconds: 10  # как часто запускать SPRT во время паузы

    metrics:
      - name: error-rate
        # PromQL-счётчики (total и failure) для канарейки и стабильной версии
        canaryTotalQuery:    'sum(http_requests_total{version="canary"})'
        canaryFailureQuery:  'sum(http_requests_total{status_code=~"5..",version="canary"}) or vector(0)'
        stableTotalQuery:    'sum(http_requests_total{version="stable"})'
        stableFailureQuery:  'sum(http_requests_total{status_code=~"5..",version="stable"}) or vector(0)'
        delta: 0.05          # минимальный обнаруживаемый прирост ошибок: p1 = p0 + Δ

  steps:
    - weight: 5           # 5 % трафика на канарейку
      pauseSeconds: 60    # пауза перед следующим шагом
    - weight: 25
      pauseSeconds: 60
    - weight: 50
      pauseSeconds: 60
    - weight: 100         # полное продвижение
      pauseSeconds: 0

  paused: false           # true — приостановить прогрессию
  abort:  false           # true — немедленный откат на стабильную версию
```

### Поля статуса

```bash
kubectl get canaryrollout demo-rollout -n statgate-demo -o yaml
```

```yaml
status:
  phase: Running
  currentStep: 2
  currentWeight: 50
  message: "SPRT: collecting evidence"
  lastTransitionTime: "2026-04-19T10:30:00Z"
  analysisState:
    - name: error-rate
      logLikelihood: -0.518    # накопленный Λ
      observations: 312        # всего запросов канарейки
      failures: 3              # всего сбоев канарейки
      baselineRate: 0.010      # последняя p0 (частота ошибок стабильной версии)
      decision: pending        # pending | promote | rollback
```

---

## Математическая основа

SPRT (Wald, 1945) при каждом обновлении прибавляет к Λ:

```
Λ += k · ln(p1/p0) + (n−k) · ln((1−p1)/(1−p0))
```

где:
- `n` — новые запросы к канарейке с прошлого цикла
- `k` — новые сбои
- `p0` = текущая частота ошибок стабильной версии (H₀)
- `p1 = p0 + Δ` (H₁, гипотеза об ухудшении)
- `A = ln((1−β)/α)` — верхняя граница (откат)
- `B = ln(β/(1−α))` — нижняя граница (продвижение)

Теорема Вальда гарантирует α′ ≤ α/(1−β) и β′ ≤ β/(1−α) — формальные
ограничения на ошибки, недостижимые при пороговых подходах.

---

## Сравнение с Flagger

| Свойство | Flagger (порог) | StatGate (SPRT) |
|---|---|---|
| Ложный откат при случайном всплеске | Возможен | Нет: Λ компенсируется последующим чистым трафиком |
| Обнаружение малой деградации (Δ < порога) | Не обнаруживает | Обнаруживает при достаточном объёме |
| Формальные гарантии ошибок | Нет | α ≤ 5 %, β ≤ 5 % (доказано) |
| Объём выборки | Фиксирован | Адаптивен (теорема Вальда об оптимальности) |
| Память между окнами | Нет | Λ накапливается по всем циклам |

Детальные сценарии сравнения: [demo/flagger/comparison.md](demo/flagger/comparison.md).

---

## Команды Makefile

| Команда | Описание |
|---|---|
| `make build` | Собрать бинарный файл контроллера |
| `make build-cli` | Собрать `bin/statctl` |
| `make run` | Запустить контроллер локально (вне кластера) |
| `make docker-build` | Собрать Docker-образ контроллера |
| `make docker-build-demo` | Собрать образы демо-приложения (v1, v2) |
| `make install` | Установить CRD в кластер |
| `make uninstall` | Удалить CRD из кластера |
| `make deploy` | Установить/обновить контроллер через Helm |
| `make undeploy` | Удалить Helm-релиз контроллера |
| `make demo` | Применить демо-манифесты |
| `make demo-clean` | Удалить демо-ресурсы |
| `make demo-loadtest` | Запустить k6 нагрузочный тест (нормальный сценарий) |
| `make demo-loadtest-error` | Запустить k6 с инъекцией ошибок (сценарий откатa) |
| `make test` | Запустить юнит-тесты |
| `make generate` | Сгенерировать deepcopy-методы |
| `make manifests` | Сгенерировать CRD-манифесты |

## Структура проекта

```
StatGate/
├── api/v1alpha1/           # CRD-типы (CanaryRollout, SPRT-типы)
├── cmd/                    # Точка входа контроллера
├── cmd/statctl/            # CLI-клиент statctl
├── config/crd/bases/       # Сгенерированный CRD YAML
├── demo/
│   ├── app/                # Демо-приложение (Go, Orders API)
│   ├── loadtest/           # k6 нагрузочный тест
│   ├── grafana/            # Grafana dashboard JSON
│   ├── flagger/            # Flagger Canary манифест и сравнение
│   └── manifests/          # Kubernetes манифесты демо-среды
├── helm/statgate/          # Helm-чарт контроллера
└── internal/controller/    # Логика reconciler + SPRT-анализатор
```
