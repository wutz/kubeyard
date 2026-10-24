package webhook

import (
	"context"
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PatchCABundle writes caPEM into the caBundle of every webhook of the
// ValidatingWebhookConfiguration named name. A missing object is not an error:
// when the manifests are applied after the operator starts, the bundle set there
// already points at this CA, and the next start patches it again.
func PatchCABundle(ctx context.Context, c client.Client, name string, caPEM []byte) error {
	var cfg admissionregistrationv1.ValidatingWebhookConfiguration
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &cfg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return err
	}
	changed := false
	for i := range cfg.Webhooks {
		if string(cfg.Webhooks[i].ClientConfig.CABundle) == string(caPEM) {
			continue
		}
		cfg.Webhooks[i].ClientConfig.CABundle = caPEM
		changed = true
	}
	if !changed {
		return nil
	}
	if err := c.Update(ctx, &cfg); err != nil {
		return fmt.Errorf("patch caBundle of %s: %w", name, err)
	}
	return nil
}
