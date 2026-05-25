#!/usr/bin/env bash
# Общие функции для прогона Flagger-сценариев.
#
# Несоответствие топологий: StatGate работает с парой Deployment (demo-stable +
# demo-canary), а Flagger ожидает ОДИН Deployment, который сам клонирует в
# *-primary и *-canary. Поэтому при запуске сравнения с Flagger мы временно
# демонтируем StatGate-инфру, ставим единый Deployment "demo", запускаем
# Flagger, и затем восстанавливаем StatGate-инфру через reset.
#
# Используется так:
#   source "$(dirname "${BASH_SOURCE[0]}")/../_flagger.sh"
#   flagger_create_topology   # ставит baseline-инфру под Flagger
#   flagger_apply_canary "$SCRIPT_DIR"
#   flagger_trigger_canary ERROR_RATE_START=0.045 ...
#   flagger_reset             # после прогона

NS="${NS:-statgate-demo}"
DEMO_IMG="${DEMO_IMG:-statgate-demo}"

# Базовый Deployment demo + Service. Запускается со stable-параметрами
# (APP_VERSION=stable, без инжекции ошибок) — Flagger клонирует это в
# demo-primary, который остаётся "хорошей" версией на всё время прогона.
flagger_create_topology() {
    echo "→ Удаляю StatGate-инфру (если есть)…"
    kubectl -n "$NS" delete canaryrollout demo-rollout --ignore-not-found
    kubectl -n "$NS" delete deployment demo-stable demo-canary --ignore-not-found
    kubectl -n "$NS" delete service demo-stable demo-canary --ignore-not-found
    kubectl -n "$NS" delete virtualservice demo-vs --ignore-not-found

    echo "→ Создаю единый Deployment demo + Service demo (baseline)…"
    cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
  namespace: $NS
  labels:
    app: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      containers:
        - name: demo
          image: $DEMO_IMG:v1
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: "stable"
            - name: DATABASE_URL
              value: "postgres://demo:demo-password@postgres:5432/ordersdb?sslmode=disable"
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: demo
  namespace: $NS
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 8080
      targetPort: 8080
EOF

    kubectl -n "$NS" rollout status deployment/demo --timeout=180s
}

# Применить Flagger Canary CR и подождать, пока Flagger:
#   1. клонирует demo → demo-primary (новый Deployment),
#   2. перейдёт в phase=Initialized (Last Promoted Spec == текущий),
#   3. отскейлит исходный demo до 0 реплик.
# Только после этого имеет смысл триггерить новую версию.
flagger_apply_canary() {
    local scenario_dir="$1"
    echo "→ Применяю Flagger Canary…"
    kubectl apply -f "$scenario_dir/flagger-canary.yaml"

    echo "→ Жду, пока Flagger клонирует demo → demo-primary…"
    for i in $(seq 1 24); do
        if kubectl -n "$NS" get deployment demo-primary >/dev/null 2>&1; then
            break
        fi
        sleep 10
    done
    kubectl -n "$NS" wait --for=condition=Available deployment/demo-primary --timeout=240s

    echo "→ Жду, пока Canary перейдёт в phase=Initialized…"
    for i in $(seq 1 30); do
        local phase
        phase=$(kubectl -n "$NS" get canary demo -o jsonpath='{.status.phase}' 2>/dev/null || true)
        if [[ "$phase" == "Initialized" ]]; then
            echo "  Initialized."
            break
        fi
        sleep 5
    done
}

# Триггер canary deploy.
# Flagger после инициализации скейлит исходный demo до 0 реплик (только
# demo-primary держит трафик). Чтобы изменение pod template привело к
# rollout, нужно:
#   1. Обновить env у Deployment demo (kubectl set env).
#   2. Вручную scale up до желаемого числа реплик — это создаёт ReplicaSet
#      с новым hash и заставляет Flagger подхватить изменения.
# Без шага 2 Flagger игнорирует template-изменение (0 → 0 replicas).
flagger_trigger_canary() {
    echo "→ Триггер canary: применяю env-overrides $*"
    kubectl -n "$NS" set env deployment/demo APP_VERSION=canary "$@"

    echo "→ Scale up demo (Flagger мог отскейлить до 0)…"
    kubectl -n "$NS" scale deployment/demo --replicas=2
    kubectl -n "$NS" rollout status deployment/demo --timeout=120s

    echo "→ Готово. Жди ~25с пока Flagger детектит новый ReplicaSet."
    echo "   Прогресс: kubectl -n $NS describe canary demo  (поле Events)"
}

# Снести Flagger-топологию и восстановить StatGate-инфру через kustomize.
flagger_reset() {
    echo "→ Снос Flagger-инфры…"
    # Сначала удаляем Canary CR — Flagger finalizer пробует revert, дай ему время.
    kubectl -n "$NS" delete canary demo --ignore-not-found
    sleep 5

    # Все Flagger-managed и наши Service/Deployment удаляем явно. Без этого
    # kubectl apply -k делает merge-patch на оставшиеся от Flagger ресурсы и
    # падает с "Duplicate value: 'http'" (порт сливается, а не заменяется),
    # поскольку Flagger-created объекты не имеют last-applied-configuration.
    kubectl -n "$NS" delete deployment demo demo-primary --ignore-not-found
    kubectl -n "$NS" delete service demo demo-primary demo-canary demo-stable --ignore-not-found
    kubectl -n "$NS" delete virtualservice demo demo-vs --ignore-not-found

    # Дождаться, пока ресурсы реально пропадут (Service'ы иногда висят на
    # finalizer'ах Istio sidecar). Иначе apply -k встретит "недоудалённый"
    # Service и снова сольёт порты.
    for svc in demo demo-primary demo-canary demo-stable; do
        kubectl -n "$NS" wait --for=delete "svc/$svc" --timeout=30s 2>/dev/null || true
    done

    echo "→ Восстанавливаю StatGate-инфру…"
    local repo_root
    repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
    kubectl apply -k "$repo_root/demo/manifests/"
    kubectl -n "$NS" rollout status deployment/demo-stable --timeout=120s
    kubectl -n "$NS" rollout status deployment/demo-canary --timeout=120s
}
