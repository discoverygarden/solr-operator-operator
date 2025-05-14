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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CollectionSpec defines the desired state of Collection.
type CollectionSpec struct {
	// The target Solr Cloud instance
	TargetSolr SolrCloudRef `json:"solrCloud"`

	// Config map in which to stash values. Will be created if it does not exist.
	Map ObjectRef `json:"map"`

	// Removal policy. One of "delete" or "retain", to respectively delete or retain the underlying collection.
	RemovalPolicy string `json:"removalPolicy"`
}

// CollectionStatus defines the observed state of Collection.
type CollectionStatus struct {
	// Conditions.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Collection name to use.
	Name string `json:"name,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Collection is the Schema for the collections API.
type Collection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CollectionSpec   `json:"spec,omitempty"`
	Status CollectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CollectionList contains a list of Collection.
type CollectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Collection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Collection{}, &CollectionList{})
}
