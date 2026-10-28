package kubernetes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MissingReferenceError reports a Secret, ConfigMap or key that does not exist.
// It names the reference, never the referenced value.
type MissingReferenceError struct {
	Kind      string
	Namespace string
	Name      string
	// Key is empty when the object itself is missing.
	Key string
}

func (e *MissingReferenceError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s %s/%s not found", e.Kind, e.Namespace, e.Name)
	}
	return fmt.Sprintf("key %q not found in %s %s/%s", e.Key, e.Kind, e.Namespace, e.Name)
}

// GetSecretKey returns the value of a Secret key in namespace. An optional
// selector whose Secret or key is missing yields an empty string.
func GetSecretKey(ctx context.Context, r client.Reader, namespace string,
	sel *corev1.SecretKeySelector) (string, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: sel.Name}, secret)
	if value, ok := secret.Data[sel.Key]; err == nil && ok {
		return string(value), nil
	}
	return "", missingRef(err, "Secret", namespace, sel.Name, sel.Key, sel.Optional)
}

// GetConfigMapKey returns the value of a ConfigMap key in namespace, from Data
// or BinaryData. An optional selector whose ConfigMap or key is missing yields
// an empty string.
func GetConfigMapKey(ctx context.Context, r client.Reader, namespace string,
	sel *corev1.ConfigMapKeySelector) (string, error) {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: sel.Name}, cm)
	if err == nil {
		if value, ok := cm.Data[sel.Key]; ok {
			return value, nil
		}
		if value, ok := cm.BinaryData[sel.Key]; ok {
			return string(value), nil
		}
	}
	return "", missingRef(err, "ConfigMap", namespace, sel.Name, sel.Key, sel.Optional)
}

// missingRef turns a failed lookup into an error, or nil for an optional
// selector. err is the Get error; nil means the object exists but lacks key.
func missingRef(err error, kind, namespace, name, key string, optional *bool) error {
	if err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to get %s %s/%s: %w", kind, namespace, name, err)
	}
	if optional != nil && *optional {
		return nil
	}
	missing := &MissingReferenceError{Kind: kind, Namespace: namespace, Name: name}
	if err == nil {
		missing.Key = key
	}
	return missing
}
