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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	solrv1alpha1 "github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	"github.com/discoverygarden/solr-user-operator/solr"
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	FinaliserName = "solr.dgicloud.com/finalizer"
)

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
		log.Error(err, "Unable to get User")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	solrClient, err := r.getClientForSolrCloud(ctx, user.Spec.SolrCloudRef)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get client instance for user %s/%s for cloud %s/%s: %w", user.Namespace, user.Name, user.Spec.SolrCloudRef.Namespace, user.Spec.SolrCloudRef.Name, err)
	}

	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(&user, FinaliserName) {
			controllerutil.AddFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&user, FinaliserName) {
			// TODO: Implement deletion
			if res, err := r.reconcileUser(ctx, solrClient, &user, false); err != nil {
				return res, fmt.Errorf("failed reconciling non-existence of %s/%s: %w", user.Namespace, user.Name, err)
			}

			controllerutil.RemoveFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if res, err := r.reconcileUser(ctx, solrClient, &user, true); err != nil {
		return res, fmt.Errorf("failed reconciling existence of %s/%s: %w", user.Namespace, user.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *UserReconciler) getClientForSolrCloud(ctx context.Context, ref solrv1alpha1.SolrCloudRef) (*solr.Client, error) {
	adminPassword, err := r.getAdminPassword(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire admin password")
	}

	return &solr.Client{
		User:     "admin",
		Password: adminPassword,
		Endpoint: fmt.Sprintf("http://%s-solrcloud-headless.%s:8983", ref.Name, ref.Namespace),
	}, nil
}

func (r *UserReconciler) getSecret(ctx context.Context, user *v1alpha1.User) (*corev1.Secret, error) {
	ref := types.NamespacedName{
		Name:      user.Spec.Secret.Name,
		Namespace: user.Spec.Secret.Namespace,
	}
	var secret corev1.Secret
	err := r.Get(ctx, ref, &secret)
	if err != nil {
		return nil, fmt.Errorf("failed to load secret for %v, %s: %w", user, ref, err)
	}
	return &secret, nil
}

type Credentials struct {
	Username string
	Password string
}

func (r *UserReconciler) getCredentials(ctx context.Context, user *v1alpha1.User) (*Credentials, error) {
	log := logf.FromContext(ctx)
	var creds Credentials

	secret, err := r.getSecret(ctx, user)
	if err == nil {
		// Get info from secret.
		user_key := user.Spec.Secret.UsernameKey
		if user_key == "" {
			user_key = "username"
		}
		username, ok := secret.Data[user_key]
		if ok {
			creds.Username = string(username)
		} else {
			// Log failure to acquire username from secret.
			log.Info("failed to acquire username from secret", "coords", fmt.Sprintf("%s/%s:%s", secret.Namespace, secret.Name, user_key))
		}
		password_key := user.Spec.Secret.PasswordKey
		if password_key == "" {
			password_key = "password"
		}
		password, ok := secret.Data[password_key]
		if ok {
			creds.Password = string(password)
		} else {
			// Log failure to acquire password from secret.
			log.Info("failed to acquire password from secret", "coords", fmt.Sprintf("%s/%s:%s", secret.Namespace, secret.Name, password_key))
		}
	}
	if creds.Username == "" {
		if user.Status.Username != "" {
			// Populate username from status.
			creds.Username = user.Status.Username
			// Log use of username from status.
			// XXX: Primarily foreseeing potential of the secret being deleted
			// before the user resource, but then wanting to later manage the
			// user resource.
			log.Info("fell back to use username from status", "found-username", creds.Username)
		} else {
			return nil, fmt.Errorf("failed to determine creds for %s/%s", user.Namespace, user.Name)
		}
	}

	return &creds, nil
}

func (r *UserReconciler) reconcileUser(ctx context.Context, solrClient *solr.Client, user *v1alpha1.User, ensure_exists_state bool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	creds, err := r.getCredentials(ctx, user)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get credentials for %s/%s: %w", user.Namespace, user.Name, err)
	}

	exists, err := solrClient.CheckUserExistence(creds.Username)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed check for user existence: %w", err)
	}

	if ensure_exists_state {
		// TODO: Ensure the user exists (with the given password).
		if !exists {
			// User doesn't exist; create.
			if err = solrClient.CreateUser(creds.Username, creds.Password); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create user %s on %s: %w", creds.Username, solrClient.Endpoint, err)
			} else {
				log.Info("Created user.")
				return ctrl.Result{}, nil
			}
		} else {
			// User exists; update password if appropriate.
			ok, err := solrClient.CheckUser(creds.Username, creds.Password)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to compare password for %s on %s: %w", creds.Username, solrClient.Endpoint, err)
			}
			if !ok {
				if err = solrClient.UpdateUser(creds.Username, creds.Password); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update password for %s on %s: %w", creds.Username, solrClient.Endpoint, err)
				} else {
					log.Info("Updated password.")
					return ctrl.Result{}, nil
				}
			} else {
				log.Info("Nothing to do.")
				return ctrl.Result{}, nil
			}
		}
	} else {
		// TODO: Delete the user (if it exists).
		if !exists {
			log.Info("User does not appear to exist in Solr.")
			return ctrl.Result{}, nil
		} else {
			if err = solrClient.DeleteUser(creds.Username); err != nil {
				log.Error(err, "Failed to delete user.")
				return ctrl.Result{}, fmt.Errorf("failed to delete user %s of %s: %w", creds.Username, solrClient.Endpoint, err)
			} else {
				log.Info("Deleted user.")
				return ctrl.Result{}, nil
			}
		}
	}
}

func (r *UserReconciler) getAdminPassword(ctx context.Context, solrCloudRef solrv1alpha1.SolrCloudRef) (string, error) {
	log := logf.FromContext(ctx)
	var adminSecret corev1.Secret
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: solrCloudRef.Namespace,
			Name:      fmt.Sprintf("%s-solrcloud-security-bootstrap", solrCloudRef.Name),
		},
		&adminSecret,
	)
	if err != nil {
		log.Error(err, "Unable to fetch admin secret.")
		return "", err
	}
	adminPasswordBytes, ok := adminSecret.Data["admin"]
	if !ok {
		log.Info("Failed to get admin secret.")
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
		WatchesMetadata(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				log := logf.FromContext(ctx)
				var list solrv1alpha1.UserList
				requests := []reconcile.Request{}
				err := r.List(ctx, &list)
				if err != nil {
					log.Error(err, "Failed to enumerate users while watching secrets.")
					return requests
				}

				for _, element := range list.Items {
					if element.Spec.Secret.Namespace == obj.GetNamespace() && element.Spec.Secret.Name == obj.GetName() {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Namespace: element.Namespace,
								Name:      element.Name,
							},
						})
					}
				}

				return requests
			}),
		).
		// WatchesMetadata(
		// 	&socv1beta1.SolrCloud{},
		// 	handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		// 		log := logf.FromContext(ctx)
		// 		var list solrv1alpha1.UserList
		// 		requests := []reconcile.Request{}
		// 		err := r.List(ctx, &list)
		// 		if err != nil {
		// 			log.Error(err, "Failed to enumerate users while watching Solr Cloud instances.")
		// 			return requests
		// 		}

		// 		for _, element := range list.Items {
		// 			if element.Spec.SolrCloudRef.Namespace == obj.GetNamespace() && element.Spec.SolrCloudRef.Name == obj.GetName() {
		// 				requests = append(requests, reconcile.Request{
		// 					NamespacedName: types.NamespacedName{
		// 						Namespace: element.Namespace,
		// 						Name:      element.Name,
		// 					},
		// 				})
		// 			}
		// 		}

		// 		return requests
		// 	}),
		// ).
		Complete(r)
}
