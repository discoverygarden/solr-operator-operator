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
	"encoding/hex"
	"fmt"
	"net/url"
	"reflect"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/apache/solr-operator/api/v1beta1"
	solrv1alpha1 "github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	"github.com/discoverygarden/solr-user-operator/internal/controller/solr"
	// +kubebuilder:scaffold:imports
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	SolrClientAware
	Scheme *runtime.Scheme
}

const (
	FinaliserName                   string = "solr.dgicloud.com/finalizer"
	conditionSecretAvailable        string = "SecretAvailable"
	conditionSecretExists           string = "SecretExists"
	conditionSecretUsername         string = "SecretUsername"
	conditionSecretPassword         string = "SecretPassword"
	conditionSecretEndpoint         string = "SecretEndpoint"
	conditionSecretEndpointHostname string = "SecretEndpointHostname"
	conditionUserAvailable          string = "Available"
	conditionUserExists             string = "Exists"
	conditionUserHasRoles           string = "HasRoles"
	conditionUserPasswordUpToDate   string = "PasswordUpToDate"
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

	var user solrv1alpha1.User
	if err := r.Get(ctx, req.NamespacedName, &user); err != nil {
		log.Error(err, "Unable to get User")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	solrClient, err := r.getClient(ctx, user.ObjectMeta, &user.Spec.SolrCloudRef)
	if err != nil {
		log.Error(err, "Failed to invoke factory method to acquire Solr client implementation.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to invoke factory: %w", err)
	}

	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		// Not being deleted, make sure it has a finalizer
		if !controllerutil.ContainsFinalizer(&user, FinaliserName) {
			controllerutil.AddFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to add finalizer: %w", err)
			}

			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionUserAvailable,
				Status:  v1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Reconciling...",
			})
			for _, value := range []string{conditionUserExists, conditionUserHasRoles, conditionUserPasswordUpToDate} {
				meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
					Type:    value,
					Status:  v1.ConditionUnknown,
					Reason:  "Reconciling",
					Message: "Reconciling...",
				})
			}
			if err := r.Status().Update(ctx, &user); err != nil {
				log.Error(err, "Failed to update conditions.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update conditons: %w", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&user, FinaliserName) {
			// Delete the user.
			if err := r.reconcileUser(ctx, solrClient, &user, false); err != nil {
				log.Error(err, "Failed reconciling (non-)existence of user.")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed reconciling non-existence of %s/%s: %w", user.Namespace, user.Name, err)
			}

			controllerutil.RemoveFinalizer(&user, FinaliserName)
			if err := r.Update(ctx, &user); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{Requeue: true}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.reconcileUser(ctx, solrClient, &user, true); err != nil {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserAvailable,
			Status:  v1.ConditionFalse,
			Reason:  "ReconciliationFailed",
			Message: fmt.Sprintf("Failed to reconcile user. Error: %s", err),
		})
		if err := r.Status().Update(ctx, &user); err != nil {
			log.Error(err, "Failed setting status on user regarding failed reconciliation.")
			return ctrl.Result{Requeue: true}, fmt.Errorf("failed setting status on user: %w", err)
		}
		log.Error(err, "Failed reconciling existence of user.")
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed reconciling existence of %s/%s: %w", user.Namespace, user.Name, err)
	} else {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserAvailable,
			Status:  v1.ConditionTrue,
			Reason:  "ReconcileSuccess",
			Message: "Successfully reconciled; ready to go!",
		})
		if err := r.Status().Update(ctx, &user); err != nil {
			log.Error(err, "Failed to update status regarding reconciliation status.")
			return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update status regarding reconciliation status: %w", err)
		}
		return ctrl.Result{}, nil
	}
}

func getNamespace(u v1.ObjectMeta, ref solrv1alpha1.ObjectRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	} else {
		return u.Namespace
	}
}

func (r *UserReconciler) getSecret(ctx context.Context, user *solrv1alpha1.User, try_create bool) (*corev1.Secret, error) {
	log := logf.FromContext(ctx)
	ref := user.Spec.Secret.ToObjectKey()
	ref.Namespace = getNamespace(user.ObjectMeta, user.Spec.Secret.ObjectRef)

	var secret corev1.Secret
	if err := r.Get(ctx, ref, &secret); err != nil {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionSecretExists,
			Status:  v1.ConditionUnknown,
			Reason:  "FailedLoad",
			Message: fmt.Sprintf("Failed to load secret: %v", err),
		})

		if try_create {
			secret = corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      ref.Name,
					Namespace: ref.Namespace,
				},
			}
			if err := controllerutil.SetOwnerReference(user, &secret, r.Scheme); err != nil {
				log.Error(err, "Failed to set owner of created secret.")
			}

			if err := r.Create(ctx, &secret); err != nil {
				meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
					Type:    conditionSecretExists,
					Status:  v1.ConditionUnknown,
					Reason:  "FailedCreation",
					Message: fmt.Sprintf("Failed to create secret: %v", err),
				})
				return nil, fmt.Errorf("failed to create secret: %w", err)
			}
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionSecretExists,
				Status:  v1.ConditionTrue,
				Reason:  "Created",
				Message: "Created secret.",
			})
			for _, value := range []string{conditionSecretUsername, conditionSecretPassword, conditionSecretEndpoint, conditionSecretEndpointHostname} {
				meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
					Type:    value,
					Status:  v1.ConditionFalse,
					Reason:  "Created",
					Message: "Empty secret created.",
				})
			}

			log.Info("Created secret",
				"namespace", ref.Namespace,
				"name", ref.Name,
			)
			return &secret, nil
		}
		return nil, fmt.Errorf("failed to load secret for %v, %s: %w", user, ref, err)
	}
	meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
		Type:    conditionSecretExists,
		Status:  v1.ConditionTrue,
		Reason:  "Exists",
		Message: "Secret exists.",
	})
	return &secret, nil
}

type Credentials struct {
	Username string `spec_field_name:"UsernameKey" condition:"SecretUsername"`
	Password string `spec_field_name:"PasswordKey" condition:"SecretPassword"`
	Endpoint string `spec_field_name:"EndpointKey" condition:"SecretEndpoint"`
	Hostname string `spec_field_name:"EndpointHostnameKey" condition:"SecretEndpointHostname"`
}

func (c *Credentials) getSecretKey(cred_field_name string, user *solrv1alpha1.User) (*string, error) {
	ref_type := reflect.TypeOf(user.Spec.Secret)
	cred_type := reflect.TypeOf(*c)
	field, ok := cred_type.FieldByName(cred_field_name)
	if !ok {
		return nil, fmt.Errorf("failed to find field from cred")
	}
	field_name := field.Tag.Get("spec_field_name")
	ref_field, present := ref_type.FieldByName(field_name)
	if !present {
		return nil, fmt.Errorf("failed to find field from secret")
	}
	secret_key := reflect.ValueOf(&user.Spec.Secret).Elem().FieldByIndex(ref_field.Index).String()
	if secret_key == "" {
		secret_key = ref_field.Tag.Get("default_value")
	}
	return &secret_key, nil
}

func (c *Credentials) extractFromSecret(ctx context.Context, user *solrv1alpha1.User, secret *corev1.Secret) {
	log := logf.FromContext(ctx)
	cred_type := reflect.TypeOf(*c)
	for index, value := range reflect.VisibleFields(cred_type) {
		secret_key, err := c.getSecretKey(value.Name, user)
		if err != nil {
			log.Error(err, "Failed to get secret key",
				"namespace", secret.Namespace,
				"name", secret.Name,
				"field", value.Name,
			)
			continue
		}
		secret_value, present := secret.Data[*secret_key]
		if !present {
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    value.Tag.Get("condition"),
				Status:  v1.ConditionFalse,
				Reason:  "NotInSecret",
				Message: "Key/value not in secret.",
			})
			log.Info("Key not in secret",
				"namespace", secret.Namespace,
				"name", secret.Name,
				"field", value.Name,
				"key", secret_key,
			)
			continue
		}
		reflect.ValueOf(c).Elem().Field(index).SetString(string(secret_value))
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    value.Tag.Get("condition"),
			Status:  v1.ConditionTrue,
			Reason:  "InSecret",
			Message: "Key/value found in secret.",
		})
		log.Info("Key in secret",
			"namespace", secret.Namespace,
			"name", secret.Name,
			"field", value.Name,
			"key", secret_key,
		)
	}
}

func (r *UserReconciler) getCredentialsFromSecret(ctx context.Context, user *solrv1alpha1.User, secret *corev1.Secret) *Credentials {
	log := logf.FromContext(ctx)
	var creds Credentials

	if secret != nil {
		// Get info from secret.
		creds.extractFromSecret(ctx, user, secret)
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
		}
	}

	return &creds
}

func (r *UserReconciler) reconcileUser(ctx context.Context, solrClient solr.ClientInterface, user *solrv1alpha1.User, ensure_exists_state bool) error {
	log := logf.FromContext(ctx)

	secret, err := r.getSecret(ctx, user, ensure_exists_state)
	if err != nil {
		if ensure_exists_state {
			log.Error(err, "Failed to load secret.")
			return fmt.Errorf("failed to load secret: %w", err)
		} else {
			log.Error(err, "Failed to load secret for user being deleted; probably fine?")
		}
	}

	creds := r.getCredentialsFromSecret(ctx, user, secret)
	if !ensure_exists_state && creds == nil {
		log.Info("Failed to load creds for user being deleted; might be left intact... this is fine?")
		return nil
	} else if creds == nil {
		// Skip reconciliation. Should be done if/when the secret becomes available.
		log.Info("Failed to load creds; skipping.")
		return nil
	}

	secret_dirty, err := r.reconcileSecret(ctx, user, creds, secret, solrClient)
	if err != nil {
		log.Error(err, "Failed to reconcile secret.")
		return fmt.Errorf("failed to reconcile secret: %w", err)
	}

	if err := r.reconcileObserveUser(ctx, user, creds, solrClient); err != nil {
		log.Error(err, "Failed to observe user.")
		return fmt.Errorf("failed to observe user: %w", err)
	}

	if ensure_exists_state {
		// Ensure the user exists (with the given password and groups).
		if !meta.IsStatusConditionTrue(user.Status.Conditions, conditionUserExists) {
			// User doesn't exist; create.
			if err := solrClient.CreateUser(creds.Username, creds.Password); err != nil {
				log.Error(err, "Failed to create user.")
				return fmt.Errorf("failed to create user: %w", err)
			}

			log.Info("Created user.")
			for _, value := range []string{conditionUserExists, conditionUserPasswordUpToDate} {
				meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
					Type:    value,
					Status:  v1.ConditionTrue,
					Reason:  "CreateUser",
					Message: "Created user.",
				})
			}
		} else {
			// User exists; update password if appropriate.
			if !meta.IsStatusConditionTrue(user.Status.Conditions, conditionUserPasswordUpToDate) {
				if err := solrClient.UpdateUser(creds.Username, creds.Password); err != nil {
					log.Error(err, "Failed to update password.")
					meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
						Type:    conditionUserPasswordUpToDate,
						Status:  v1.ConditionUnknown,
						Reason:  "FailedPasswordCheck",
						Message: fmt.Sprintf("Failed to check if password is up-to-date: %s", err),
					})
					return fmt.Errorf("failed to update password: %w", err)
				}
				log.Info("Updated password.")
				meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
					Type:    conditionUserPasswordUpToDate,
					Status:  v1.ConditionTrue,
					Reason:  "UpdatedPassword",
					Message: "Update Solr with password.",
				})
			}
		}

		if user.Status.Username != creds.Username {
			user.Status.Username = creds.Username
			log.Info("Updating username in status.")
		} else {
			log.Info("No need to update username in status.")
		}

		if meta.IsStatusConditionTrue(user.Status.Conditions, conditionUserHasRoles) {
			// Status should have been update above.
			log.Info("User has roles; no need to assign.")
		} else {
			if err := solrClient.UpsertRoles(creds.Username); err != nil {
				log.Error(err, "Failed to upsert roles.")
				return fmt.Errorf("failed to upsert roles: %w", err)
			}
			log.Info("Added roles.")
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionUserHasRoles,
				Status:  v1.ConditionTrue,
				Reason:  "AddededRoles",
				Message: "Added roles.",
			})
		}
		if secret_dirty {
			if meta.IsStatusConditionTrue(user.Status.Conditions, conditionSecretAvailable) {
				if err := r.Update(ctx, secret); err != nil {
					log.Error(err, "Failed to update secret.")
				} else {
					log.Info("Updated secret.")
					// secret_dirty = false
				}
			} else {
				log.Info("Secret dirty but not available to be updated?")
				return fmt.Errorf("secret dirty but not available to be updated")
			}
		} else {
			log.Info("Secret clean; no need to update.")
		}
	} else {
		// Expecting subresources/status to go away along with the resource
		// proper, so let's not bother updating the resource status.
		if meta.IsStatusConditionTrue(user.Status.Conditions, conditionUserHasRoles) {
			if err := solrClient.DeleteRoles(creds.Username); err != nil {
				log.Error(err, "Failed to delete roles for user.")
				return fmt.Errorf("failed to delete roles for user: %w", err)
			}
			log.Info("Deleted roles for user.")
		} else {
			log.Info("User does not have roles; no need to remove.")
		}

		// Delete the user (if it exists).
		if !meta.IsStatusConditionTrue(user.Status.Conditions, conditionUserExists) {
			log.Info("User does not appear to exist in Solr.")
		} else {
			if err := solrClient.DeleteUser(creds.Username); err != nil {
				log.Error(err, "Failed to delete user.")
				return fmt.Errorf("failed to delete user: %w", err)
			} else {
				log.Info("Deleted user.")
			}
		}
	}
	return nil
}

func (r *UserReconciler) reconcileSecret(ctx context.Context, user *solrv1alpha1.User, creds *Credentials, secret *corev1.Secret, solrClient solr.ClientInterface) (bool, error) {
	log := logf.FromContext(ctx)

	secret_conditions := make([]v1.Condition, 0)
	secret_dirty := false

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}

	if !meta.IsStatusConditionTrue(user.Status.Conditions, conditionSecretUsername) {
		// Mint unique username: {user.namespace}-{user.name}-{random suffix} or something of the like?
		suffix := make([]byte, 4)
		if _, err := rand.Read(suffix); err != nil {
			log.Error(err, "Failed to generate random suffix for username.")
			return false, fmt.Errorf("failed to generate random suffix for username: %w", err)
		}
		username := fmt.Sprintf(
			"%s--%s--%x",
			user.Namespace,
			user.Name,
			suffix,
		)
		// Save username on secret (to configured key).
		key, err := creds.getSecretKey("Username", user)
		if err != nil {
			log.Error(err, "Failed to get secret key for username.")
			return false, fmt.Errorf("failed to get secret key for username: %w", err)
		}
		creds.Username = username
		secret.Data[*key] = []byte(username)
		secret_dirty = true
		secret_conditions = append(secret_conditions, v1.Condition{
			Type:    conditionSecretUsername,
			Status:  v1.ConditionTrue,
			Reason:  "GeneratedUsername",
			Message: fmt.Sprintf("Generated username %s.", username),
		})
	}

	if !meta.IsStatusConditionTrue(user.Status.Conditions, conditionSecretPassword) {
		// Generate password.
		password_bytes := make([]byte, 16)
		if _, err := rand.Read(password_bytes); err != nil {
			log.Error(err, "Failed to generate bytes for password.")
			return false, fmt.Errorf("failed to generate bytes for password: %w", err)
		}
		password := hex.EncodeToString(password_bytes)
		// Save password on secret (to configured key).
		key, err := creds.getSecretKey("Password", user)
		if err != nil {
			log.Error(err, "Failed to get secret key for password.")
			return false, fmt.Errorf("failed to get secret key for password: %w", err)
		}
		creds.Password = password
		secret.Data[*key] = []byte(password)
		secret_dirty = true
		secret_conditions = append(secret_conditions, v1.Condition{
			Type:    conditionSecretPassword,
			Status:  v1.ConditionTrue,
			Reason:  "GeneratedPassword",
			Message: "Generated password.",
		})
	}

	// TODO: Reconcile endpoint info on secret.
	{
		endpoint := v1beta1.InternalURLForCloud(solrClient.GetSolrCloud())
		key, err := creds.getSecretKey("Endpoint", user)
		if err != nil {
			log.Error(err, "Failed to get secret key for endpoint.")
			return false, fmt.Errorf("failed to get secret key for endpoint: %w", err)
		}
		creds.Endpoint = endpoint
		if !slices.Equal(secret.Data[*key], []byte(endpoint)) {
			secret.Data[*key] = []byte(endpoint)
			secret_dirty = true
			secret_conditions = append(secret_conditions, v1.Condition{
				Type:    conditionSecretEndpoint,
				Status:  v1.ConditionTrue,
				Reason:  "SetEndpoint",
				Message: "Set endpoint.",
			})
		} else {
			secret_conditions = append(secret_conditions, v1.Condition{
				Type:    conditionSecretEndpoint,
				Status:  v1.ConditionTrue,
				Reason:  "EndpointAlreadySet",
				Message: "Endpoint already set.",
			})
		}

		key, err = creds.getSecretKey("Hostname", user)
		if err != nil {
			log.Error(err, "Failed to get secret key for endpoint.")
			return false, fmt.Errorf("failed to get secret key for endpoint: %w", err)
		}
		parsed_url, err := url.Parse(endpoint)
		if err != nil {
			log.Error(err, "Failed to parse endpoint URL to get hostname.")
			return false, fmt.Errorf("failed to parse endpoint URL to get hostname: %w", err)
		}
		creds.Hostname = parsed_url.Host
		if !slices.Equal(secret.Data[*key], []byte(parsed_url.Hostname())) {
			secret.Data[*key] = []byte(parsed_url.Hostname())
			secret_dirty = true
			secret_conditions = append(secret_conditions, v1.Condition{
				Type:    conditionSecretEndpointHostname,
				Status:  v1.ConditionTrue,
				Reason:  "SetHostname",
				Message: "Set hostname.",
			})
		} else {
			secret_conditions = append(secret_conditions, v1.Condition{
				Type:    conditionSecretEndpointHostname,
				Status:  v1.ConditionTrue,
				Reason:  "HostnameAlreadySet",
				Message: "Hostname already set.",
			})
		}
	}

	if len(secret_conditions) > 0 {
		for _, condition := range secret_conditions {
			meta.SetStatusCondition(&user.Status.Conditions, condition)
		}

		conditions := []string{conditionSecretExists, conditionSecretUsername, conditionSecretPassword, conditionSecretEndpoint, conditionSecretEndpointHostname}
		statuses := make([]bool, len(conditions))
		for i, value := range conditions {
			statuses[i] = meta.IsStatusConditionTrue(user.Status.Conditions, value)
		}
		if !slices.Contains(statuses, false) {
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionSecretAvailable,
				Status:  v1.ConditionTrue,
				Reason:  "Ready",
				Message: "Ready; expected keys exist.",
			})
		} else {
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionSecretAvailable,
				Status:  v1.ConditionFalse,
				Reason:  "NotReady",
				Message: "Not ready; missing some keys in secret.",
			})
		}
	}
	return secret_dirty, nil
}

func (r *UserReconciler) reconcileObserveUser(ctx context.Context, user *solrv1alpha1.User, creds *Credentials, solrClient solr.ClientInterface) error {
	log := logf.FromContext(ctx)

	user_exists, err := solrClient.CheckUserExistence(creds.Username)
	if err != nil {
		log.Error(err, "Failed to check for user existence.")
		return fmt.Errorf("failed check for user existence: %w", err)
	} else if user_exists {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserExists,
			Status:  v1.ConditionTrue,
			Reason:  "UserInConfig",
			Message: "User appears to exist in Solr config.",
		})

		ok, err := solrClient.CheckUser(creds.Username, creds.Password)
		if err != nil {
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionUserPasswordUpToDate,
				Status:  v1.ConditionUnknown,
				Reason:  "Error",
				Message: fmt.Sprintf("Failed to check if password is up-to-date. Error: %s", err),
			})
			log.Error(err, "Failed to compare password.")
			return fmt.Errorf("failed to compare password: %w", err)
		}
		if !ok {
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionUserPasswordUpToDate,
				Status:  v1.ConditionFalse,
				Reason:  "PasswordDifferent",
				Message: "Password in Solr config appears to have diverged.",
			})
		} else {
			log.Info("Password appears up-to-date; no need to update.")
			meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
				Type:    conditionUserPasswordUpToDate,
				Status:  v1.ConditionTrue,
				Reason:  "PasswordUpToDate",
				Message: "Solr appears up-to-date with password from spec'd secret.",
			})
		}
	} else {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserExists,
			Status:  v1.ConditionFalse,
			Reason:  "UserNotInConfig",
			Message: "User does not appear to exist in Solr config.",
		})
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserPasswordUpToDate,
			Status:  v1.ConditionFalse,
			Reason:  "UserNotInConfig",
			Message: "User does not appear to be in config, so cannot have an up-to-date password.",
		})
	}

	roles_assigned, err := solrClient.HasRoles(creds.Username)
	if err != nil {
		log.Error(err, "Failed to check roles.")
		return fmt.Errorf("failed check for user roles: %w", err)
	} else if roles_assigned {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserHasRoles,
			Status:  v1.ConditionTrue,
			Reason:  "UserHasRoles",
			Message: "User appears to have the expected roles in Solr config.",
		})
	} else {
		meta.SetStatusCondition(&user.Status.Conditions, v1.Condition{
			Type:    conditionUserHasRoles,
			Status:  v1.ConditionFalse,
			Reason:  "UserMissingRoles",
			Message: "User does not appear to have the expected roles in Solr config.",
		})
	}

	return nil
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
				secret := obj.(*v1.PartialObjectMetadata)

				var list solrv1alpha1.UserList
				requests := []reconcile.Request{}
				err := r.List(ctx, &list)
				if err != nil {
					log.Error(err, "Failed to enumerate users while watching secrets.")
					return requests
				}

				for _, element := range list.Items {
					ref := element.Spec.Secret.ObjectRef
					if ref.Namespace == secret.GetNamespace() && ref.Name == secret.GetName() {
						requests = append(requests, reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&element),
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
				solr_cloud := obj.(*v1.PartialObjectMetadata)

				var list solrv1alpha1.UserList
				requests := []reconcile.Request{}
				err := r.List(ctx, &list)
				if err != nil {
					log.Error(err, "Failed to enumerate users while watching Solr Cloud instances.")
					return requests
				}

				for _, element := range list.Items {
					ref := element.Spec.SolrCloudRef.ObjectRef
					if ref.Namespace == solr_cloud.GetNamespace() && ref.Name == solr_cloud.GetName() {
						requests = append(requests, reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&element),
						})
					}
				}

				return requests
			}),
		).
		Complete(r)
}
