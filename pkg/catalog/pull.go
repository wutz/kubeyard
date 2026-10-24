package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Pull fetches a catalog artifact from an OCI registry and returns its files.
// The reference must be pinned by digest. The registry then serves exactly
// the content that was signed, and a mismatch is an error. Callers pass the
// digest Flux verified, so the signature check still covers what is read.
func Pull(ctx context.Context, ref string) (fs.FS, error) {
	parsed, err := name.ParseReference(strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		return nil, fmt.Errorf("catalog reference: %w", err)
	}
	digest, ok := parsed.(name.Digest)
	if !ok {
		return nil, errors.New("catalog reference must be pinned by digest")
	}
	img, err := remote.Image(digest,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return nil, fmt.Errorf("pull catalog: %w", err)
	}
	got, err := img.Digest()
	if err != nil {
		return nil, err
	}
	if got.String() != digest.DigestStr() {
		return nil, fmt.Errorf("catalog digest %s, want %s", got, digest.DigestStr())
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}
	if len(layers) != 1 {
		return nil, fmt.Errorf("catalog artifact has %d layers, want 1", len(layers))
	}
	rc, err := layers[0].Compressed()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return ReadArtifact(rc)
}
