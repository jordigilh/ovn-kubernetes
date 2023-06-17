package apbroute

import (
	"fmt"

	adminpolicybasedrouteapi "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/adminpolicybasedroute/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

func (m *externalPolicyManager) syncNamespace(namespaceName string, namespaceLister corev1listers.NamespaceLister, routeQueue workqueue.RateLimitingInterface) error {
	namespace, err := namespaceLister.Get(namespaceName)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	cacheInfo, found := m.getNamespaceInfoFromCache(namespaceName)
	if (err != nil && apierrors.IsNotFound(err)) || (found && cacheInfo.markForDeletion) {
		// DELETE use case
		klog.Infof("Deleting namespace reference %s", namespaceName)
		err := m.processDeleteNamespace(namespaceName)
		if err != nil {
			return err
		}
		return nil
	}

	matches, err := m.getPoliciesForNamespace(namespaceName, cacheInfo)
	if err != nil {
		return err
	}
	if !found && len(matches) == 0 {
		// it's not a namespace being cached already and it is not a target for policies, nothing to do
		return nil
	}

	if found && cacheInfo.markForDeletion {
		// namespace exists and has been marked for deletion, this means there should be an event to complete deleting the namespace.
		// wait for the namespace to be deleted before recreating it in the cache.
		return fmt.Errorf("cannot add namespace %s because it is currently being deleted", namespace.Name)
	}

	// notify of changes to the policy controller
	klog.Infof("Queuing policies %+v", matches)
	return m.notifyRouteController(matches, routeQueue)
}

func (m *externalPolicyManager) notifyRouteController(policies []string, routeQueue workqueue.RateLimitingInterface) error {

	for _, policyName := range policies {
		routeQueue.Add(policyName)
	}
	return nil
}

// processDeleteNamespace processes a delete namespace event by ensuring that no pod is still running in that namespace before deleting the namespace info cache. It also
// marks the namespace for deletion so that if a new pod event appears targeting the namespace, the operation will be rejected.
func (m *externalPolicyManager) processDeleteNamespace(namespaceName string) error {
	nsInfo, found := m.getAndMarkForDeleteNamespaceInfoFromCache(namespaceName)
	if !found {
		// namespace is not a recipient for policies
		return nil
	}
	podsInNs, err := m.podLister.Pods(namespaceName).List(labels.Everything())
	if err != nil {
		return err
	}
	if len(podsInNs) != 0 {
		klog.Infof("Attempting to delete namespace %s with resources still attached to it. Retrying...", namespaceName)
		return fmt.Errorf("unable to delete namespace %s with resources still attached to it", namespaceName)
	}
	m.deleteNamespaceInfoInCache(namespaceName, nsInfo)
	return nil
}

func (m *externalPolicyManager) applyPolicyToNamespace(namespaceName string, policy *adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute) error {

	cacheInfo, found := m.getNamespaceInfoFromCache(namespaceName)
	if !found {
		klog.Infof("Namespace %s not found in cache, creating it", namespaceName)
		cacheInfo = m.newNamespaceInfoInCache(namespaceName)
	}

	processedPolicy, err := m.processExternalRoutePolicy(policy)
	if err != nil {
		return err
	}
	err = m.applyProcessedPolicyToNamespace(namespaceName, policy.Name, processedPolicy, cacheInfo)
	if err != nil {
		return err
	}
	return m.updateNamespaceInfoCache(namespaceName, cacheInfo)
}

func (m *externalPolicyManager) removePolicyFromNamespace(targetNamespace string, policy *adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute) error {

	cacheInfo, found := m.getNamespaceInfoFromCache(targetNamespace)
	if !found {
		klog.Infof("Namespace %s not found in cache, nothing to do", targetNamespace)
		return nil
	}

	processedPolicy, err := m.processExternalRoutePolicy(policy)
	if err != nil {
		return err
	}
	err = m.deletePolicyInNamespace(targetNamespace, policy, processedPolicy, cacheInfo)
	if err != nil {
		return err
	}
	klog.Infof("Deleting policy %s in namespace %s", policy.Name, targetNamespace)
	cacheInfo.Policies = cacheInfo.Policies.Delete(policy.Name)
	return m.updateNamespaceInfoCache(targetNamespace, cacheInfo)
}

func (m *externalPolicyManager) listNamespacesBySelector(selector *metav1.LabelSelector) ([]*v1.Namespace, error) {
	s, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}
	ns, err := m.namespaceLister.List(s)
	if err != nil {
		return nil, err
	}
	return ns, nil

}
