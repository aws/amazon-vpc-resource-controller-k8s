package serviceaccount

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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

var (
	tokenTimeOut = time.Second * 10
	awsNodeToken = types.NamespacedName{
		Name:      "aws-node-token",
		Namespace: "kube-system",
	}
	saUIDKey  = "kubernetes.io/service-account.uid"
	saNameKey = "kubernetes.io/service-account.name"
)

func NewManager(k8sClient client.Client, restCfg *rest.Config) Manager {
	return &defaultManager{k8sClient: k8sClient, restCfg: restCfg}
}

func (m *defaultManager) createServiceAccountToken(ctx context.Context, saKey types.NamespacedName, tokenKey types.NamespacedName) error {
	token := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      tokenKey.Name,
			Namespace: tokenKey.Namespace,
			Annotations: map[string]string{
				saNameKey: saKey.Name,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	if err := m.k8sClient.Create(ctx, token); err != nil {
		return errors.Errorf("Creating service account aws-node's token failed, Error %s", err.Error())
	}

	err := wait.Poll(utils.PollIntervalShort, tokenTimeOut, func() (done bool, err error) {
		sa := &corev1.ServiceAccount{}
		err = m.k8sClient.Get(ctx, saKey, sa)
		if err != nil {
			return true, err
		}
		return m.hasTokenAssociatedToSA(ctx, sa, tokenKey), nil
	})
	return err
}

func (m *defaultManager) hasTokenAssociatedToSA(ctx context.Context, sa *corev1.ServiceAccount, tokenKey types.NamespacedName) bool {
	return len(sa.Secrets) > 0 || m.isTokenCreatedForSA(ctx, sa, tokenKey)
}

func (m *defaultManager) isTokenCreatedForSA(ctx context.Context, sa *corev1.ServiceAccount, tokenKey types.NamespacedName) bool {
	token := &corev1.Secret{}
	err := m.k8sClient.Get(ctx, tokenKey, token)
	result := err == nil && token.Annotations[saUIDKey] == string(sa.UID)
	return result
}

func (m *defaultManager) BuildRestConfigWithServiceAccount(ctx context.Context, saKey types.NamespacedName) (*rest.Config, error) {
	sa := &corev1.ServiceAccount{}
	if err := m.k8sClient.Get(ctx, saKey, sa); err != nil {
		return nil, err
	}

	if !m.hasTokenAssociatedToSA(ctx, sa, awsNodeToken) {
		if err := m.createServiceAccountToken(ctx, saKey, awsNodeToken); err != nil {
			return nil, errors.Errorf("serviceAccount %s have no secrets with errors %s", saKey.String(), err.Error())
		}

	}

	var saTokenSecretKey types.NamespacedName
	if len(sa.Secrets) > 0 {
		// < eks 1.24
		saTokenSecretName := sa.Secrets[0].Name
		saTokenSecretKey = types.NamespacedName{Namespace: saKey.Namespace, Name: saTokenSecretName}

	} else {
		// >= eks 1.24
		saTokenSecretKey = awsNodeToken
	}
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
