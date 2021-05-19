package strategy

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/client-go/operator/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/image/reference"
)

const (
	icsp = `apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: release
spec:
  repositoryDigestMirrors:
  - mirrors:
    - does.not.exist/match/image
    source: docker.io/ocp-test/does-not-exist
  - mirrors:
    - exists/match/image
    source: quay.io/ocp-test/does-not-exist
`
)

func TestAlternativeImageSources(t *testing.T) {
	icspFile, _ := ioutil.TempFile("", "test-icsp")
	defer os.Remove(icspFile.Name())
	fmt.Fprintf(icspFile, icsp)

	tests := []struct {
		name                 string
		icspList             []runtime.Object
		icspFile             string
		image                string
		imageSourcesExpected []string
	}{
		{
			name: "multiple ICSPs",
			icspList: []runtime.Object{
				&operatorv1alpha1.ImageContentSourcePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: "release",
					},
					Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
						RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
							{
								Source: "quay.io/multiple/icsps",
								Mirrors: []string{
									"someregistry/somerepo/release",
								},
							},
							{
								Source: "quay.io/ocp-test/another-release",
								Mirrors: []string{
									"someregistry/repo/does-not-exist",
								},
							},
						},
					},
				},
				&operatorv1alpha1.ImageContentSourcePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: "another",
					},
					Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
						RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
							{
								Source: "quay.io/multiple/icsps",
								Mirrors: []string{
									"anotherregistry/anotherrepo/release",
								},
							},
						},
					},
				},
			},
			icspFile:             "",
			image:                "quay.io/multiple/icsps:4.5",
			imageSourcesExpected: []string{"quay.io/multiple/icsps", "anotherregistry/anotherrepo/release", "someregistry/somerepo/release"},
		},
		{
			name:                 "sources match ICSP file",
			icspFile:             icspFile.Name(),
			image:                "quay.io/ocp-test/does-not-exist:4.7",
			imageSourcesExpected: []string{"quay.io/ocp-test/does-not-exist", "exists/match/image"},
		},
		{
			name:                 "no match ICSP file",
			icspFile:             icspFile.Name(),
			image:                "quay.io/passed/image:4.5",
			imageSourcesExpected: []string{"quay.io/passed/image"},
		},
		{
			name: "ICSP mirrors match image",
			icspList: []runtime.Object{
				&operatorv1alpha1.ImageContentSourcePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: "release",
					},
					Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
						RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
							{
								Source: "quay.io/ocp-test/release",
								Mirrors: []string{
									"someregistry/mirrors/match",
								},
							},
						},
					},
				},
			},
			icspFile:             "",
			image:                "quay.io/ocp-test/release:4.5",
			imageSourcesExpected: []string{"quay.io/ocp-test/release", "someregistry/mirrors/match"},
		},
		{
			name: "ICSP source matches image",
			icspList: []runtime.Object{
				&operatorv1alpha1.ImageContentSourcePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: "release",
					},
					Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
						RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
							{
								Source: "quay.io/source/matches",
								Mirrors: []string{
									"someregistry/somerepo/release",
								},
							},
						},
					},
				},
			},
			icspFile:             "",
			image:                "quay.io/source/matches:4.5",
			imageSourcesExpected: []string{"quay.io/source/matches", "someregistry/somerepo/release"},
		},
		{
			name: "source image matches multiple mirrors",
			icspList: []runtime.Object{
				&operatorv1alpha1.ImageContentSourcePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: "release",
					},
					Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
						RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
							{
								Source: "quay.io/ocp-test/release",
								Mirrors: []string{
									"someregistry/mirrors/match",
									"quay.io/another/release",
									"quay.io/andanother/release",
								},
							},
						},
					},
				},
			},
			image:                "quay.io/ocp-test/release:4.5",
			imageSourcesExpected: []string{"quay.io/ocp-test/release", "someregistry/mirrors/match", "quay.io/another/release", "quay.io/andanother/release"},
		},
		{
			name:                 "no ICSP",
			image:                "quay.io/ocp-test/release:4.5",
			imageSourcesExpected: []string{"quay.io/ocp-test/release"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected := []reference.DockerImageReference{}
			for _, e := range tt.imageSourcesExpected {
				ref, _ := reference.Parse(e)
				expected = append(expected, ref)
			}

			client := fake.NewSimpleClientset(tt.icspList...)
			alternates := NewSimpleLookupICSPStrategy(tt.icspFile, client.OperatorV1alpha1().ImageContentSourcePolicies())
			imageRef, _ := reference.Parse(tt.image)

			actual, err := alternates.OnFailure(context.Background(), imageRef)
			if err != nil {
				t.Errorf("Unexpected error %v", err)
				return
			}
			if actions := client.Actions(); len(actions) > 1 {
				t.Errorf("Unexpected calls to ICSP client, should be at most 1 got %#v", actions)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Errorf("Unexpected alternates got = %v, want %v", actual, expected)
			}

			// additional calls shouldn't trigger ICSP re-read
			alternates.OnFailure(context.Background(), imageRef)
			if actions := client.Actions(); len(actions) > 1 {
				t.Errorf("Unexpected calls to ICSP client, should be at most 1 got %#v", actions)
			}
		})
	}
}
