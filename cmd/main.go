package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/zerospiel/portforward"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type labelFlags map[string]string

func (l *labelFlags) String() string {
	return fmt.Sprintf("%v", *l)
}

func (l *labelFlags) Set(value string) error {
	label := strings.SplitN(value, "=", 2)

	if len(label) != 2 {
		return errors.New("label must include equals sign.")
	}

	(*l)[label[0]] = label[1]
	return nil
}

func main() {
	labels := labelFlags{}

	var resource string
	var namespace string
	var port int
	var listenPort int

	flag.Var(&labels, "label", "pod labels to look for")
	flag.IntVar(&listenPort, "listen", 0, "port to bind to, random if 0")
	flag.IntVar(&port, "port", 80, "port to forward to")
	flag.StringVar(&resource, "resname", "", "resource name")
	flag.StringVar(&namespace, "namespace", "default", "namespace to look for the pod in")
	flag.Parse()

	pf, err := portforward.NewPortForwarder(namespace, metav1.LabelSelector{
		MatchLabels: labels,
	}, port)
	if err != nil {
		log.Fatal("Error setting up port forwarder: ", err)
	}
	pf.Name = resource
	pf.ListenPort = listenPort

	err = pf.Start(context.Background())
	if err != nil {
		log.Fatal("Error starting port forward: ", err)
	}

	log.Printf("Started tunnel on %d\n", pf.ListenPort)
	time.Sleep(60 * time.Second)
}
