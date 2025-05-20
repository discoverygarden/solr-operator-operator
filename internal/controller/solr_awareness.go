package controller

import (
	"context"
	"fmt"

	"github.com/apache/solr-operator/api/v1beta1"
	"github.com/discoverygarden/solr-user-operator/api/v1alpha1"
	"github.com/discoverygarden/solr-user-operator/internal/controller/solr"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type SolrClientAware struct {
	client.Client
	solrClientFactory func(ctx context.Context, base v1.ObjectMeta, ref *v1alpha1.SolrCloudRef) (solr.ClientInterface, error)
}

func (r *SolrClientAware) getNamespace(u v1.ObjectMeta, ref v1alpha1.ObjectRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	} else {
		return u.Namespace
	}
}

func (r *SolrClientAware) getClient(ctx context.Context, base v1.ObjectMeta, ref *v1alpha1.SolrCloudRef) (solr.ClientInterface, error) {
	if r.solrClientFactory != nil {
		return r.solrClientFactory(ctx, base, ref)
	}
	return r.getDefaultClient(ctx, base, ref)
}

func (r *SolrClientAware) getDefaultClient(ctx context.Context, base v1.ObjectMeta, ref *v1alpha1.SolrCloudRef) (solr.ClientInterface, error) {
	solr_cloud, err := r.getSolrCloud(ctx, base, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire Solr Cloud instance: %w", err)
	}

	adminPassword, err := r.getAdminPassword(ctx, solr_cloud)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire admin password: %w", err)
	}

	return &solr.Client{
		Context:   ctx,
		User:      "admin",
		Password:  adminPassword,
		SolrCloud: solr_cloud,
	}, nil
}

func (r *SolrClientAware) getSolrCloud(ctx context.Context, base v1.ObjectMeta, solrCloud *v1alpha1.SolrCloudRef) (*v1beta1.SolrCloud, error) {
	ref := solrCloud.ToObjectKey()
	ref.Namespace = r.getNamespace(base, solrCloud.ObjectRef)

	var solr_cloud v1beta1.SolrCloud
	if err := r.Get(ctx, ref, &solr_cloud); err != nil {
		return nil, fmt.Errorf("failed to acquire Solr Cloud reference: %w", err)
	}
	return &solr_cloud, nil
}

func (r *SolrClientAware) getAdminPassword(ctx context.Context, solr_cloud *v1beta1.SolrCloud) (string, error) {
	log := logf.FromContext(ctx)

	var adminSecret corev1.Secret

	if err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: solr_cloud.Namespace,
			Name:      solr_cloud.SecurityBootstrapSecretName(),
		},
		&adminSecret,
	); err != nil {
		log.Error(err, "Unable to fetch admin secret.")
		return "", fmt.Errorf("failed to fetch admin secret: %w", err)
	}
	adminPasswordBytes, ok := adminSecret.Data["admin"]
	if !ok {
		log.Info("Failed to get admin password from secret.")
		return "", fmt.Errorf("failed to get admin password from secret; not under the expected 'admin' key?")
	}
	adminPassword := string(adminPasswordBytes)
	return adminPassword, nil
}
