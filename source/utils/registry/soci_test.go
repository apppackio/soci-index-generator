package registry

import (
	"encoding/json"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIsSociConvertedIndexTrueWhenChildCarriesSociArtifactType(t *testing.T) {
	raw := mustJSON(t, ocispec.Index{
		MediaType: MediaTypeOCIImageIndex,
		Manifests: []ocispec.Descriptor{
			{MediaType: MediaTypeOCIManifest, Digest: "sha256:aaaa"},
			{MediaType: MediaTypeOCIManifest, Digest: "sha256:bbbb", ArtifactType: SociIndexV2ArtifactType},
		},
	})
	got, err := IsSociConvertedIndex(MediaTypeOCIImageIndex, raw)
	if err != nil || !got {
		t.Fatalf("got (%v, %v), want (true, nil)", got, err)
	}
}

func TestIsSociConvertedIndexFalseForPlainMultiPlatformIndex(t *testing.T) {
	raw := mustJSON(t, ocispec.Index{
		MediaType: MediaTypeOCIImageIndex,
		Manifests: []ocispec.Descriptor{
			{MediaType: MediaTypeOCIManifest, Digest: "sha256:aaaa", Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"}},
			{MediaType: MediaTypeOCIManifest, Digest: "sha256:bbbb", Platform: &ocispec.Platform{OS: "linux", Architecture: "arm64"}},
		},
	})
	got, err := IsSociConvertedIndex(MediaTypeOCIImageIndex, raw)
	if err != nil || got {
		t.Fatalf("got (%v, %v), want (false, nil)", got, err)
	}
}

func TestIsSociConvertedIndexFalseForImageManifest(t *testing.T) {
	raw := mustJSON(t, ocispec.Manifest{MediaType: MediaTypeDockerManifest})
	got, err := IsSociConvertedIndex(MediaTypeDockerManifest, raw)
	if err != nil || got {
		t.Fatalf("got (%v, %v), want (false, nil)", got, err)
	}
}

func TestIsSociConvertedIndexErrorsOnMalformedJSON(t *testing.T) {
	got, err := IsSociConvertedIndex(MediaTypeOCIImageIndex, []byte("{not json"))
	if err == nil || got {
		t.Fatalf("got (%v, %v), want (false, error)", got, err)
	}
}
