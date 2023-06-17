package apbroute

import (
	"encoding/json"
	"fmt"
	"net"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	utilnet "k8s.io/utils/net"
)

func (m *externalPolicyManager) syncPod(podName ktypes.NamespacedName, podLister corev1listers.PodLister, namespaceLister corev1listers.NamespaceLister, routeQueue, namespaceQueue workqueue.RateLimitingInterface) error {

	_, err := podLister.Pods(podName.Namespace).Get(podName.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	policies, pErr := m.listPoliciesInNamespacesUsingPodGateway(podName)
	if pErr != nil {
		return pErr
	}
	klog.Infof("Processing pod %s with matching policies %+v", podName, policies)
	return m.notifyRouteController(policies, routeQueue)
}

func getExGwPodIPs(gatewayPod *v1.Pod, networkName string) (sets.Set[string], error) {
	if networkName != "" {
		return getMultusIPsFromNetworkName(gatewayPod, networkName)
	}
	if gatewayPod.Spec.HostNetwork {
		return getPodIPs(gatewayPod), nil
	}
	return nil, fmt.Errorf("ignoring pod %s as an external gateway candidate. Invalid combination "+
		"of host network: %t and routing-network annotation: %s", gatewayPod.Name, gatewayPod.Spec.HostNetwork,
		networkName)
}

func getPodIPs(pod *v1.Pod) sets.Set[string] {
	foundGws := sets.New[string]()
	for _, podIP := range pod.Status.PodIPs {
		ip := utilnet.ParseIPSloppy(podIP.IP)
		if ip != nil {
			foundGws.Insert(ip.String())
		}
	}
	return foundGws
}

func getMultusIPsFromNetworkName(pod *v1.Pod, networkName string) (sets.Set[string], error) {
	foundGws := sets.New[string]()
	var multusNetworks []nettypes.NetworkStatus
	err := json.Unmarshal([]byte(pod.ObjectMeta.Annotations[nettypes.NetworkStatusAnnot]), &multusNetworks)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshall annotation on pod %s k8s.v1.cni.cncf.io/network-status '%s': %v",
			pod.Name, pod.ObjectMeta.Annotations[nettypes.NetworkStatusAnnot], err)
	}
	for _, multusNetwork := range multusNetworks {
		if multusNetwork.Name == networkName {
			for _, gwIP := range multusNetwork.IPs {
				ip := net.ParseIP(gwIP)
				if ip != nil {
					foundGws.Insert(ip.String())
				}
			}
			return foundGws, nil
		}
	}
	return nil, fmt.Errorf("unable to find multus network %s in pod %s/%s", networkName, pod.Namespace, pod.Name)
}

func (m *externalPolicyManager) listPoliciesUsingPodGateway(key ktypes.NamespacedName) ([]string, error) {

	ret := make([]string, 0)
	policyNames := m.routePolicySyncCache.GetKeys()
	for _, pName := range policyNames {
		policy, found, markedForDeletion := m.getRoutePolicyFromCache(pName)
		if !found {
			klog.Infof("Policy %s not found", pName)
			continue
		}
		if markedForDeletion {
			klog.Infof("Policy %s has been marked for deletion, skipping", pName)
		}
		pp, err := m.processExternalRoutePolicy(policy)
		if err != nil {
			return nil, err
		}
		if _, found := pp.dynamicGateways[key]; found {
			ret = append(ret, policy.Name)
		}
	}
	return ret, nil
}

func (m *externalPolicyManager) listPoliciesInNamespacesUsingPodGateway(key ktypes.NamespacedName) ([]string, error) {
	policies := sets.New[string]()
	// iterate through all current namespaces that contain the pod. This is needed in case the pod is deleted from an existing namespace, in which case
	// if we iterated applying the namespace selector in the policies, we would miss the fact that a pod was part of a namespace that is no longer
	// and we'd miss updating that namespace and removing the pod through the reconciliation of the policy in that namespace.
	nsList := m.listNamespaceInfoCache()
	for _, namespaceName := range nsList {
		cacheInfo, found := m.getNamespaceInfoFromCache(namespaceName)
		if !found {
			continue
		}
		policies = policies.Union(cacheInfo.Policies)
	}
	// list all namespaces that match the policy, for those new namespaces where the pod now applies
	p, err := m.listPoliciesUsingPodGateway(key)
	if err != nil {
		return nil, err
	}
	for _, policy := range p {
		policies.Insert(policy)
	}
	return policies.UnsortedList(), nil
}
