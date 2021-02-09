package portforward

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakekubernetes "k8s.io/client-go/kubernetes/fake"
)

func newPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
			Name:   name,
		},
	}
}

func Test_findResourceByLabels(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod1", map[string]string{
				"name": "other",
			}),
			newPod("mypod2", map[string]string{
				"name": "flux",
			}),
			newPod("mypod3", map[string]string{})),
		Labels: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "flux",
			},
		},
	}

	pod, err := pf.findResourceByLabels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mypod2", pod)
}

func Test_findResourceByLabels_NoneExist(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod1", map[string]string{
				"name": "other",
			})),
		Labels: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "flux",
			},
		},
	}

	_, err := pf.findResourceByLabels(context.Background())
	require.Error(t, err)
	assert.Equal(t, "could not find running pod for selector: labels \"name=flux\"", err.Error())
}

func Test_findResourceByLabels_Multiple(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod1", map[string]string{
				"name": "flux",
			}),
			newPod("mypod2", map[string]string{
				"name": "flux",
			}),
			newPod("mypod3", map[string]string{})),
		Labels: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "flux",
			},
		},
	}

	_, err := pf.findResourceByLabels(context.Background())
	require.Error(t, err)
	assert.Equal(t, "ambiguous pod: found more than one pod for selector: labels \"name=flux\"", err.Error())
}

func Test_findResourceByLabels_Expression(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod1", map[string]string{
				"name": "lol",
			}),
			newPod("mypod2", map[string]string{
				"name": "fluxd",
			}),
			newPod("mypod3", map[string]string{})),
		Labels: metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "name",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"flux", "fluxd"},
				},
			},
		},
	}

	pod, err := pf.findResourceByLabels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mypod2", pod)
}

func Test_findResourceByLabels_ExpressionNotFound(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod1", map[string]string{
				"name": "lol",
			}),
			newPod("mypod2", map[string]string{
				"name": "lol",
			}),
			newPod("mypod3", map[string]string{})),
		Labels: metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "name",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"flux", "fluxd"},
				},
			},
		},
	}

	_, err := pf.findResourceByLabels(context.Background())
	require.Error(t, err)
	assert.Equal(t, "could not find running pod for selector: labels \"name in (flux,fluxd)\"", err.Error())
}

func Test_getResourceName_NameSet(t *testing.T) {
	pf := PortForward{
		Name: "hello",
	}

	pod, err := pf.getResourceName(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "hello", pod)
}

func Test_getResourceName_NoNameSet(t *testing.T) {
	pf := PortForward{
		resType: podType,
		Clientset: fakekubernetes.NewSimpleClientset(
			newPod("mypod", map[string]string{
				"name": "flux",
			})),
		Labels: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "flux",
			},
		},
	}

	pod, err := pf.getResourceName(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mypod", pod)
	assert.Equal(t, pf.Name, pod)
}

func TestGetFreePort(t *testing.T) {
	pf := PortForward{}
	port, err := pf.getFreePort()
	require.NoError(t, err)
	assert.NotZero(t, port)
}

func TestGetListenPort(t *testing.T) {
	pf := PortForward{
		ListenPort: 80,
	}

	port, err := pf.getListenPort()
	require.NoError(t, err)
	assert.Equal(t, 80, port)
}

func TestGetListenPortRandom(t *testing.T) {
	pf := PortForward{}

	port, err := pf.getListenPort()
	require.NoError(t, err)
	assert.NotZero(t, port)
	assert.Equal(t, pf.ListenPort, port)
}
