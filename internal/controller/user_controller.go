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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	solrv1alpha1 "github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	"github.com/discoverygarden/solr-user-operator/solr"
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the User object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var user solrv1alpha1.User
	if err := r.Get(ctx, req.NamespacedName, &user); err != nil {
		log.Error(err, "unable to get User")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	userFinalizerName := "solr.dgicloud.com/finalizer"
	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(&user, userFinalizerName) {
			controllerutil.AddFinalizer(&user, userFinalizerName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "failed to add finalizer")
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&user, userFinalizerName) {
			// TODO: Implement deletion

			controllerutil.RemoveFinalizer(&user, userFinalizerName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "failed to remove finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	password, res, err := r.reconcileCredential(ctx, &user)
	if err != nil {
		return res, err
	}
	adminPassword, err := r.getAdminPassword(ctx, req, user.Spec.SolrCloudRef)
	if err != nil {
		return ctrl.Result{}, nil
	}

	solrClient := solr.Client{
		User:     "admin",
		Password: adminPassword,
		// Host:     fmt.Sprintf("solr-solrcloud-headless.%s", req.Namespace),
		Host: "localhost",
		Port: "8983",
	}
	if res, err = r.reconcileUser(ctx, &solrClient, &user, password); err != nil {
		return res, err
	}

	return ctrl.Result{}, nil
}

func (r *UserReconciler) reconcileCredential(ctx context.Context, user *v1alpha1.User) (string, ctrl.Result, error) {

	return "test", ctrl.Result{}, nil
}

func (r *UserReconciler) reconcileUser(ctx context.Context, solrClient *solr.Client, user *v1alpha1.User, password string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ok, err := solrClient.CheckUser(user.Name, password)
	if err != nil {
		log.Error(err, "failed to list user")
		return ctrl.Result{}, err
	}
	if ok {
		log.Info("User is reconciled")
		return ctrl.Result{}, nil
	}

	log.Info("User does not exist or has incorrect password")
	err = solrClient.CreateUser(user.Name, password)
	if err != nil {
		log.Error(err, "failed to create user")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil

}

func (r *UserReconciler) getAdminPassword(ctx context.Context, req ctrl.Request, solrCloudRef solrv1alpha1.SolrCloudRef) (string, error) {
	log := logf.FromContext(ctx)
	var adminSecret corev1.Secret
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: req.Namespace,
			Name:      fmt.Sprintf("%s-solrcloud-security-bootstrap", solrCloudRef.Name),
		},
		&adminSecret,
	)
	if err != nil {
		log.Error(err, "unable to fetch admin secret")
		return "", err
	}
	adminPasswordBytes, ok := adminSecret.Data["admin"]
	if !ok {
		log.Info("failed to get admin secret")
		return "", client.IgnoreNotFound(err)
	}
	adminPassword := string(adminPasswordBytes)
	return adminPassword, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&solrv1alpha1.User{}).
		Named("user").
		Complete(r)
}
