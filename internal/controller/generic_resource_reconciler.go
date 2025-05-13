package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Resource struct {
	v1.Object
	v1.ObjectMeta
	v1.ObjectMetaAccessor
}

type ResourceReconcilerInterface[T Resource] interface {
	client.Client
	Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)
	observeResource(ctx context.Context, resource T) error
	createResource(ctx context.Context, resource T) error
	updateResource(ctx context.Context, resource T) error
	deleteResource(ctx context.Context, resource T) error
}

type ResourceReconciler[T Resource] struct {
	ResourceReconcilerInterface[T]
}

func (r *ResourceReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var resource T

	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		log.Error(err, "Unable to get Collection")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if resource.GetObjectMeta().GetDeletionTimestamp().IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(resource, FinaliserName) {
			controllerutil.AddFinalizer(resource, FinaliserName)
			if err := r.Update(ctx, resource); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer: %w", err)
			}

			meta.SetStatusCondition(resource.Status.Conditions, v1.Condition{
				Type:    conditionCollectionAvailable,
				Status:  v1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Reconciling...",
			})
			for _, value := range base_conditions {
				meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
					Type:    value,
					Status:  v1.ConditionUnknown,
					Reason:  "Reconciling",
					Message: "Reconciling...",
				})
			}
			if err := r.Status().Update(ctx, resource); err != nil {
				log.Error(err, "Failed to update conditions.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update conditons: %w", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(resource, FinaliserName) {
			// Delete the resource.
			if err := r.deleteResource(ctx, resource); err != nil {
				log.Error(err, "Failed to delete collection.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed reconciling non-existence: %w", err)
			}

			controllerutil.RemoveFinalizer(resource, FinaliserName)
			if err := r.Update(ctx, resource); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.observeResource(ctx, resource); err != nil {
		log.Error(err, "Failed to observe resource.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to observe collresourcection: %w", err)
	}
	if meta.IsStatusConditionTrue(resource.Status.Conditions, conditionCollectionExists) {
		all_clear := true
		for _, condition := range base_conditions {
			if !meta.IsStatusConditionTrue(resource.Status.Conditions, condition) {
				all_clear = false
				break
			}
		}
		if !all_clear {
			if err := r.updateResource(ctx, resource); err != nil {
				log.Error(err, "Failed to update resource.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update resource: %w", err)
			}
			log.Info("Updated resource.")
		} else {
			log.Info("Resource has nothing to update.")
		}
	} else if err := r.createResource(ctx, resource); err != nil {
		log.Error(err, "Failed to create resource.")
	} else {
		log.Info("Created resource.")
	}

	return ctrl.Result{}, nil
}
