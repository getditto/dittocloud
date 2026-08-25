package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	awsVPCModeDittocloud    = "dittocloud"
	awsVPCModeCAPI          = "capi"
	awsVPCModeExisting      = "existing"
	awsClusterTypeKubeadm   = "kubeadm"
	awsClusterTypeEKS       = "eks"
	awsScopeReferenceLength = 30
	// The managed VPC module always spans the first three availability zones of
	// its Region, so a supplied Elastic IP set has to match that exactly.
	awsManagedVPCAvailabilityZones = 3
)

// Blocks inside 100.64.0.0/10 that Valet clusters already use for in-cluster
// addressing, so a VPC secondary CIDR there would overlap cluster-internal
// traffic. 100.66.0.0/16 is the kubeadm cluster pod and Service range; the
// 100.80 and 100.81 blocks are the pod and Service CIDRs every self-managed AWS
// cluster is built with (cloud-infra-apps apps/valet-cluster-k8s-aws).
var awsReservedClusterSecondaryCIDRs = []string{
	"100.66.0.0/16",
	"100.80.0.0/16",
	"100.81.0.0/16",
}

var (
	awsScopeReferencePattern  = regexp.MustCompile(`^dsc-[0-7][0-9a-hjkmnp-tv-z]{25}$`)
	awsClusterNamePattern     = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
	awsRegionPattern          = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)
	awsVPCIDPattern           = regexp.MustCompile(`^vpc-(?:[0-9a-f]{8}|[0-9a-f]{17})$`)
	awsEIPAllocationIDPattern = regexp.MustCompile(`^eipalloc-(?:[0-9a-f]{8}|[0-9a-f]{17})$`)
)

// AWSDeploymentScopes is the desired set of Dittocloud deployments in one AWS
// account. The map key is the immutable scope reference shared with downstream
// services; it is distinct from the optional Kubernetes cluster name.
type AWSDeploymentScopes map[string]AWSDeploymentScope

type AWSDeploymentScope struct {
	Default               bool        `yaml:"default,omitempty" json:"default"`
	ClusterName           string      `yaml:"clusterName,omitempty" json:"cluster_name,omitempty"`
	ClusterType           string      `yaml:"clusterType,omitempty" json:"cluster_type"`
	Region                string      `yaml:"region" json:"region"`
	ScopeTagPolicyVersion int         `yaml:"scopeTagPolicyVersion,omitempty" json:"scope_tag_policy_version"`
	VPC                   AWSScopeVPC `yaml:"vpc" json:"vpc"`
}

type AWSScopeVPC struct {
	Mode string `yaml:"mode" json:"mode"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// CIDR is the DMZ block: load balancers, NAT gateways, and explicitly placed
	// EC2 only. SecondaryCIDR carries every workload tier and must be unique per
	// VPC, because AWS rejects a peering connection when any associated CIDR
	// overlaps, secondary blocks included.
	CIDR                       string   `yaml:"cidr,omitempty" json:"cidr,omitempty"`
	SecondaryCIDR              string   `yaml:"secondaryCidr,omitempty" json:"secondary_cidr,omitempty"`
	PublicSubnetNetmask        int      `yaml:"publicSubnetNetmask,omitempty" json:"public_subnet_netmask,omitempty"`
	PrivateSubnetNetmask       int      `yaml:"privateSubnetNetmask,omitempty" json:"private_subnet_netmask,omitempty"`
	ID                         string   `yaml:"id,omitempty" json:"id,omitempty"`
	NATGatewayName             string   `yaml:"natGatewayName,omitempty" json:"nat_gateway_name,omitempty"`
	NATGatewayEIPAllocationIDs []string `yaml:"natGatewayEipAllocationIds,omitempty" json:"nat_gateway_eip_allocation_ids,omitempty"`
}

// Terraform applies these defaults when the scopes file leaves the sizing out.
// The CLI has to agree with them so a recovered file round-trips unchanged.
const (
	awsDefaultPublicSubnetNetmask  = 24
	awsDefaultPrivateSubnetNetmask = 23

	// Sizing of a VPC provisioned before the DMZ split. A recovered scopes file
	// has to pin these, or the next apply renumbers live subnets.
	awsLegacyPublicSubnetNetmask  = 22
	awsLegacyPrivateSubnetNetmask = 18
)

func loadAWSDeploymentScopes(path string) (AWSDeploymentScopes, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read AWS scopes file %q: %w", path, err)
	}
	return decodeAWSDeploymentScopes(content, path)
}

func decodeAWSDeploymentScopes(content []byte, path string) (AWSDeploymentScopes, error) {
	var scopes AWSDeploymentScopes
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&scopes); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("invalid AWS scopes file %q: at least one deployment scope is required", path)
		}
		return nil, fmt.Errorf("unable to decode AWS scopes file %q: %w", path, err)
	}

	var trailingDocument any
	if err := decoder.Decode(&trailingDocument); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("unable to decode AWS scopes file %q: %w", path, err)
		}
		return nil, fmt.Errorf("AWS scopes file %q must contain exactly one YAML document", path)
	}

	for scopeRef, scope := range scopes {
		if scope.ClusterType == "" {
			scope.ClusterType = awsClusterTypeKubeadm
		}
		scopes[scopeRef] = scope.withManagedVPCDefaults()
	}

	if err := scopes.Validate(); err != nil {
		return nil, fmt.Errorf("invalid AWS scopes file %q: %w", path, err)
	}
	return scopes, nil
}

func (scopes AWSDeploymentScopes) Validate() error {
	if len(scopes) == 0 {
		return fmt.Errorf("at least one deployment scope is required")
	}

	scopeRefs := make([]string, 0, len(scopes))
	for scopeRef := range scopes {
		scopeRefs = append(scopeRefs, scopeRef)
	}
	sort.Strings(scopeRefs)

	defaultScopeCount := 0
	for _, scopeRef := range scopeRefs {
		scope := scopes[scopeRef]
		if !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("scope reference %q must be exactly %d characters in generated dsc-<lowercase-crockford-ulid> form", scopeRef, awsScopeReferenceLength)
		}
		if scope.Default {
			defaultScopeCount++
		}
		if err := validateAWSDeploymentScopeFields(scopeRef, scope); err != nil {
			return err
		}
	}

	if defaultScopeCount != 1 {
		return fmt.Errorf("exactly one deployment scope must set default: true; found %d", defaultScopeCount)
	}
	return validateAWSGeneratedScopeNames(scopes)
}

func validateAWSDeploymentScopeFields(scopeRef string, scope AWSDeploymentScope) error {
	if scope.ClusterType != awsClusterTypeKubeadm && scope.ClusterType != awsClusterTypeEKS {
		return fmt.Errorf("scope %q clusterType must be %q or %q", scopeRef, awsClusterTypeKubeadm, awsClusterTypeEKS)
	}
	if scope.ScopeTagPolicyVersion != 0 && scope.ScopeTagPolicyVersion != 1 {
		return fmt.Errorf("scope %q scopeTagPolicyVersion must be 0 or 1", scopeRef)
	}
	if scope.ScopeTagPolicyVersion == 1 && scope.ClusterName == "" {
		return fmt.Errorf("scope %q scopeTagPolicyVersion 1 requires one exact clusterName", scopeRef)
	}
	if scope.ClusterName != "" {
		if len(scope.ClusterName) > 63 || !awsClusterNamePattern.MatchString(scope.ClusterName) {
			return fmt.Errorf("scope %q clusterName %q must be a lowercase DNS label with at most 63 characters", scopeRef, scope.ClusterName)
		}
	}
	if !awsRegionPattern.MatchString(scope.Region) {
		return fmt.Errorf("scope %q region %q is not a valid AWS region name", scopeRef, scope.Region)
	}
	return validateAWSScopeVPC(scopeRef, scope.VPC)
}

func validateAWSScopeVPC(scopeRef string, vpc AWSScopeVPC) error {
	switch vpc.Mode {
	case awsVPCModeDittocloud:
		if strings.TrimSpace(vpc.Name) == "" {
			return fmt.Errorf("scope %q requires vpc.name when vpc.mode is %q", scopeRef, awsVPCModeDittocloud)
		}
		prefix, err := netip.ParsePrefix(vpc.CIDR)
		if err != nil || !prefix.Addr().Is4() {
			return fmt.Errorf("scope %q vpc.cidr %q must be a valid IPv4 CIDR", scopeRef, vpc.CIDR)
		}
		if vpc.ID != "" {
			return fmt.Errorf("scope %q cannot set vpc.id when vpc.mode is %q", scopeRef, awsVPCModeDittocloud)
		}
		if vpc.NATGatewayName != "" && (strings.TrimSpace(vpc.NATGatewayName) != vpc.NATGatewayName || len(vpc.NATGatewayName) > 256) {
			return fmt.Errorf("scope %q vpc.natGatewayName must contain 1 to 256 non-whitespace-bounded characters", scopeRef)
		}
		if err := validateAWSScopeSecondaryCIDR(scopeRef, vpc.SecondaryCIDR); err != nil {
			return err
		}
		if err := validateAWSScopeSubnetNetmasks(scopeRef, vpc, prefix.Bits()); err != nil {
			return err
		}
		if err := validateAWSScopeNATEIPAllocationIDs(scopeRef, vpc.NATGatewayEIPAllocationIDs); err != nil {
			return err
		}
	case awsVPCModeCAPI:
		if err := rejectAWSManagedVPCFields(scopeRef, vpc, awsVPCModeCAPI); err != nil {
			return err
		}
		if vpc.ID != "" && !awsVPCIDPattern.MatchString(vpc.ID) {
			return fmt.Errorf("scope %q vpc.id %q must be a valid VPC ID", scopeRef, vpc.ID)
		}
	case awsVPCModeExisting:
		if !awsVPCIDPattern.MatchString(vpc.ID) {
			return fmt.Errorf("scope %q requires a valid vpc.id when vpc.mode is %q", scopeRef, awsVPCModeExisting)
		}
		if err := rejectAWSManagedVPCFields(scopeRef, vpc, awsVPCModeExisting); err != nil {
			return err
		}
	default:
		return fmt.Errorf("scope %q vpc.mode must be one of %q, %q, or %q", scopeRef, awsVPCModeDittocloud, awsVPCModeCAPI, awsVPCModeExisting)
	}
	return nil
}

// withManagedVPCDefaults fills in the subnet sizing Terraform would apply to a
// Dittocloud-managed VPC. The CLI has to hold the effective values, not the
// written ones, because it compares its own view of a scope against the
// configuration snapshot Terraform plans.
func (scope AWSDeploymentScope) withManagedVPCDefaults() AWSDeploymentScope {
	if scope.VPC.Mode != awsVPCModeDittocloud {
		return scope
	}
	if scope.VPC.PublicSubnetNetmask == 0 {
		scope.VPC.PublicSubnetNetmask = awsDefaultPublicSubnetNetmask
	}
	if scope.VPC.PrivateSubnetNetmask == 0 {
		scope.VPC.PrivateSubnetNetmask = awsDefaultPrivateSubnetNetmask
	}
	return scope
}

func rejectAWSManagedVPCFields(scopeRef string, vpc AWSScopeVPC, mode string) error {
	if vpc.Name != "" || vpc.CIDR != "" || vpc.NATGatewayName != "" {
		return fmt.Errorf("scope %q cannot set vpc.name, vpc.cidr, or vpc.natGatewayName when vpc.mode is %q", scopeRef, mode)
	}
	if vpc.SecondaryCIDR != "" || vpc.PublicSubnetNetmask != 0 || vpc.PrivateSubnetNetmask != 0 {
		return fmt.Errorf(
			"scope %q cannot set vpc.secondaryCidr, vpc.publicSubnetNetmask, or vpc.privateSubnetNetmask when vpc.mode is %q",
			scopeRef,
			mode,
		)
	}
	if len(vpc.NATGatewayEIPAllocationIDs) > 0 {
		return fmt.Errorf("scope %q cannot set vpc.natGatewayEipAllocationIds when vpc.mode is %q", scopeRef, mode)
	}
	return nil
}

// The workload block comes out of the shared 100.64.0.0/10 pool. 100.66.0.0/16
// is excluded because Valet clusters already use it for in-cluster pod and
// Service addressing.
func validateAWSScopeSecondaryCIDR(scopeRef string, secondaryCIDR string) error {
	if secondaryCIDR == "" {
		return nil
	}
	prefix, err := netip.ParsePrefix(secondaryCIDR)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 16 || prefix != prefix.Masked() {
		return fmt.Errorf("scope %q vpc.secondaryCidr %q must be a /16 IPv4 CIDR", scopeRef, secondaryCIDR)
	}
	if !netip.MustParsePrefix("100.64.0.0/10").Contains(prefix.Addr()) {
		return fmt.Errorf("scope %q vpc.secondaryCidr %q must fall inside 100.64.0.0/10", scopeRef, secondaryCIDR)
	}
	if slices.Contains(awsReservedClusterSecondaryCIDRs, secondaryCIDR) {
		return fmt.Errorf(
			"scope %q vpc.secondaryCidr %q is reserved for in-cluster pod and Service addressing",
			scopeRef,
			secondaryCIDR,
		)
	}
	return nil
}

// A load-balancer subnet needs at least 8 free addresses per AZ and scales its
// ENI count under load, and each tier has to fit inside the primary block: the
// public tier owns its first quarter and the private tier starts after that.
func validateAWSScopeSubnetNetmasks(scopeRef string, vpc AWSScopeVPC, primaryPrefix int) error {
	tiers := []struct {
		field      string
		netmask    int
		minimumGap int
	}{
		{field: "publicSubnetNetmask", netmask: vpc.PublicSubnetNetmask, minimumGap: 4},
		{field: "privateSubnetNetmask", netmask: vpc.PrivateSubnetNetmask, minimumGap: 2},
	}
	for _, tier := range tiers {
		if tier.netmask > 24 {
			return fmt.Errorf(
				"scope %q vpc.%s must be 24 or lower so each load-balancer subnet is at least a /24",
				scopeRef,
				tier.field,
			)
		}
		if tier.netmask < primaryPrefix+tier.minimumGap {
			return fmt.Errorf(
				"scope %q vpc.%s must be at least %d bits longer than the vpc.cidr prefix /%d so three subnets fit",
				scopeRef,
				tier.field,
				tier.minimumGap,
				primaryPrefix,
			)
		}
	}
	return nil
}

func validateAWSScopeNATEIPAllocationIDs(scopeRef string, allocationIDs []string) error {
	if len(allocationIDs) == 0 {
		return nil
	}
	if len(allocationIDs) != awsManagedVPCAvailabilityZones {
		return fmt.Errorf(
			"scope %q vpc.natGatewayEipAllocationIds must contain exactly %d entries, one per availability zone",
			scopeRef,
			awsManagedVPCAvailabilityZones,
		)
	}
	for _, allocationID := range allocationIDs {
		if !awsEIPAllocationIDPattern.MatchString(allocationID) {
			return fmt.Errorf("scope %q vpc.natGatewayEipAllocationIds entry %q is not a valid Elastic IP allocation ID", scopeRef, allocationID)
		}
	}
	return nil
}

func marshalAWSDeploymentScopes(scopes AWSDeploymentScopes) (string, error) {
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal AWS deployment scopes: %w", err)
	}
	return string(encoded), nil
}
