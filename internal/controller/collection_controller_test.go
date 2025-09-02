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

	"github.com/apache/solr-operator/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	solrv1alpha1 "github.com/discoverygarden/solr-user-operator/api/v1alpha1"
)

var _ = Describe("Collection Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		collection := &solrv1alpha1.Collection{}
		bootstrap_secret := &corev1.Secret{}
		solr_cloud := &v1beta1.SolrCloud{}

		BeforeEach(func() {
			By("creating resources to reference")
			bootstrap_secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "solr-solrcloud-security-bootstrap",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"admin": []byte("testasdfqwer"),
				},
			}
			Expect(k8sClient.Create(ctx, bootstrap_secret)).To(Succeed())
			solr_cloud = &v1beta1.SolrCloud{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "solr",
					Namespace: "default",
				},
				Spec: v1beta1.SolrCloudSpec{},
			}
			Expect(k8sClient.Create(ctx, solr_cloud)).To(Succeed())
		})
		BeforeEach(func() {
			By("creating the custom resource for the Kind Collection")
			err := k8sClient.Get(ctx, typeNamespacedName, collection)
			if err != nil && errors.IsNotFound(err) {
				resource := &solrv1alpha1.Collection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: solrv1alpha1.CollectionSpec{
						TargetSolr: solrv1alpha1.SolrCloudRef{
							ObjectRef: solrv1alpha1.ObjectRef{
								Name:      solr_cloud.Name,
								Namespace: solr_cloud.Namespace,
							},
						},
						Map: solrv1alpha1.ObjectRef{
							Name:      "test-map",
							Namespace: "default",
						},
						RemovalPolicy: "delete",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &solrv1alpha1.Collection{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Collection")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		AfterEach(func() {
			names := []client.Object{
				solr_cloud,
				bootstrap_secret,
			}

			for _, obj := range names {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Cleanup dependent resource: %s/%s", obj.GetNamespace(), obj.GetName()))
				Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
			}
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &CollectionReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
