package providers

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// kubeStatusFields is the bounded set of readable status fields per kind.
// Every field is an Integer count maintained by the workload controller.
var kubeStatusFields = map[schema.GroupVersionKind]map[string]bool{
	{Group: "apps", Version: "v1", Kind: "Deployment"}: {
		"replicas": true, "readyReplicas": true, "availableReplicas": true, "updatedReplicas": true,
	},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"}: {
		"replicas": true, "readyReplicas": true, "availableReplicas": true, "updatedReplicas": true,
	},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"}: {
		"desiredNumberScheduled": true, "numberReady": true, "numberAvailable": true, "updatedNumberScheduled": true,
	},
}

// KubeStatusConfig maps assertion slots to status fields.
type KubeStatusConfig struct {
	// Fields maps a slot name to a status field of the target's kind.
	Fields map[string]string `json:"fields"`
}

// KubeStatus reads one Integer status fact of the request's own targetRef.
//
// Observation basis: the fact is the workload controller's status as recorded
// in the API server for the object's current generation. A value is emitted
// only when status.observedGeneration >= metadata.generation; while the
// controller has not yet observed the current spec the provider reports
// unavailable instead of presenting a status for an older spec as current.
// ObservedAt is the read time of that recorded status. The provider cannot
// detect a stalled workload controller that stops updating status; that
// limit is documented, not hidden.
type KubeStatus struct {
	Reader client.Reader
	Config KubeStatusConfig
	Now    func() time.Time
}

// Observe performs one GET of the subject in its own namespace.
func (k *KubeStatus) Observe(ctx context.Context, req Request) Result {
	gv, err := schema.ParseGroupVersion(req.Subject.APIVersion)
	if err != nil {
		return Result{Unavailable: "unsupported-kind"}
	}
	gvk := gv.WithKind(req.Subject.Kind)
	allowed, ok := kubeStatusFields[gvk]
	if !ok {
		return Result{Unavailable: "unsupported-kind"}
	}
	field, ok := k.Config.Fields[req.Key.Slot]
	if !ok || !allowed[field] {
		return Result{Unavailable: "slot-not-configured"}
	}
	if req.SlotType != operations.TypeInteger {
		return Result{Unavailable: "slot-type-not-integer"}
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	err = k.Reader.Get(ctx, types.NamespacedName{Namespace: req.Subject.Namespace, Name: req.Subject.Name}, obj)
	readAt := k.Now()
	switch {
	case apierrors.IsNotFound(err):
		return Result{Unavailable: "subject-not-found"}
	case err != nil:
		return Result{Unavailable: "read-failed"}
	}
	if string(obj.GetUID()) != req.Subject.ResolvedUID {
		return Result{Unavailable: "subject-uid-mismatch"}
	}
	observedGen, found, err := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
	if err != nil || !found {
		return Result{Unavailable: "status-not-observed"}
	}
	if observedGen < obj.GetGeneration() {
		return Result{Unavailable: "status-lagging-generation"}
	}
	// Zero-valued counts are omitted from status; absence means 0 once the
	// controller has observed this generation.
	v, _, err := unstructured.NestedInt64(obj.Object, "status", field)
	if err != nil {
		return Result{Unavailable: "malformed-status"}
	}
	return Result{
		Value:      operations.Int(v),
		ObservedAt: readAt,
		// Stable handle: same object, field and generation give the same
		// reference, so refreshing an unchanged fact does not churn status.
		EvidenceRef: fmt.Sprintf("k8s:%s/%s/%s@%s#status.%s,generation=%d",
			gvk.Kind, req.Subject.Namespace, req.Subject.Name, obj.GetUID(), field, obj.GetGeneration()),
	}
}
