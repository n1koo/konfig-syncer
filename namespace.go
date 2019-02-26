package main

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

func (c *Controller) syncNamespace(key string) error {
	log.WithField("namespace", key).Info("Syncing namespace")
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	ns, err := c.namespacesLister.Get(name)
	if err != nil {
		return err
	}

	nsLabels := ns.GetLabels()

	go c.syncSecretsToNamespace(name, nsLabels)
	go c.syncConfigMapsToNamespace(name, nsLabels)

	return nil
}

func (c *Controller) syncSecretsToNamespace(ns string, nsLabels map[string]string) {
	secrets, err := c.secretsLister.List(labels.Everything())
	if err != nil {
		log.Error(err)
	}

	//Create missing secrets
	for _, s := range secrets {
		if _, ok := s.Annotations[syncAnnotation]; !ok {
			//Skip objects that dont have our annotation
			continue
		}

		l := strings.Split(s.Annotations[syncAnnotation], "=")
		if len(l) > 2 {
			log.WithFields(log.Fields{"secret": s.Name, "annotation": s.Annotations, "split": l, "splitlen": len(l), "namespace": ns}).Warn("Annotation not valid")
			continue
		}

		if s.Annotations[syncAnnotation] == "" || nsLabels[l[0]] == l[1] {
			_, err := c.secretsLister.Secrets(ns).Get(s.Name)
			if errors.IsNotFound(err) {
				log.WithFields(log.Fields{"secret": s.Name, "namespace": ns}).Info("Adding secret")
				newSecret := createNewSecret(s)
				_, err = c.kubeclientset.CoreV1().Secrets(ns).Create(newSecret)
				if err != nil {
					log.Error(err)
				}
			} else if err != nil {
				log.Error(err)
			}
		}
	}
	c.deleteDeprecatedSecretsFromNs(ns, nsLabels)
}

func (c *Controller) syncConfigMapsToNamespace(ns string, nsLabels map[string]string) {
	configMaps, err := c.configMapsLister.List(labels.Everything())
	if err != nil {
		log.Error(err)
	}

	for _, cm := range configMaps {
		if _, ok := cm.Annotations[syncAnnotation]; !ok {
			//Skip objects that dont have our annotation
			continue
		}

		l := strings.Split(cm.Annotations[syncAnnotation], "=")
		if len(l) > 2 {
			log.WithFields(log.Fields{"configmap": cm.Name, "annotation": cm.Annotations, "split": l, "splitlen": len(l), "namespace": ns}).Warn("Annotation not valid")
			continue
		}

		if cm.Annotations[syncAnnotation] == "" || nsLabels[l[0]] == l[1] {
			_, err := c.configMapsLister.ConfigMaps(ns).Get(cm.Name)
			if errors.IsNotFound(err) {
				log.WithFields(log.Fields{"configmap": cm.Name, "namespace": ns}).Info("Adding configmap")
				newConfigMap := createNewConfigMap(cm)
				_, err = c.kubeclientset.CoreV1().ConfigMaps(ns).Create(newConfigMap)
				if err != nil {
					log.Error(err)
				}
			} else if err != nil {
				log.Error(err)
			}
		}
	}
	c.deleteDeprecatedConfigMapsFromNs(ns, nsLabels)
}

func (c *Controller) namespacesForLabel(label string) (sets.String, error) {
	var namespaces []*v1.Namespace
	var err error

	if label != "" {
		l := strings.Split(label, "=")
		if len(l) != 2 {
			return nil, fmt.Errorf("%s not valid label", label)
		}
		labelsmap := labels.Set{
			l[0]: l[1],
		}
		namespaces, err = c.namespacesLister.List(labels.SelectorFromSet(labelsmap))
	} else {
		namespaces, err = c.namespacesLister.List(labels.Everything())
	}

	if err != nil {
		return nil, err
	}

	ns := sets.NewString()
	for _, obj := range namespaces {
		ns.Insert(obj.Name)
	}
	return ns, nil
}

func (c *Controller) deleteDeprecatedConfigMapsFromNs(ns string, nsLabels map[string]string) {
	//Delete configmaps that dont match to labels anymore
	configMaps, err := c.configMapsLister.ConfigMaps(ns).List(labels.Everything())
	for _, configMap := range configMaps {
		cma, ok := configMap.Annotations[fmt.Sprintf("%s-%s", syncAnnotation, "metadata")]
		if !ok {
			continue
		}

		l, succeed := jsonLabelToArray(cma)
		if !succeed || len(l) == 0 {
			continue
		}

		//Label still exists
		if len(l) == 2 && nsLabels[l[0]] == l[1] {
			log.Debug("ConfigMap matched labels")
			continue
		}

		log.WithFields(log.Fields{"nsLabels": nsLabels, "l": l, "wtf": nsLabels[l[0]]}).Debug("Configmap didnt match labels")

		err = c.kubeclientset.CoreV1().ConfigMaps(ns).Delete(configMap.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"configmap": configMap.Name, "namespace": ns}).Info("ConfigMap deleted")
	}
}

func (c *Controller) deleteDeprecatedSecretsFromNs(ns string, nsLabels map[string]string) {
	//Delete secrets that dont match to labels anymore
	secrets, err := c.secretsLister.Secrets(ns).List(labels.Everything())
	for _, secret := range secrets {
		sa, ok := secret.Annotations[fmt.Sprintf("%s-%s", syncAnnotation, "metadata")]
		if !ok {
			continue
		}

		l, succeed := jsonLabelToArray(sa)
		if !succeed || len(l) == 0 {
			continue
		}

		//Label still exists
		if len(l) == 2 && nsLabels[l[0]] == l[1] {
			log.Debug("Secret matched labels")
			continue
		}

		log.WithFields(log.Fields{"nsLabels": nsLabels, "l": l, "wtf": nsLabels[l[0]]}).Debug("Secret didnt match labels")

		err = c.kubeclientset.CoreV1().Secrets(ns).Delete(secret.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"secret": secret.Name, "namespace": ns}).Info("Secret deleted")
	}
}

func jsonLabelToArray(jsonLabel string) (label []string, succeed bool) {
	m := make(map[string]string)

	err := json.Unmarshal([]byte(jsonLabel), &m)

	if err != nil {
		log.WithFields(log.Fields{"data": jsonLabel}).Error(err)
		return []string{}, false
	}

	//Global configMap
	if m["label"] == "" {
		return []string{}, true
	}

	label = strings.Split(m["label"], "=")
	if len(label) > 2 {
		log.WithFields(log.Fields{"data": jsonLabel, "label": label, "labellen": len(label)}).Warn("Annotation not valid")
		return []string{}, false
	}
	return label, true
}
