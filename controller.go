package main

import (
	"fmt"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const syncAnnotation string = "konfig-syncer"

// Controller is responsible for watching Secret/Configmap events and adding them to WQ for processing
type Controller struct {
	kubeclientset kubernetes.Interface

	configMapsLister        corelisters.ConfigMapLister
	configMapsSynced        cache.InformerSynced
	configMapWorkqueue      workqueue.RateLimitingInterface
	deletedConfigMapIndexer cache.Indexer

	secretsLister        corelisters.SecretLister
	secretsSynced        cache.InformerSynced
	secretWorkqueue      workqueue.RateLimitingInterface
	deletedSecretIndexer cache.Indexer

	namespacesLister   corelisters.NamespaceLister
	namespacesSynced   cache.InformerSynced
	namespaceWorkqueue workqueue.RateLimitingInterface
}

// NewController creates controller FIXME proper comment
func NewController(
	kubeclientset kubernetes.Interface,
	configMapInformer coreinformers.ConfigMapInformer,
	secretInformer coreinformers.SecretInformer,
	namespaceInformer coreinformers.NamespaceInformer) *Controller {

	controller := &Controller{
		kubeclientset:           kubeclientset,
		configMapsLister:        configMapInformer.Lister(),
		configMapsSynced:        configMapInformer.Informer().HasSynced,
		configMapWorkqueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ConfigMaps"),
		deletedConfigMapIndexer: cache.NewIndexer(cache.DeletionHandlingMetaNamespaceKeyFunc, cache.Indexers{}),
		secretsLister:           secretInformer.Lister(),
		secretsSynced:           secretInformer.Informer().HasSynced,
		secretWorkqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Secrets"),
		deletedSecretIndexer:    cache.NewIndexer(cache.DeletionHandlingMetaNamespaceKeyFunc, cache.Indexers{}),
		namespacesLister:        namespaceInformer.Lister(),
		namespacesSynced:        namespaceInformer.Informer().HasSynced,
		namespaceWorkqueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Namespaces"),
	}

	secretInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			s := new.(*corev1.Secret)
			if _, ok := s.Annotations[syncAnnotation]; ok {
				log.Debug("Secret added to workqueue")
				controller.enqueueSecret(new)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			news := new.(*corev1.Secret)
			olds := old.(*corev1.Secret)
			nanno, newHasAnno := news.Annotations[syncAnnotation]
			oanno, oldHasAnno := olds.Annotations[syncAnnotation]

			if newHasAnno && (!oldHasAnno || !reflect.DeepEqual(news.Data, olds.Data)) {
				log.Debug("Secret updated to have sync annotation or data changed")
				controller.enqueueSecret(news)
			} else if !newHasAnno && oldHasAnno {
				log.Debug("Sync annotation was removed from Secret")
				controller.deletedSecretIndexer.Add(olds)
				controller.enqueueSecret(news)
			} else {
				if !reflect.DeepEqual(nanno, oanno) {
					log.Debug("Sync annotation was was changed on secret")
					controller.deletedSecretIndexer.Add(olds)
					controller.enqueueSecret(news)
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			log.Debug("Secret deleted")
			s := obj.(*corev1.Secret)
			if _, ok := s.Annotations[syncAnnotation]; ok {
				controller.enqueueSecret(obj)
				controller.deletedSecretIndexer.Add(obj)
			}
		},
	})

	configMapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			s := new.(*corev1.ConfigMap)
			if _, ok := s.Annotations[syncAnnotation]; ok {
				log.Debug("ConfigMap added to workqueue")
				controller.enqueueConfigMap(s)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			news := new.(*corev1.ConfigMap)
			olds := old.(*corev1.ConfigMap)

			nanno, newHasAnno := news.Annotations[syncAnnotation]
			oanno, oldHasAnno := olds.Annotations[syncAnnotation]

			if newHasAnno && (!oldHasAnno || !reflect.DeepEqual(news.Data, olds.Data)) {
				log.Debug("ConfigMap updated to have sync annotation or data changed")
				controller.enqueueConfigMap(news)
			} else if !newHasAnno && oldHasAnno {
				log.Debug("Sync annotation was removed from ConfigMap")
				controller.deletedConfigMapIndexer.Add(olds)
			} else if !reflect.DeepEqual(nanno, oanno) {
				log.Debug("Sync annotation was was changed on ConfigMap")
				controller.deletedConfigMapIndexer.Add(olds)
				controller.enqueueConfigMap(news)
			}
		},
		DeleteFunc: func(obj interface{}) {
			s := obj.(*corev1.ConfigMap)
			if _, ok := s.Annotations[syncAnnotation]; ok {
				log.Debug("ConfigMap deleted")
				controller.enqueueConfigMap(obj)
				controller.deletedConfigMapIndexer.Add(obj)
			}
		},
	})

	namespaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			log.Debug("Namespace added to workqueue")
			controller.enqueueNamespace(new)
		},
		UpdateFunc: func(old, new interface{}) {
			newNs := new.(*corev1.Namespace)
			oldNs := old.(*corev1.Namespace)

			if newNs.Status.Phase != corev1.NamespaceTerminating && !reflect.DeepEqual(newNs.Labels, oldNs.Labels) {
				log.Debug("Namespace added to workqueue on update")
				controller.enqueueNamespace(newNs)
			}

		},
	})

	return controller
}

// Run spins up the controller
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.configMapWorkqueue.ShutDown()
	defer c.secretWorkqueue.ShutDown()
	defer c.namespaceWorkqueue.ShutDown()

	// Wait for the caches to be synced before starting workers
	log.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.configMapsSynced, c.secretsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	log.Info("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runConfigMapWorker, time.Second, stopCh)
		go wait.Until(c.runSecretWorker, time.Second, stopCh)
		go wait.Until(c.runNamespaceWorker, time.Second, stopCh)
	}

	log.Info("Started workers")
	<-stopCh
	log.Info("Shutting down workers")

	return nil
}

func (c *Controller) enqueueSecret(obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.secretWorkqueue.AddRateLimited(key)
}

func (c *Controller) enqueueConfigMap(obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.configMapWorkqueue.AddRateLimited(key)
}

func (c *Controller) enqueueNamespace(obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.namespaceWorkqueue.AddRateLimited(key)
}

func (c *Controller) runConfigMapWorker() {
	for c.processNextConfigMap() {
	}
}

func (c *Controller) runSecretWorker() {
	for c.processNextSecret() {
	}
}

func (c *Controller) runNamespaceWorker() {
	for c.processNextNamespace() {
	}
}

func (c *Controller) processNextConfigMap() bool {
	obj, shutdown := c.configMapWorkqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.configMapWorkqueue.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.configMapWorkqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncConfigMap(key); err != nil {
			c.configMapWorkqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.configMapWorkqueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) processNextSecret() bool {
	obj, shutdown := c.secretWorkqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.secretWorkqueue.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.secretWorkqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncSecret(key); err != nil {
			c.secretWorkqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.secretWorkqueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) processNextNamespace() bool {
	obj, shutdown := c.namespaceWorkqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.namespaceWorkqueue.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			c.namespaceWorkqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		if err := c.syncNamespace(key); err != nil {
			c.namespaceWorkqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}

		c.namespaceWorkqueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}
