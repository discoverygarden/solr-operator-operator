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

	"github.com/apache/solr-operator/api/v1beta1"
	"github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	"github.com/discoverygarden/solr-user-operator/solr"
	// +kubebuilder:scaffold:imports
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	SolrClientFactory func(ctx context.Context, user *v1alpha1.User) (solr.ClientInterface, error)
}

const (
	FinaliserName string = "solr.dgicloud.com/finalizer"
)

// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=solr.dgicloud.com,resources=users/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get
// +kubebuilder:rbac:groups=solr.apache.org,resources=solrclouds,verbs=get;list;watch

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

	var user v1alpha1.User
	if err := r.Get(ctx, req.NamespacedName, &user); err != nil {
		log.Error(err, "Unable to get User")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.SolrClientFactory == nil {
		// Assign default service/factory, if not provided.
		r.SolrClientFactory = r.getClient
	}
	solrClient, err := r.SolrClientFactory(ctx, &user)
	if err != nil {
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to use factory")
	}

	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(&user, FinaliserName) {
			controllerutil.AddFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer: %w", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&user, FinaliserName) {
			// Delete the user.
			if res, err := r.reconcileUser(ctx, solrClient, &user, false); err != nil {
				return res, fmt.Errorf("failed reconciling non-existence of %s/%s: %w", user.Namespace, user.Name, err)
			}

			controllerutil.RemoveFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if res, err := r.reconcileUser(ctx, solrClient, &user, true); err != nil {
		return res, fmt.Errorf("failed reconciling existence of %s/%s: %w", user.Namespace, user.Name, err)
	} else {
		return res, nil
	}
}

func getNamespace(u *v1alpha1.User, ref v1alpha1.ObjectRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	} else {
		return u.Namespace
	}
}

func (r *UserReconciler) getClient(ctx context.Context, user *v1alpha1.User) (solr.ClientInterface, error) {
	adminPassword, err := r.getAdminPassword(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire admin password")
	}

	ref := user.Spec.SolrCloudRef.Ref

	return &solr.Client{
		Context:  ctx,
		User:     "admin",
		Password: adminPassword,
		Endpoint: fmt.Sprintf("http://%s-solrcloud-headless.%s:8983", ref.Name, getNamespace(user, user.Spec.SolrCloudRef.Ref)),
	}, nil
}

func (r *UserReconciler) getSecret(ctx context.Context, user *v1alpha1.User) (*corev1.Secret, error) {
	ref := types.NamespacedName{
		Name:      user.Spec.Secret.Ref.Name,
		Namespace: getNamespace(user, user.Spec.Secret.Ref),
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

func (r *UserReconciler) getCredentialsFromSecret(ctx context.Context, user *v1alpha1.User) *Credentials {
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
	} else {
		log.Error(err, "Failed to load secret.")
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
			// Nothing to which to fallback; return nothing.
			return nil
		}
	}

	return &creds
}

func (r *UserReconciler) reconcileUser(ctx context.Context, solrClient solr.ClientInterface, user *v1alpha1.User, ensure_exists_state bool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	creds := r.getCredentialsFromSecret(ctx, user)
	if !ensure_exists_state && creds == nil {
		log.Info("Failed to load creds for user being deleted; might be left intact... this is fine?")
		return ctrl.Result{}, nil
	} else if creds == nil {
		log.Info("Failed to load creds; skipping.")
		return ctrl.Result{}, nil
	}

	user_exists, err := solrClient.CheckUserExistence(creds.Username)
	if err != nil {
		log.Error(err, "Failed to check for user existence.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed check for user existence: %w", err)
	}
	roles_assigned, err := solrClient.HasRoles(creds.Username)
	if err != nil {
		log.Error(err, "Failed to check roles.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed check for user roles: %w", err)
	}

	if ensure_exists_state {
		// Ensure the user exists (with the given password and groups).
		if !user_exists {
			// User doesn't exist; create.
			if err = solrClient.CreateUser(creds.Username, creds.Password); err != nil {
				log.Error(err, "Failed to create user.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to create user: %w", err)
			} else {
				log.Info("Created user.")
			}
		} else {
			// User exists; update password if appropriate.
			ok, err := solrClient.CheckUser(creds.Username, creds.Password)
			if err != nil {
				log.Error(err, "Failed to compare password.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to compare password: %w", err)
			}
			if !ok {
				if err = solrClient.UpdateUser(creds.Username, creds.Password); err != nil {
					log.Error(err, "Failed to update password.")
					return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update password: %w", err)
				} else {
					log.Info("Updated password.")
				}
			} else {
				log.Info("Password appears up-to-date; no need to create.")
			}
		}

		if user.Status.Username != creds.Username {
			user.Status.Username = creds.Username
			err = r.Status().Update(ctx, user)
			if err != nil {
				log.Error(err, "Failed to update user status.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update user status: %w", err)
			}
		} else {
			log.Info("No need to update status.")
		}

		if roles_assigned {
			log.Info("User has roles; no need to assign.")
		} else {
			err = solrClient.UpsertRoles(creds.Username)
			if err != nil {
				log.Error(err, "Failed to upsert roles.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to upsert roles: %w", err)
			} else {
				log.Info("Added roles.")
			}
		}
	} else {
		if roles_assigned {
			err = solrClient.DeleteRoles(creds.Username)
			if err != nil {
				log.Error(err, "Failed to delete roles for user.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to delete roles for user: %w", err)
			} else {
				log.Info("Deleted roles for user.")
			}
		} else {
			log.Info("User does not have roles; no need to remove.")
		}

		// Delete the user (if it exists).
		if !user_exists {
			log.Info("User does not appear to exist in Solr.")
		} else {
			if err = solrClient.DeleteUser(creds.Username); err != nil {
				log.Error(err, "Failed to delete user.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to delete user: %w", err)
			} else {
				log.Info("Deleted user.")
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *UserReconciler) getAdminPassword(ctx context.Context, user *v1alpha1.User) (string, error) {
	log := logf.FromContext(ctx)
	solrCloudRef := user.Spec.SolrCloudRef
	namespace := getNamespace(user, solrCloudRef.Ref)
	var adminSecret corev1.Secret
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-solrcloud-security-bootstrap", solrCloudRef.Ref.Name),
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
		For(&v1alpha1.User{}).
		Named("user").
		WatchesMetadata(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				log := logf.FromContext(ctx)
				var list v1alpha1.UserList
				requests := []reconcile.Request{}
				err := r.List(ctx, &list)
				if err != nil {
					log.Error(err, "Failed to enumerate users while watching secrets.")
					return requests
				}

				for _, element := range list.Items {
					ref := element.Spec.Secret.Ref
					if ref.Namespace == obj.GetNamespace() && ref.Name == obj.GetName() {
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
		WatchesMetadata(
			&v1beta1.SolrCloud{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				log := logf.FromContext(ctx)
				var list v1alpha1.UserList
				requests := []reconcile.Request{}
				err := r.List(ctx, &list)
				if err != nil {
					log.Error(err, "Failed to enumerate users while watching Solr Cloud instances.")
					return requests
				}

				for _, element := range list.Items {
					if element.Spec.SolrCloudRef.Ref.Namespace == obj.GetNamespace() && element.Spec.SolrCloudRef.Ref.Name == obj.GetName() {
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
		Complete(r)
}
