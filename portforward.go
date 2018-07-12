package portforward

import (
	"fmt"
	"github.com/pkg/errors"
	"io/ioutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
)

// Used for creating a port forward into a Kubernetes pod
// in a Kubernetes cluster.
type PortForward struct {
	// The parsed Kubernetes configuration file.
	Config *rest.Config
	// The initialized Kubernetes client.
	Clientset kubernetes.Interface
	// The pod name to use, required if Labels is empty.
	Name string
	// The labels to use to find the pod.
	Labels map[string]string
	// The port on the pod to forward traffic to.
	DestinationPort int
	// The port that the port forward should listen to, random if not set.
	ListenPort int
	// The namespace to look for the pod in.
	Namespace string
	stopChan  chan struct{}
	readyChan chan struct{}
}

// Initialize a port forwarder, loads the Kubernetes configuration file and creates the client.
// You do not need to use this function if you have a client to use already - the PortForward
// struct can be created directly.
func NewPortForwarder(namespace string, labels map[string]string, port int) (*PortForward, error) {
	pf := &PortForward{
		Namespace:       namespace,
		Labels:          labels,
		DestinationPort: port,
	}

	configPath, err := pf.getKubeConfigPath()
	if err != nil {
		return pf, errors.Wrap(err, "Could not find kubernetes configuration file")
	}

	pf.Config, err = clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return pf, errors.Wrap(err, "Could not load kubernetes configuration file")
	}

	pf.Clientset, err = kubernetes.NewForConfig(pf.Config)
	if err != nil {
		return pf, errors.Wrap(err, "Could not create kubernetes client")
	}

	return pf, nil
}

// Returns the path to the kubernetes config file.
// If KUBECONFIG is set, it will use KUBECONFIG.
// Otherwise, it will return ~/.kube/config.
func (p *PortForward) getKubeConfigPath() (string, error) {
	kubeconfig := ""

	if os.Getenv("KUBECONFIG") != "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	} else {
		user, err := user.Current()
		if err != nil {
			return "", err
		}

		kubeconfig = filepath.Join(user.HomeDir, ".kube", "config")
	}

	return kubeconfig, nil
}

// Start a port forward to a pod - blocks until the tunnel is ready for use.
func (p *PortForward) Start() error {
	p.stopChan = make(chan struct{}, 1)
	readyChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	listenPort, err := p.getListenPort()
	if err != nil {
		return errors.Wrap(err, "Could not find a port to bind to")
	}

	dialer, err := p.dialer()
	if err != nil {
		return errors.Wrap(err, "Could not create a dialer")
	}

	ports := []string{
		fmt.Sprintf("%d:%d", listenPort, p.DestinationPort),
	}

	discard := ioutil.Discard
	pf, err := portforward.New(dialer, ports, p.stopChan, readyChan, discard, discard)
	if err != nil {
		return errors.Wrap(err, "Could not port forward into pod")
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		return errors.Wrap(err, "Could not create port forward")
	case <-readyChan:
		return nil
	}

	return nil
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
func (p *PortForward) dialer() (httpstream.Dialer, error) {
	pod, err := p.getPodName()
	if err != nil {
		return nil, errors.Wrap(err, "Could not get pod name")
	}

	url := p.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(p.Namespace).
		Name(pod).
		SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(p.Config)
	if err != nil {
		return nil, errors.Wrap(err, "Could not create round tripper")
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	return dialer, nil
}

// Gets the pod name to port forward to, if Name is set, Name is returned. Otherwise,
// it will call findPodByLabels().
func (p *PortForward) getPodName() (string, error) {
	var err error
	if p.Name == "" {
		p.Name, err = p.findPodByLabels()
	}
	return p.Name, err
}

// Find the name of a pod by label, returns an error if the label returns
// more or less than one pod.
// It searches for the labels specified by labels.
func (p *PortForward) findPodByLabels() (string, error) {
	if len(p.Labels) == 0 {
		return "", errors.New("No pod labels specified")
	}

	pods, err := p.Clientset.CoreV1().Pods(p.Namespace).List(metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
			MatchLabels: p.Labels,
		}),
	})

	if err != nil {
		return "", errors.Wrap(err, "Listing pods in kubernetes")
	}

	if len(pods.Items) == 0 {
		return "", errors.New(fmt.Sprintf("Could not find pod for selector: labels %v", p.Labels))
	}

	if len(pods.Items) != 1 {
		return "", errors.New(fmt.Sprintf("Ambiguous pod: found more than one pod for selector: labels %v", p.Labels))
	}

	return pods.Items[0].ObjectMeta.Name, nil
}