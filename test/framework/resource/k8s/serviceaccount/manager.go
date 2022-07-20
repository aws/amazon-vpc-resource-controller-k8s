package serviceaccount

import (
	"context"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	// BuildRestConfigWithServiceAccount will build a new rest config with credentials from service account.
	BuildRestConfigWithServiceAccount(ctx context.Context, saKey types.NamespacedName) (*rest.Config, error)
}

type defaultManager struct {
	k8sClient client.Client
	restCfg   *rest.Config
}

func NewManager(k8sClient client.Client, restCfg *rest.Config) Manager {
	return &defaultManager{k8sClient: k8sClient, restCfg: restCfg}
}

func (m *defaultManager) BuildRestConfigWithServiceAccount(ctx context.Context, saKey types.NamespacedName) (*rest.Config, error) {
	sa := &corev1.ServiceAccount{}
	if err := m.k8sClient.Get(ctx, saKey, sa); err != nil {
		return nil, err
	}
	if len(sa.Secrets) == 0 {
		return nil, errors.Errorf("serviceAccount %s have no secrets", saKey.String())
	}
	saTokenSecretName := sa.Secrets[0].Name
	saTokenSecretKey := types.NamespacedName{Namespace: saKey.Namespace, Name: saTokenSecretName}
	saTokenSecret := &corev1.Secret{}
	if err := m.k8sClient.Get(ctx, saTokenSecretKey, saTokenSecret); err != nil {
		return nil, err
	}
	bearerToken := string(saTokenSecret.Data["token"])
	tlsClientConfig := rest.TLSClientConfig{CAData: saTokenSecret.Data["ca.crt"]}
	return &rest.Config{
		Host:            m.restCfg.Host,
		TLSClientConfig: tlsClientConfig,
		BearerToken:     bearerToken,
	}, nil
}
