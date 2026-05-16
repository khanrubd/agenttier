/*
Copyright 2024 AgentTier Authors.

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
	"encoding/base64"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// runtimeTokenBytes is the entropy size of the per-sandbox runtime token.
// 32 bytes = 256 bits of cryptographic random — well above any reasonable
// security ceiling. URL-safe base64 encoded for transit (no padding so the
// header value never needs URL-encoding).
const runtimeTokenBytes = 32

// runtimeTokenSecretName returns the deterministic Secret name we use for
// a sandbox's HTTP-exec bearer token. Keeping it deterministic (one per
// sandbox, named after the sandbox) means a controller restart finds the
// existing token instead of churning it.
func runtimeTokenSecretName(sandboxName string) string {
	return sandboxName + "-runtime-token"
}

// ensureRuntimeTokenSecret creates the per-sandbox token Secret if it
// doesn't already exist. Returns the Secret name on success — the caller
// (the reconciler) plumbs it into MergedPodConfig.RuntimeTokenSecret so
// the Pod spec mounts AGENTTIER_RUNTIME_TOKEN from this Secret.
//
// Idempotent: if the Secret is already present, the existing token is
// preserved and we just return its name. That's important — rotating
// tokens on every reconcile would force a Pod restart and break any
// in-flight Router→sandbox connection.
//
// Owned by the Sandbox via owner reference so deleting the Sandbox
// garbage-collects the Secret automatically.
func (r *SandboxReconciler) ensureRuntimeTokenSecret(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (string, error) {
	name := runtimeTokenSecretName(sandbox.Name)
	existing := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: sandbox.Namespace}, existing)
	if err == nil {
		// Already there; trust the existing token.
		return name, nil
	}
	if !errors.IsNotFound(err) {
		return "", fmt.Errorf("get runtime-token secret: %w", err)
	}

	tokenBytes := make([]byte, runtimeTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("read random for runtime token: %w", err)
	}
	// URL-safe base64 — the value flows through HTTP Authorization
	// headers; standard base64's `+`/`/` would need URL-encoding.
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				"agenttier.io/sandbox": sandbox.Name,
				"agenttier.io/managed": "true",
				// Distinct purpose label so admin tooling can find these
				// alongside other AgentTier-managed Secrets without false
				// positives from credential refs the user defined.
				"agenttier.io/secret-purpose": "runtime-token",
			},
		},
		Type: corev1.SecretTypeOpaque,
		// We write Data directly (not StringData) because the
		// controller-runtime fake client used in tests doesn't run the
		// apiserver-side StringData → Data conversion. Writing Data
		// makes the test path identical to the production path.
		Data: map[string][]byte{
			"token": []byte(token),
		},
	}
	if err := controllerutil.SetControllerReference(sandbox, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("set owner reference on runtime-token secret: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil {
		// Race condition: another reconcile loop committed first. Re-Get
		// to honor the existing token rather than overwriting it.
		if errors.IsAlreadyExists(err) {
			return name, nil
		}
		return "", fmt.Errorf("create runtime-token secret: %w", err)
	}
	return name, nil
}

// readRuntimeToken returns the plaintext token value for a sandbox's
// runtime-token Secret. Used by the Router-side proxy when forging the
// Bearer header; not needed by the controller's create path. Returns an
// empty string + nil error when the Secret is missing — callers should
// treat that as "fall back to SPDY exec" rather than as a hard error.
func ReadRuntimeToken(ctx context.Context, c client.Client, sandboxName, namespace string) (string, error) {
	s := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Name: runtimeTokenSecretName(sandboxName), Namespace: namespace}, s)
	if errors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(s.Data["token"]), nil
}
