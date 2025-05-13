/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	solrv1alpha1 "github.com/discoverygarden/solr-user-operator/api/v1alpha1"
)

// CollectionReconciler reconciles a Collection object
type CollectionReconciler struct {
	client.Client
	SolrClientAware
	Scheme *runtime.Scheme
}

const (
	conditionCollectionAvailable              string = "Available"
	conditionCollectionConfigMapAvailable     string = "ConfigMapAvailable"
	conditionCollectionConfigMapExists        string = "ConfigMapExists"
	conditionCollectionConfigMapHasCollection string = "ConfigMapHasCollection"
	conditionCollectionConfigMapHasRole       string = "ConfigMapHasRole"
	configMapCollectionKey                    string = "collectionName"
	configMapRoleKey                          string = "rwRole"
)

// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=collections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=collections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=collections/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Collection object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *CollectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var collection v1alpha1.Collection

	if err := r.Get(ctx, req.NamespacedName, &collection); err != nil {
		log.Error(err, "Unable to get Collection")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.solrClientFactory == nil {
		// Assign default service/factory, if not provided.
		r.solrClientFactory = r.getClient
	}

	base_conditions := []string{
		conditionCollectionConfigMapAvailable,
		conditionCollectionConfigMapExists,
		conditionCollectionConfigMapHasCollection,
	}

	if collection.Spec.CreateRWRole {
		base_conditions = append(base_conditions, conditionCollectionConfigMapHasRole)
	}

	if collection.ObjectMeta.DeletionTimestamp.IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(&collection, FinaliserName) {
			controllerutil.AddFinalizer(&collection, FinaliserName)
			if err := r.Update(ctx, &collection); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer: %w", err)
			}

			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
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
			if err := r.Status().Update(ctx, &collection); err != nil {
				log.Error(err, "Failed to update conditions.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update conditons: %w", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&collection, FinaliserName) {
			// Delete the collection.
			if err := r.deleteCollection(ctx, &collection); err != nil {
				log.Error(err, "Failed to delete collection.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed reconciling non-existence: %w", err)
			}

			controllerutil.RemoveFinalizer(&collection, FinaliserName)
			if err := r.Update(ctx, &collection); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.observeCollection(ctx, &collection); err != nil {
		log.Error(err, "Failed to observe collection.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to observe collection: %w", err)
	}
	if meta.IsStatusConditionTrue(collection.Status.Conditions, conditionCollectionConfigMapAvailable) {
		all_clear := true
		for _, condition := range base_conditions {
			if !meta.IsStatusConditionTrue(collection.Status.Conditions, condition) {
				all_clear = false
				break
			}
		}
		if !all_clear {
			if err := r.updateCollection(ctx, &collection); err != nil {
				log.Error(err, "Failed to update collection.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update collection: %w", err)
			}
			log.Info("Updated collection.")
		} else {
			log.Info("Collection has nothing to update.")
		}
	} else if err := r.createCollection(ctx, &collection); err != nil {
		log.Error(err, "Failed to create collection.")
	} else {
		log.Info("Created collection.")
	}

	return ctrl.Result{}, nil
}

func (r *CollectionReconciler) getConfigMap(ctx context.Context, collection *v1alpha1.Collection) (*corev1.ConfigMap, error) {
	var config_map corev1.ConfigMap

	if err := r.Get(ctx, collection.Spec.Map.ToObjectKey(), &config_map); err != nil {
		return nil, err
	} else {
		return &config_map, nil
	}
}

func (r *CollectionReconciler) observeCollection(ctx context.Context, collection *v1alpha1.Collection) error {
	log := logf.FromContext(ctx)

	if config_map, err := r.getConfigMap(ctx, collection); err != nil {
		conditions := []string{
			conditionCollectionConfigMapExists,
			conditionCollectionConfigMapHasCollection,
			conditionCollectionConfigMapHasRole,
		}
		if client.IgnoreNotFound(err) == nil {
			for _, condition := range conditions {
				meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
					Type:    condition,
					Status:  v1.ConditionFalse,
					Reason:  "ConfigMapDoesNotExist",
					Message: "The target config map does not exist.",
				})
			}
		} else {
			log.Error(err, "Failed to get the config map.")
			for _, condition := range conditions {
				meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
					Type:    condition,
					Status:  v1.ConditionUnknown,
					Reason:  "ConfigMapLookupFailed",
					Message: fmt.Sprintf("Failed to get the config map: %v", err),
				})
			}
			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
				Type:   conditionCollectionConfigMapAvailable,
				Status: v1.ConditionFalse,
			})
		}
	} else {
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapExists,
			Status:  v1.ConditionTrue,
			Reason:  "FoundConfigMap",
			Message: "Found the target config map.",
		})
		if value, ok := config_map.Data[configMapCollectionKey]; ok {
			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
				Type:    conditionCollectionConfigMapHasCollection,
				Status:  v1.ConditionTrue,
				Reason:  "ConfigMapHasCollection",
				Message: "Found collection name in the config map.",
			})
			collection.Status.Name = value
		} else {
			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
				Type:    conditionCollectionConfigMapHasCollection,
				Status:  v1.ConditionFalse,
				Reason:  "ConfigMapMissingCollection",
				Message: "Collection name missing from config map.",
			})
		}

		if value, ok := config_map.Data[configMapRoleKey]; ok {
			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
				Type:    conditionCollectionConfigMapHasRole,
				Status:  v1.ConditionTrue,
				Reason:  "ConfigMapHasRole",
				Message: "Found role name in the config map.",
			})
			collection.Status.RWRole = value
		} else {
			meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
				Type:    conditionCollectionConfigMapHasRole,
				Status:  v1.ConditionFalse,
				Reason:  "ConfigMapMissingRole",
				Message: "Role name missing from config map.",
			})
		}
	}

	r.checkMapStatus(collection)

	return nil
}

func (r *CollectionReconciler) checkMapStatus(collection *v1alpha1.Collection) {
	available_conditions := []string{
		conditionCollectionConfigMapExists,
		conditionCollectionConfigMapHasCollection,
	}
	if collection.Spec.CreateRWRole {
		available_conditions = append(available_conditions, conditionCollectionConfigMapHasRole)
	}
	condition_status := make([]bool, len(available_conditions))
	for index, value := range available_conditions {
		condition_status[index] = meta.IsStatusConditionTrue(collection.Status.Conditions, value)
	}
	if !slices.Contains(condition_status, false) {
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapAvailable,
			Status:  v1.ConditionTrue,
			Reason:  "Ready",
			Message: "Requirements met!",
		})
	} else {
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapAvailable,
			Status:  v1.ConditionFalse,
			Reason:  "NotReady",
			Message: "Not all requirements have been met...",
		})
	}
}

func (r *CollectionReconciler) createCollection(ctx context.Context, collection *v1alpha1.Collection) error {
	return nil
}

func (r *CollectionReconciler) updateCollection(ctx context.Context, collection *v1alpha1.Collection) error {
	log := logf.FromContext(ctx)
	var config_map corev1.ConfigMap
	if !meta.IsStatusConditionTrue(collection.Status.Conditions, conditionCollectionConfigMapExists) {
		config_map = corev1.ConfigMap{
			ObjectMeta: v1.ObjectMeta{
				Namespace: collection.Spec.Map.Namespace,
				Name:      collection.Spec.Map.Name,
			},
		}
		if err := r.Create(ctx, &config_map); err != nil {
			return fmt.Errorf("failed to create config map: %w", err)
		}
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapExists,
			Status:  v1.ConditionTrue,
			Reason:  "Created",
			Message: "Created config map.",
		})
		if err := controllerutil.SetOwnerReference(collection, &config_map, r.Scheme); err != nil {
			log.Error(err, "Failed to set owner reference; proceeding anyway.")
		}
	} else if cmap, err := r.getConfigMap(ctx, collection); err != nil {
		return fmt.Errorf("failed to get config map to update: %w", err)
	} else {
		config_map = *cmap
	}
	if config_map.Data == nil {
		config_map.Data = make(map[string]string)
	}

	config_map_dirty := false

	if !meta.IsStatusConditionTrue(collection.Status.Conditions, conditionCollectionConfigMapHasCollection) {
		// Generate collection name and add to the config map.
		suffix := make([]byte, 4)
		if _, err := rand.Read(suffix); err != nil {
			log.Error(err, "Failed to generate random suffix for collection.")
			return fmt.Errorf("failed to generate random suffix for collection: %w", err)
		}

		collection_name := fmt.Sprintf(
			"coll--%s--%x",
			collection.Namespace,
			suffix,
		)
		collection.Status.Name = collection_name
		config_map.Data[configMapCollectionKey] = collection_name
		config_map_dirty = true
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapHasCollection,
			Status:  v1.ConditionTrue,
			Reason:  "GeneratedCollectionName",
			Message: "Generated collection name.",
		})
	}

	if collection.Spec.CreateRWRole && !meta.IsStatusConditionTrue(collection.Status.Conditions, conditionCollectionConfigMapHasRole) {
		// TODO: Generate R/W role name
		config_map_dirty = true
		meta.SetStatusCondition(&collection.Status.Conditions, v1.Condition{
			Type:    conditionCollectionConfigMapHasRole,
			Status:  v1.ConditionTrue,
			Reason:  "GeneratedRole",
			Message: "Generate role.",
		})
	}

	if config_map_dirty {
		if err := r.Update(ctx, &config_map); err != nil {
			log.Error(err, "Failed to update config map.")
			return fmt.Errorf("failed to update config map: %w", err)
		}
		log.Info("Updated config map.")
	} else {
		// Theoretically, shouldn't get here... Should only call
		// "updateCollection" when we will be updating something.
		log.Info("Config map was not dirty, so did not update config map.")
	}

	r.checkMapStatus(collection)

	return nil
}

func (r *CollectionReconciler) deleteCollection(ctx context.Context, collection *v1alpha1.Collection) error {
	// Handle collection deletion.
	if collection.Spec.RemovalPolicy == "delete" {
		// TODO: Actually delete the collection from Solr.
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CollectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&solrv1alpha1.Collection{}).
		Named("collection").
		Complete(r)
}
