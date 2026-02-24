# StatGate — Контроллер прогрессивной доставки сервисов в Kubernetes

StatGate — это Kubernetes-оператор, автоматизирующий канареечные развёртывания путём постепенного переключения трафика
между стабильной и канареечной версиями через управление весами Istio VirtualService.

## Архитектура

![](/docs/architecture.svg)

Контроллер отслеживает ресурсы `CanaryRollout` и пошагово увеличивает вес трафика на канареечную версию в Istio
VirtualService с настраиваемыми паузами между шагами. Перед каждым увеличением веса проверяется готовность подов
канареечной версии. Поддерживаются пауза/возобновление и немедленный откат.

## Фазы развёртывания

| Фаза       | Описание                                                  |
|------------|-----------------------------------------------------------|
| `Pending`  | Развёртывание создано, ещё не начато                      |
| `Running`  | Идёт пошаговое переключение трафика                       |
| `Paused`   | Приостановлено пользователем (`spec.paused: true`)        |
| `Promoted` | Канареечная версия достигла 100%, развёртывание завершено |
| `Aborted`  | Откат на 100% стабильной версии (`spec.abort: true`)      |
| `Failed`   | Поды канареечной версии не готовы в течение тайм-аута     |

## Предварительные требования

- Go 1.23+
- Docker
- Kubernetes-кластер (1.28+)
- [Istio](https://istio.io/), установленный в кластере
- kubectl
- Helm 3 (для установки через Helm-чарт)

## Быстрый старт

### 1. Сборка контроллера

```bash
# Собрать бинарный файл
make build

# Собрать Docker-образ
make docker-build IMG=statgate-controller:latest
```

### 2. Установка CRD

```bash
make install
```

### 3. Развёртывание контроллера

```bash
# Через Helm
helm upgrade --install statgate helm/statgate/ \
  --namespace statgate-system \
  --create-namespace \
  --set image.repository=statgate-controller \
  --set image.tag=latest
```

Или запустить локально для разработки:

```bash
make run
```

### 4. Запуск демо

```bash
# Собрать образы демо-приложения
make docker-build-demo

# Развернуть демо-ресурсы
kubectl apply -f demo/manifests/

# Наблюдать за ходом развёртывания
kubectl get canaryrollout -n statgate-demo -w
```

### 5. Управление развёртыванием

```bash
# Приостановить развёртывание
kubectl patch canaryrollout demo-rollout -n statgate-demo \
  --type merge -p '{"spec":{"paused":true}}'

# Возобновить развёртывание
kubectl patch canaryrollout demo-rollout -n statgate-demo \
  --type merge -p '{"spec":{"paused":false}}'

# Прервать и откатить на стабильную версию
kubectl patch canaryrollout demo-rollout -n statgate-demo \
  --type merge -p '{"spec":{"abort":true}}'
```

## Справочник CRD (пример)

```yaml
apiVersion: statgate.io/v1alpha1
kind: CanaryRollout
metadata:
  name: my-rollout
spec:
  targetRef: my-canary-deployment      # Имя канареечного Deployment
  stableServiceRef: my-stable-svc      # Service для стабильных подов
  canaryServiceRef: my-canary-svc      # Service для канареечных подов
  virtualServiceRef: my-virtualservice  # Istio VirtualService для патчинга весов
  steps:
    - weight: 5                         # 5% трафика на канареечную версию
      pauseSeconds: 60                  # Ожидание 60с перед следующим шагом
    - weight: 25
      pauseSeconds: 60
    - weight: 50
      pauseSeconds: 60
    - weight: 100                       # Полное продвижение
      pauseSeconds: 0
  paused: false                         # Установить true для паузы
  abort: false                          # Установить true для отката
```

### Статус (пример)

```bash
$ kubectl get canaryrollout -n statgate-demo
NAME           PHASE     WEIGHT   STEP   AGE
demo-rollout   Running   25       1      2m
```

## Команды Makefile

| Команда                  | Описание                                |
|--------------------------|-----------------------------------------|
| `make build`             | Собрать бинарный файл контроллера       |
| `make run`               | Запустить контроллер локально           |
| `make docker-build`      | Собрать Docker-образ контроллера        |
| `make docker-build-demo` | Собрать образы демо-приложения (v1, v2) |
| `make install`           | Установить CRD в кластер                |
| `make deploy`            | Развернуть контроллер через Helm        |
| `make demo`              | Применить демо-манифесты                |
| `make test`              | Запустить тесты                         |
