/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	v1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
)

// Layer3SubnetApplyConfiguration represents an declarative configuration of the Layer3Subnet type for use
// with apply.
type Layer3SubnetApplyConfiguration struct {
	CIDR       *v1.CIDR `json:"cidr,omitempty"`
	HostSubnet *int32   `json:"hostSubnet,omitempty"`
}

// Layer3SubnetApplyConfiguration constructs an declarative configuration of the Layer3Subnet type for use with
// apply.
func Layer3Subnet() *Layer3SubnetApplyConfiguration {
	return &Layer3SubnetApplyConfiguration{}
}

// WithCIDR sets the CIDR field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CIDR field is set to the value of the last call.
func (b *Layer3SubnetApplyConfiguration) WithCIDR(value v1.CIDR) *Layer3SubnetApplyConfiguration {
	b.CIDR = &value
	return b
}

// WithHostSubnet sets the HostSubnet field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HostSubnet field is set to the value of the last call.
func (b *Layer3SubnetApplyConfiguration) WithHostSubnet(value int32) *Layer3SubnetApplyConfiguration {
	b.HostSubnet = &value
	return b
}