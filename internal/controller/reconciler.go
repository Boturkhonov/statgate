package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

const readinessTimeout = 5 * time.Minute

// CanaryRolloutReconciler reconciles a CanaryRollout object.
type CanaryRolloutReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=statgate.io,resources=canaryrollouts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=statgate.io,resources=canaryrollouts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch

func (r *CanaryRolloutReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rollout statgatev1alpha1.CanaryRollout
	if err := r.Get(ctx, req.NamespacedName, &rollout); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Aborted is always terminal — rollback already performed.
	if rollout.Status.Phase == statgatev1alpha1.PhaseAborted {
		return ctrl.Result{}, nil
	}

	// Promoted is terminal unless the user explicitly sets spec.abort=true,
	// in which case we fall through to the abort handler below so that weights
	// are reset back to stable=100 / canary=0.
	if rollout.Status.Phase == statgatev1alpha1.PhasePromoted && !rollout.Spec.Abort {
		return ctrl.Result{}, nil
	}

	// --- Abort ---
	if rollout.Spec.Abort {
		logger.Info("abort requested, rolling back to stable")
		if err := SetVirtualServiceWeights(
			ctx, r.Client, rollout.Namespace, rollout.Spec.VirtualServiceRef,
			rollout.Spec.StableServiceRef, 100,
			rollout.Spec.CanaryServiceRef, 0,
		); err != nil {
			return ctrl.Result{}, fmt.Errorf("rollback weights: %w", err)
		}
		return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseAborted, rollout.Status.CurrentStep, 0, "Rollout aborted, 100% traffic to stable")
	}

	// --- Pause ---
	if rollout.Spec.Paused {
		if rollout.Status.Phase != statgatev1alpha1.PhasePaused {
			logger.Info("pausing rollout")
			return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhasePaused, rollout.Status.CurrentStep, rollout.Status.CurrentWeight, "Rollout paused by user")
		}
		return ctrl.Result{}, nil
	}

	// Unpaused: if we were paused, reset the timer so pause duration doesn't count.
	if rollout.Status.Phase == statgatev1alpha1.PhasePaused {
		logger.Info("resuming rollout")
		return ctrl.Result{RequeueAfter: time.Second}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseRunning, rollout.Status.CurrentStep, rollout.Status.CurrentWeight, "Rollout resumed")
	}

	// --- Initialize ---
	if rollout.Status.Phase == "" || rollout.Status.Phase == statgatev1alpha1.PhasePending {
		logger.Info("initializing rollout")
		return ctrl.Result{RequeueAfter: time.Second}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseRunning, 0, 0, "Rollout started")
	}

	steps := rollout.Spec.Steps
	currentStep := int(rollout.Status.CurrentStep)

	if currentStep >= len(steps) {
		return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhasePromoted, rollout.Status.CurrentStep, 100, "Rollout promoted, 100% traffic to canary")
	}

	// --- Pod readiness check ---
	ready, err := r.isCanaryReady(ctx, rollout.Namespace, rollout.Spec.TargetRef)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check canary readiness: %w", err)
	}
	if !ready {
		elapsed := time.Since(rollout.Status.LastTransitionTime.Time)
		if elapsed > readinessTimeout {
			logger.Info("canary pods not ready, timeout exceeded")
			_ = SetVirtualServiceWeights(
				ctx, r.Client, rollout.Namespace, rollout.Spec.VirtualServiceRef,
				rollout.Spec.StableServiceRef, 100,
				rollout.Spec.CanaryServiceRef, 0,
			)
			return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseFailed, rollout.Status.CurrentStep, 0, "Canary pods not ready, timeout exceeded")
		}
		logger.Info("waiting for canary pods to become ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// --- Current step ---
	step := steps[currentStep]
	pauseDuration := time.Duration(step.PauseSeconds) * time.Second
	elapsed := time.Since(rollout.Status.LastTransitionTime.Time)

	// --- Apply current step weight ---
	if rollout.Status.CurrentWeight != step.Weight {
		canaryWeight := step.Weight
		stableWeight := int32(100) - canaryWeight

		logger.Info("setting traffic weights", "step", currentStep, "canary", canaryWeight, "stable", stableWeight)
		if err := SetVirtualServiceWeights(
			ctx, r.Client, rollout.Namespace, rollout.Spec.VirtualServiceRef,
			rollout.Spec.StableServiceRef, stableWeight,
			rollout.Spec.CanaryServiceRef, canaryWeight,
		); err != nil {
			return ctrl.Result{}, fmt.Errorf("set weights at step %d: %w", currentStep, err)
		}

		msg := fmt.Sprintf("Step %d: canary weight set to %d%%", currentStep, canaryWeight)
		return ctrl.Result{RequeueAfter: pauseDuration}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseRunning, int32(currentStep), canaryWeight, msg)
	}

	analysisInterval := 10 * time.Second
	if rollout.Spec.Analysis != nil && rollout.Spec.Analysis.AnalysisIntervalSeconds > 0 {
		analysisInterval = time.Duration(rollout.Spec.Analysis.AnalysisIntervalSeconds) * time.Second
	}

	// --- SPRT analysis (runs on EVERY reconcile during pause) ---
	// We deliberately do NOT call r.Status().Update() here. All status
	// mutations are collected in-memory and flushed in exactly ONE update
	// per reconcile cycle at the return point below. This prevents the
	// "object has been modified" conflict that arises when two Update calls
	// race against each other within the same reconcile.
	if rollout.Spec.PrometheusURL != "" && rollout.Spec.Analysis != nil {
		decision, newState, reason, err := RunSPRT(
			ctx, rollout.Spec.PrometheusURL,
			rollout.Spec.Analysis,
			rollout.Status.AnalysisState,
		)

		if err != nil {
			// Transient error (Prometheus unreachable, etc.) — retry, do NOT abort.
			// Don't persist potentially partial state on error.
			logger.Error(err, "SPRT analysis error, will retry")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}

		// Accumulate state in memory; persisted at the single update below.
		rollout.Status.AnalysisState = newState
		rollout.Status.Message = reason

		if decision == SPRTRollback {
			logger.Info("SPRT: rollback decision reached", "reason", reason)
			_ = SetVirtualServiceWeights(
				ctx, r.Client, rollout.Namespace, rollout.Spec.VirtualServiceRef,
				rollout.Spec.StableServiceRef, 100,
				rollout.Spec.CanaryServiceRef, 0,
			)
			msg := fmt.Sprintf("SPRT rollback at step %d: %s", currentStep, reason)
			// setStatus calls r.Status().Update() — the one and only update
			// this cycle; AnalysisState set above is included automatically.
			return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseAborted,
				int32(currentStep), 0, msg)
		}
	}

	// --- Timer check (pause must still elapse before advancing) ---
	if elapsed < pauseDuration {
		remaining := pauseDuration - elapsed
		requeue := analysisInterval
		if remaining < requeue {
			requeue = remaining
		}
		logger.Info("waiting at current step", "step", currentStep, "remaining", remaining.Round(time.Second))
		// Single status update for this cycle: persists AnalysisState + Message
		// set by the SPRT block above without resetting LastTransitionTime.
		if err := r.Status().Update(ctx, &rollout); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	// --- SPRT gate: require explicit promote decision before advancing ---
	// Only applies when a pause is configured (pauseSeconds > 0) and analysis
	// is enabled. Steps with pauseSeconds=0 are "set weight and advance
	// immediately" steps — SPRT is not meaningful there.
	// Without this gate, the step would advance on timer expiry even when
	// SPRT is still collecting evidence (e.g. no real user traffic yet).
	if pauseDuration > 0 && rollout.Spec.Analysis != nil {
		allPromote := len(rollout.Status.AnalysisState) > 0
		for _, s := range rollout.Status.AnalysisState {
			if s.Decision != "promote" {
				allPromote = false
				break
			}
		}
		if !allPromote {
			msg := fmt.Sprintf("Step %d: timer elapsed, waiting for SPRT promote decision", currentStep)
			logger.Info(msg)
			rollout.Status.Message = msg
			// Single status update for this cycle.
			if err := r.Status().Update(ctx, &rollout); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: analysisInterval}, nil
		}
	}

	// --- Advance to next step ---
	// Reset SPRT state for the next step.
	rollout.Status.AnalysisState = nil

	nextStep := int32(currentStep + 1)
	if int(nextStep) >= len(steps) {
		logger.Info("all steps completed, promoting")
		return ctrl.Result{}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhasePromoted, nextStep, 100, "Rollout promoted, 100% traffic to canary")
	}

	msg := fmt.Sprintf("Advancing to step %d", nextStep)
	logger.Info(msg)
	return ctrl.Result{RequeueAfter: time.Second}, r.setStatus(ctx, &rollout, statgatev1alpha1.PhaseRunning, nextStep, rollout.Status.CurrentWeight, msg)
}

func (r *CanaryRolloutReconciler) setStatus(
	ctx context.Context,
	rollout *statgatev1alpha1.CanaryRollout,
	phase statgatev1alpha1.RolloutPhase,
	step, weight int32,
	message string,
) error {
	rollout.Status.Phase = phase
	rollout.Status.CurrentStep = step
	rollout.Status.CurrentWeight = weight
	rollout.Status.LastTransitionTime = metav1.Now()
	rollout.Status.Message = message
	return r.Status().Update(ctx, rollout)
}

// isCanaryReady checks whether the canary Deployment has at least one Ready pod.
func (r *CanaryRolloutReconciler) isCanaryReady(ctx context.Context, namespace, deploymentName string) (bool, error) {
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: deploymentName}, &deploy); err != nil {
		return false, fmt.Errorf("get deployment %s: %w", deploymentName, err)
	}

	var podList corev1.PodList
	if err := r.List(ctx, &podList,
		client.InNamespace(namespace),
		client.MatchingLabels(deploy.Spec.Selector.MatchLabels),
	); err != nil {
		return false, fmt.Errorf("list pods for %s: %w", deploymentName, err)
	}

	for _, pod := range podList.Items {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CanaryRolloutReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&statgatev1alpha1.CanaryRollout{}).
		Complete(r)
}
