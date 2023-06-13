package apbroute

import (
	"fmt"

	adminpolicybasedrouteapi "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/adminpolicybasedroute/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

func (m *externalPolicyManager) syncNamespace(namespace *v1.Namespace, namespaceLister corev1listers.NamespaceLister, routeQueue workqueue.RateLimitingInterface) error {
	_, err := namespaceLister.Get(namespace.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if apierrors.IsNotFound(err) || !namespace.DeletionTimestamp.IsZero() {
		// DELETE use case
		klog.Infof("Deleting namespace reference %s", namespace.Name)
		policies, err := m.processDeleteNamespace(namespace.Name)
		if err != nil {
			return err
		}
		// notify of changes to the policy controller
		if len(policies) > 0 {
			for _, policy := range policies {
				klog.Infof("Queueing policy %s", policy.Name)
				routeQueue.Add(policy)
			}
		}
		return nil
	}

	cacheInfo, found := m.getNamespaceInfoFromCache(namespace.Name)
	matches, err := m.getPoliciesForNamespace(namespace.Name, cacheInfo)
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

	if !found {
		// ADD use case
		klog.V(2).InfoS("Adding namespace %s", namespace.Name)
		cacheInfo = m.newNamespaceInfoInCache(namespace.Name)
	} else {
		// UPDATE use case
		klog.V(2).InfoS("Updating namespace %s", namespace.Name)
		cacheInfo.Policies = matches
	}
	// notify of changes to the policy controller
	err = m.notifyRouteController(matches, routeQueue)
	if err != nil {
		return err
	}
	// persist cacheInfo
	return m.unlockNamespaceInfoCache(namespace.Name, cacheInfo)
}

func (m *externalPolicyManager) notifyRouteController(policies sets.Set[string], routeQueue workqueue.RateLimitingInterface) error {
	var err error
	for policyName := range policies {
		policy, found, markedForDeletion := m.getRoutePolicyFromCache(policyName)
		if markedForDeletion {
			klog.Infof("Skipping route policy %s as it has been marked for deletion", policyName)
			continue
		}
		if !found {
			_, err = m.routeLister.Get(policyName)
			// Check if the route policy exists in the API server, if it exists it means the policy has not yet been processed and we should not ignore it
			// On the other hand, consider the policy as deleted and ignore it
			if apierrors.IsNotFound(err) {
				klog.Infof("Route policy %s not found in the cache or in the API server", policyName)
				continue
			}
			return fmt.Errorf("route policy %s not found in cache but exists in API server", policyName)
		}
		klog.V(2).InfoS("Queueing route policy %s", policyName)
		routeQueue.Add(policy)
	}
	return nil
}

// processDeleteNamespace processes a delete namespace event by ensuring that no pod is still running in that namespace before deleting the namespace info cache. It also
// marks the namespace for deletion so that if a new pod event appears targeting the namespace, the operation will be rejected.
func (m *externalPolicyManager) processDeleteNamespace(namespaceName string) ([]*adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute, error) {
	nsInfo, found := m.getAndMarkForDeleteNamespaceInfoFromCache(namespaceName)
	if !found {
		// namespace is not a recipient for policies
		return nil, nil
	}
	podsInNs, err := m.podLister.Pods(namespaceName).List(labels.Everything())
	if err != nil {
		return nil, err
	}
	if len(podsInNs) != 0 {
		klog.Infof("Attempting to delete namespace %s with resources still attached to it. Retrying...", namespaceName)
		return nil, fmt.Errorf("unable to delete namespace %s with resources still attached to it", namespaceName)
	}
	m.deleteNamespaceInfoInCache(namespaceName, nsInfo)
	// capture all policies targeting the namespace for notification of the change
	policies := make([]*adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute, 0)
	for policyName := range nsInfo.Policies {
		policy, found, markedForDeletion := m.getRoutePolicyFromCache(policyName)
		if !found {
			klog.Infof("failed to find external route policy %s in cache", policyName)
			continue
		}
		if markedForDeletion {
			klog.Infof("Skipping route policy %s as it has been marked for deletion", policyName)
			continue
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

func (m *externalPolicyManager) applyPolicyToNamespace(namespaceName string, policy *adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute, cacheInfo *namespaceInfo) error {

	processedPolicy, err := m.processExternalRoutePolicy(policy)
	if err != nil {
		return err
	}
	err = m.applyProcessedPolicyToNamespace(namespaceName, policy.Name, processedPolicy, cacheInfo)
	if err != nil {
		return err
	}
	return nil
}

func (m *externalPolicyManager) removePolicyFromNamespace(targetNamespace string, policy *adminpolicybasedrouteapi.AdminPolicyBasedExternalRoute, cacheInfo *namespaceInfo) error {

	processedPolicy, err := m.processExternalRoutePolicy(policy)
	if err != nil {
		return err
	}
	err = m.deletePolicyInNamespace(targetNamespace, policy.Name, processedPolicy, cacheInfo)
	if err != nil {
		return err
	}
	klog.Infof("Deleting policy %s in namespace %s", policy.Name, targetNamespace)
	cacheInfo.Policies = cacheInfo.Policies.Delete(policy.Name)
	return nil
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
