package portforward

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type ResourceForwardOption func(*PortForward) *PortForward

func WithPodForward() ResourceForwardOption {
	return func(pf *PortForward) *PortForward {
		pf.resType = podType
		return pf
	}
}

func WithServiceForward() ResourceForwardOption {
	return func(pf *PortForward) *PortForward {
		pf.resType = serviceType
		return pf
	}
}

type resType int

const (
	podType resType = iota
	serviceType
)

func (t resType) String() string {
	switch t {
	case podType:
		return "pod"
	case serviceType:
		return "service"
	default:
		return "unknown"
	}
}

// Used for creating a port forward into a Kubernetes resource (service, deployment or pod)
// in a Kubernetes cluster.
type PortForward struct {
	// The parsed Kubernetes configuration file.
	Config *rest.Config
	// The initialized Kubernetes client.
	Clientset kubernetes.Interface

	stopChan  chan struct{}
	readyChan chan struct{}

	// The resource name to use, required if Labels is empty.
	Name string
	// The namespace to look for the resource in.
	Namespace string

	// The labels to use to find the resource.
	Labels metav1.LabelSelector
	// The port on the resource to forward traffic to.
	DestinationPort int
	// The port that the port forward should listen to, random if not set.
	ListenPort int

	resType resType
}

// Initialize a port forwarder, loads the Kubernetes configuration file and creates the client.
// You do not need to use this function if you have a client to use already - the PortForward
// struct can be created directly.
func NewPortForwarder(namespace string, labels metav1.LabelSelector, port int, opts ...ResourceForwardOption) (*PortForward, error) {
	pf := &PortForward{
		Namespace:       namespace,
		Labels:          labels,
		DestinationPort: port,
		resType:         serviceType,
	}

	for _, o := range opts {
		pf = o(pf)
	}

	var err error
	pf.Config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return pf, fmt.Errorf("could not load kubernetes configuration file: %w", err)
	}

	pf.Clientset, err = kubernetes.NewForConfig(pf.Config)
	if err != nil {
		return pf, fmt.Errorf("could not create kubernetes client: %w", err)
	}

	return pf, nil
}

// Start a port forward to a resource - blocks until the tunnel is ready for use.
func (p *PortForward) Start(ctx context.Context) error {
	p.stopChan = make(chan struct{}, 1)
	p.readyChan = make(chan struct{}, 1)
	errChan := make(chan error, 1)

	listenPort, err := p.getListenPort()
	if err != nil {
		return fmt.Errorf("could not find a port to bind to: %w", err)
	}

	dialer, err := p.dialer(ctx)
	if err != nil {
		return fmt.Errorf("could not create a dialer: %w", err)
	}

	ports := []string{
		fmt.Sprintf("%d:%d", listenPort, p.DestinationPort),
	}

	discard := ioutil.Discard
	pf, err := portforward.New(dialer, ports, p.stopChan, p.readyChan, discard, discard)
	if err != nil {
		return fmt.Errorf("could not port forward into %s: %w", p.resType, err)
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		return fmt.Errorf("could not create port forward: %w", err)
	case <-p.readyChan:
		return nil
	}
}

// Stop a port forward.
func (p *PortForward) Stop() {
	p.stopChan <- struct{}{}
}

// Returns the port that the port forward should listen on.
// If ListenPort is set, then it returns ListenPort.
// Otherwise, it will call getFreePort() to find an open port.
func (p *PortForward) getListenPort() (int, error) {
	var err error

	if p.ListenPort == 0 {
		p.ListenPort, err = p.getFreePort()
	}

	return p.ListenPort, err
}

// Get a free port on the system by binding to port 0, checking
// the bound port number, and then closing the socket.
func (p *PortForward) getFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	port := listener.Addr().(*net.TCPAddr).Port
	err = listener.Close()
	if err != nil {
		return 0, err
	}

	return port, nil
}

// Create an httpstream.Dialer for use with portforward.New
func (p *PortForward) dialer(ctx context.Context) (httpstream.Dialer, error) {
	resourceName, err := p.getResourceName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get %s name: %w", p.resType, err)
	}

	url := p.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(p.Namespace).
		Name(resourceName).
		SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(p.Config)
	if err != nil {
		return nil, fmt.Errorf("could not create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	return dialer, nil
}

// Gets the resource name to port forward to, if Name is set, Name is returned. Otherwise,
// it will call findResourceByLabels().
func (p *PortForward) getResourceName(ctx context.Context) (string, error) {
	var err error
	if p.Name == "" {
		p.Name, err = p.findResourceByLabels(ctx)
	}
	return p.Name, err
}

// Find the name of a resource by label, returns an error if the label returns
// more or less than one resource.
// It searches for the labels specified by labels.
func (p *PortForward) findResourceByLabels(ctx context.Context) (string, error) {
	if len(p.Labels.MatchLabels) == 0 && len(p.Labels.MatchExpressions) == 0 {
		return "", fmt.Errorf("no %s labels specified", p.resType)
	}

	switch p.resType {
	case podType:
		return p.getPodName(ctx)
	case serviceType:
		return p.getFromEndpoints(ctx)
	default:
		return "", fmt.Errorf("unknown resource type")
	}
}

func (p *PortForward) getPodName(ctx context.Context) (string, error) {
	formatLabelSel := metav1.FormatLabelSelector(&p.Labels)
	pods, err := p.Clientset.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: formatLabelSel,
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(v1.PodRunning)).String(),
	})
	if err != nil {
		return "", fmt.Errorf("listing pods in kubernetes: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("could not find running %s for selector: labels \"%s\"", p.resType, formatLabelSel)
	}

	if len(pods.Items) != 1 {
		return "", fmt.Errorf("ambiguous %s: found more than one %s for selector: labels \"%s\"", p.resType, p.resType, formatLabelSel)
	}

	return pods.Items[0].ObjectMeta.Name, nil
}

func init() {
	rand.Seed(time.Now().UnixNano()) //nolint:gosec
}

func (p *PortForward) getFromEndpoints(ctx context.Context) (string, error) {
	formatLabelSel := metav1.FormatLabelSelector(&p.Labels)
	eps, err := p.Clientset.CoreV1().Endpoints(p.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: formatLabelSel,
		// no field sel
	})
	if err != nil {
		return "", fmt.Errorf("listing endpoints in kubernetes: %w", err)
	}

	if len(eps.Items) == 0 {
		return "", fmt.Errorf("could not find running endpoints for selector: labels \"%s\"", formatLabelSel)
	}

	randEp := eps.Items[rand.Intn(len(eps.Items))]
	for _, s := range randEp.Subsets {
		for _, a := range s.Addresses {
			if a.TargetRef != nil {
				return a.TargetRef.Name, nil
			}
		}
	}

	return "", fmt.Errorf("could not find any pods attached to endpoint %s", randEp.Name)
}
