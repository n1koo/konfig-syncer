package main

import (
	"flag"
	"time"

	"github.com/n1koo/konfig-syncer/pkg/signals"
	log "github.com/sirupsen/logrus"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	masterURL         string
	kubeconfig        string
	debug             bool
	humanReadableLogs bool
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&debug, "debug", false, "Enable debug mode")
	flag.BoolVar(&humanReadableLogs, "human-readable-logs", false, "Log in human readable mode rather than default json")
	flag.Set("logtostderr", "true")
}

func main() {
	flag.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}
	if !humanReadableLogs {
		log.SetFormatter(&log.JSONFormatter{})
	}

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		log.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Minute*1)

	c := NewController(kubeClient,
		kubeInformerFactory.Core().V1().ConfigMaps(),
		kubeInformerFactory.Core().V1().Secrets(),
		kubeInformerFactory.Core().V1().Namespaces(),
	)

	kubeInformerFactory.Start(stopCh)

	if err = c.Run(2, stopCh); err != nil {
		log.Fatalf("Error running controller: %s", err.Error())
	}
}
