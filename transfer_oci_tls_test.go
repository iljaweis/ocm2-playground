package playground_test

import (
	"bytes"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"ocm.software/open-component-model/bindings/go/blob/inmemory"
	"ocm.software/open-component-model/bindings/go/credentials"
	descriptorv2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	"ocm.software/open-component-model/bindings/go/oci"
	"ocm.software/open-component-model/bindings/go/oci/repository/provider"
	"ocm.software/open-component-model/bindings/go/oci/repository/resource"
	identityv1 "ocm.software/open-component-model/bindings/go/oci/spec/identity/v1"
	ocirepositoryspecv1 "ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/oci"
	"ocm.software/open-component-model/bindings/go/runtime"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	urlresolver "ocm.software/open-component-model/bindings/go/oci/resolver/url"
	"ocm.software/open-component-model/bindings/go/transfer"
	transferv1alpha1 "ocm.software/open-component-model/bindings/go/transfer/v1alpha1/spec"
)

func TestCreateAndTransferSimpleComponentWithTLSAndAuth(t *testing.T) {
	a, r := assert.New(t), require.New(t)

	ctx := t.Context()

	srcUser, srcPass := "srcuser", "srcpass"
	srcRegistry, _ := NewRegistry(t, &srcUser, &srcPass, true)

	srcResolver, err := urlresolver.New(
		urlresolver.WithBaseURL(srcRegistry),
		urlresolver.WithBaseClient(&auth.Client{
			Client:     retry.DefaultClient,
			Cache:      auth.NewCache(),
			Credential: auth.StaticCredential(srcRegistry, auth.Credential{Username: srcUser, Password: srcPass}),
		}),
	)
	r.NoError(err)

	srcRepo, err := oci.NewRepository(
		oci.WithResolver(srcResolver),
		oci.WithTempDir(t.TempDir()),
	)
	r.NoError(err)

	// Difference between `oci.Repository` and `ocirepositoryspecv1.Repository` is the former
	// contains the infrastructure to actually access the repository, while the latter is just a
	// specification of the repository's location.
	srcSpec := &ocirepositoryspecv1.Repository{
		Type:    runtime.NewVersionedType(ocirepositoryspecv1.Type, ocirepositoryspecv1.Version),
		BaseUrl: srcRegistry,
	}

	srcIdentity, err := identityv1.IdentityFromOCIRepository(srcSpec)
	r.NoError(err)

	// --

	targetUser, targetPass := "targetuser", "targetpass"
	targetRegistry, _ := NewRegistry(t, &targetUser, &targetPass, true)
	/*
		dstResolver, err := urlresolver.New(
			urlresolver.WithBaseURL(dstRegistry),
			urlresolver.WithPlainHTTP(true),
			urlresolver.WithBaseClient(&auth.Client{
				Client: retry.DefaultClient,
				Cache:  auth.NewCache(),
			}),
		)
		r.NoError(err)

		dstRepo, err := oci.NewRepository(
			oci.WithResolver(dstResolver),
			oci.WithTempDir(t.TempDir()),
			//oci.WithGlobalAccessPolicy(oci.GlobalAccessPolicyAuto),
			//oci.WithScheme(repositoryScheme()),
		)
		r.NoError(err)
	*/

	targetSpec := &ocirepositoryspecv1.Repository{
		Type:    runtime.NewVersionedType(ocirepositoryspecv1.Type, ocirepositoryspecv1.Version),
		BaseUrl: targetRegistry,
	}

	targetIdentity, err := identityv1.IdentityFromOCIRepository(targetSpec)
	r.NoError(err)

	// ---

	component := "opendefense.cloud/apps/my-app"
	version := "1.0.0"

	content := "hello world"

	resdesc := &descriptor.Resource{
		Relation: descriptor.LocalRelation,
		ElementMeta: descriptor.ElementMeta{
			ObjectMeta: descriptor.ObjectMeta{Name: "my-resource", Version: "0.0.1"},
		},
		Type: "plainText",
		Access: &descriptorv2.LocalBlob{
			LocalReference: digest.FromBytes([]byte(content)).String(),
			MediaType:      "text/plain",
		},
	}

	b := inmemory.New(bytes.NewReader([]byte(content)))
	res, err := srcRepo.AddLocalResource(ctx, component, version, resdesc, b)
	r.NoError(err)

	desc := &descriptor.Descriptor{
		Meta: descriptor.Meta{Version: "v2"},
		Component: descriptor.Component{
			Provider: descriptor.Provider{Name: "opendefense.cloud"},
			ComponentMeta: descriptor.ComponentMeta{
				ObjectMeta: descriptor.ObjectMeta{
					Name:    component,
					Version: version,
				},
			},
			Resources: []descriptor.Resource{*res},
		},
	}

	r.NoError(srcRepo.AddComponentVersion(ctx, desc))

	// --

	versions, err := srcRepo.ListComponentVersions(ctx, component)
	r.NoError(err)
	a.Len(versions, 1)
	a.Equal(version, versions[0])

	cv, err := srcRepo.GetComponentVersion(ctx, component, version)
	r.NoError(err)

	a.Equal(component, cv.Component.Name)
	a.Equal(version, cv.Component.Version)

	// --

	tgd, err := transfer.BuildGraphDefinition(ctx,
		&transferv1alpha1.Config{
			CopyMode:   transferv1alpha1.CopyModeAllResources,
			UploadType: transferv1alpha1.UploadAsOciArtifact,
		},
		transfer.Mapping{
			Components: []transfer.ComponentID{{Component: component, Version: version}},
			Target:     targetSpec,
			Resolver:   transfer.NewRepositoryResolver(srcRepo, srcSpec),
		},
	)
	r.NoError(err)

	repoProvider := provider.NewComponentVersionRepositoryProvider(
		provider.WithTempDir(t.TempDir()),
	)
	resourceRepo := resource.NewResourceRepository(nil)

	credResolver := credentials.NewStaticCredentialsResolver(map[string]map[string]string{
		srcIdentity.String():    {"username": srcUser, "password": srcPass},
		targetIdentity.String(): {"username": targetUser, "password": targetPass},
	})

	transferBuilder := transfer.NewDefaultBuilder(repoProvider, resourceRepo, credResolver)
	graph, err := transferBuilder.BuildAndCheck(tgd)
	r.NoError(err)
	r.NoError(graph.Process(ctx))
}
