package main

import (
	"fmt"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
)

func (c *Controller) syncSecret(key string) error {
	obj, exists, _ := c.deletedSecretIndexer.GetByKey(key)
	if exists {
		log.Debug("Cleanup secrets that were added by old origin Secret")
		c.deletedSecretIndexer.Delete(key)
		s := obj.(*corev1.Secret)
		c.deleteSyncedSecrets(s)
	}

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	sourceSecret, err := c.secretsLister.Secrets(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	namespaces, err := c.namespacesForLabel(sourceSecret.Annotations[syncAnnotation])
	if err != nil {
		return nil
	}

	newSecret := createNewSecret(sourceSecret)
	for _, ns := range namespaces.UnsortedList() {
		if ns == sourceSecret.Namespace {
			continue
		}
		targetSecret, err := c.secretsLister.Secrets(ns).Get(newSecret.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				_, err = c.kubeclientset.CoreV1().Secrets(ns).Create(newSecret)
				if err != nil {
					log.Error(err)
				}
				log.WithFields(log.Fields{"secret": newSecret.Name, "namespace": ns}).Info("Secret added")
				continue
			} else {
				log.Error(err)
			}
			continue
		}

		if reflect.DeepEqual(targetSecret.Data, newSecret.Data) {
			log.WithFields(log.Fields{"secret": newSecret.Name, "namespace": ns}).Debug("Data hasn't changed, dont sync")
			continue
		}
		_, err = c.kubeclientset.CoreV1().Secrets(ns).Update(newSecret)
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"secret": newSecret.Name, "namespace": ns}).Info("Secret updated")

	}
	return nil
}

func (c *Controller) deleteSyncedSecrets(s *corev1.Secret) {
	log.Debug("Secret was deleted, lets delete the synced copies")
	namespaces, err := c.namespacesForLabel(s.Annotations[syncAnnotation])
	if err != nil {
		log.Error(err)
		return
	}

	for _, ns := range namespaces.UnsortedList() {
		if ns == s.Namespace {
			continue
		}
		err = c.kubeclientset.CoreV1().Secrets(ns).Delete(s.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"secret": s.Name, "namespace": ns}).Info("Secret deleted")
	}
}

func createNewSecret(sourceSecret *corev1.Secret) *corev1.Secret {
	newSecret := sourceSecret.DeepCopy()

	delete(newSecret.Annotations, syncAnnotation)
	newSecret.ResourceVersion = ""
	newSecret.Namespace = ""
	newSecret.UID = ""
	newSecret.GenerateName = ""
	newSecret.SelfLink = ""
	newSecret.CreationTimestamp.Reset()
	if newSecret.Annotations == nil {
		newSecret.Annotations = make(map[string]string)
	}
	kubeSyncAnnotationValue := fmt.Sprintf(`{"namespace":%q,"name":%q,"uid":%q,"resourceVersion":%q,"label":%q,"last-update":%q}`,
		sourceSecret.Namespace,
		sourceSecret.Name,
		sourceSecret.UID,
		sourceSecret.ResourceVersion,
		sourceSecret.Annotations[syncAnnotation],
		time.Now().String())
	newSecret.Annotations[fmt.Sprintf("%s-%s", syncAnnotation, "metadata")] = kubeSyncAnnotationValue
	return newSecret
}
