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

func (c *Controller) syncConfigMap(key string) error {
	obj, exists, _ := c.deletedConfigMapIndexer.GetByKey(key)
	if exists {
		log.Debug("Cleanup configmaps that were added by old origin ConfigMap")
		c.deletedConfigMapIndexer.Delete(key)
		s := obj.(*corev1.ConfigMap)
		c.deleteSyncedConfigMaps(s)
	}

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	sourceConfigMap, err := c.configMapsLister.ConfigMaps(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
          return nil
		}
		return err
	}

	namespaces, err := c.namespacesForLabel(sourceConfigMap.Annotations[syncAnnotation])
	if err != nil {
		return nil
	}

	newConfigMap := createNewConfigMap(sourceConfigMap)
	for _, ns := range namespaces.UnsortedList() {
		if ns == sourceConfigMap.Namespace {
			continue
		}

		targetConfigMap, err := c.configMapsLister.ConfigMaps(ns).Get(newConfigMap.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				_, err = c.kubeclientset.CoreV1().ConfigMaps(ns).Create(newConfigMap)
				if err != nil {
					log.Error(err)
				}
				log.WithFields(log.Fields{"configMap": newConfigMap.Name, "namespace": ns}).Info("ConfigMap added")
			} else {
				log.Error(err)
			}
			continue
		}

		if reflect.DeepEqual(targetConfigMap.Data, newConfigMap.Data) {
			log.WithFields(log.Fields{"configMap": newConfigMap.Name, "namespace": ns}).Debug("Data hasn't changed, dont sync")
			continue
		}
		_, err = c.kubeclientset.CoreV1().ConfigMaps(ns).Update(newConfigMap)
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"configMap": newConfigMap.Name, "namespace": ns}).Info("ConfigMap updated")
	}
	return nil
}

func (c *Controller) deleteSyncedConfigMaps(s *corev1.ConfigMap) {
	log.Debug("ConfigMap was deleted, lets delete the synced copies")
	namespaces, err := c.namespacesForLabel(s.Annotations[syncAnnotation])
	if err != nil {
		log.Error(err)
		return
	}

	for _, ns := range namespaces.UnsortedList() {
		if ns == s.Namespace {
			continue
		}
		err = c.kubeclientset.CoreV1().ConfigMaps(ns).Delete(s.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Error(err)
		}
		log.WithFields(log.Fields{"ConfigMap": s.Name, "namespace": ns}).Info("ConfigMap deleted")
	}
}



func createNewConfigMap(sourceConfigMap *corev1.ConfigMap) *corev1.ConfigMap {
	newConfigMap := sourceConfigMap.DeepCopy()

	delete(newConfigMap.Annotations, syncAnnotation)
	newConfigMap.ResourceVersion = ""
	newConfigMap.Namespace = ""
	newConfigMap.UID = ""
	newConfigMap.GenerateName = ""
	newConfigMap.SelfLink = ""
	newConfigMap.CreationTimestamp.Reset()
	if newConfigMap.Annotations == nil {
		newConfigMap.Annotations = make(map[string]string)
	}
	kubeSyncAnnotationValue := fmt.Sprintf(`{"namespace":%q,"name":%q,"uid":%q,"resourceVersion":%q,"label":%q,"last-update":%q}`,
		sourceConfigMap.Namespace,
		sourceConfigMap.Name,
		sourceConfigMap.UID,
		sourceConfigMap.ResourceVersion,
		sourceConfigMap.Annotations[syncAnnotation],
		time.Now().String())
	newConfigMap.Annotations[fmt.Sprintf("%s-%s", syncAnnotation, "metadata")] = kubeSyncAnnotationValue
	return newConfigMap
}

