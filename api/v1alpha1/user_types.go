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

package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// UserSpec defines the desired state of User.
type UserSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	SolrCloudRef SolrCloudRef `json:"solrCloud"`
	Secret       SecretRef    `json:"secret"`
}

type ObjectRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

func (r *ObjectRef) String() string {
	return fmt.Sprintf("%s/%s", r.Namespace, r.Name)
}

func (r *ObjectRef) ToObjectKey() client.ObjectKey {
	return client.ObjectKey{
		Namespace: r.Namespace,
		Name:      r.Name,
	}
}

// Reference to the Solr Cloud instance in which to manage a user.
type SolrCloudRef struct {
	ObjectRef `json:",inline"`
}

// Reference to a secret containing the username and password for the user.
type SecretRef struct {
	ObjectRef `json:",inline"`
	// Key in secret containing the username. Defaults to "username" for compatibility with kubernetes.io/basic-auth secrets.
	UsernameKey string `json:"username-key,omitempty" default_value:"username"`
	// Key in secret containing the password. Defaults to "password" for compatibility with kubernetes.io/basic-auth secrets.
	PasswordKey string `json:"password-key,omitempty" default_value:"password"`
	// Key in secret indicating the Solr endpoint. Defaults to "endpoint".
	EndpointKey string `json:"endpoint-key,omitempty" default_value:"endpoint"`
}

// UserStatus defines the observed state of User.
type UserStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	Username   string             `json:"username"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// User is the Schema for the users API.
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec,omitempty"`
	Status UserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// UserList contains a list of User.
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
