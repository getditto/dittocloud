package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	awsVPCModeDittocloud      = "dittocloud"
	awsVPCModeCAPI            = "capi"
	awsVPCModeExisting        = "existing"
	awsClusterTypeKubeadm     = "kubeadm"
	awsClusterTypeEKS         = "eks"
	awsScopeReferenceMaxLen   = 32
	awsScopeDisplayNameMaxLen = 100
)

var (
	awsScopeReferencePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	awsClusterNamePattern    = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
	awsRegionPattern         = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)
	awsVPCIDPattern          = regexp.MustCompile(`^vpc-(?:[0-9a-f]{8}|[0-9a-f]{17})$`)
)

// AWSDeploymentScopes is the desired set of Dittocloud deployments in one AWS
// account. The map key is the immutable scope reference shared with downstream
// services; it is not a mutable display name or a Kubernetes cluster name.
type AWSDeploymentScopes map[string]AWSDeploymentScope

type AWSDeploymentScope struct {
	Name        string      `yaml:"name,omitempty" json:"name,omitempty"`
	Default     bool        `yaml:"default,omitempty" json:"default"`
	ClusterName string      `yaml:"clusterName,omitempty" json:"cluster_name,omitempty"`
	ClusterType string      `yaml:"clusterType,omitempty" json:"cluster_type"`
	Region      string      `yaml:"region" json:"region"`
	VPC         AWSScopeVPC `yaml:"vpc" json:"vpc"`
}

type AWSScopeVPC struct {
	Mode string `yaml:"mode" json:"mode"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	CIDR string `yaml:"cidr,omitempty" json:"cidr,omitempty"`
	ID   string `yaml:"id,omitempty" json:"id,omitempty"`
}

func loadAWSDeploymentScopes(path string) (AWSDeploymentScopes, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read AWS scopes file %q: %w", path, err)
	}

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
			scopes[scopeRef] = scope
		}
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
	clusterNames := make(map[string]string, len(scopes))
	for _, scopeRef := range scopeRefs {
		scope := scopes[scopeRef]
		if !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("scope reference %q must contain 1-%d lowercase letters, digits, or internal hyphens and must begin and end with a letter or digit", scopeRef, awsScopeReferenceMaxLen)
		}
		if err := validateAWSScopeDisplayName(scopeRef, scope.Name); err != nil {
			return err
		}
		if scope.Default {
			defaultScopeCount++
		}
		if scope.ClusterType != awsClusterTypeKubeadm && scope.ClusterType != awsClusterTypeEKS {
			return fmt.Errorf("scope %q clusterType must be %q or %q", scopeRef, awsClusterTypeKubeadm, awsClusterTypeEKS)
		}
		if scope.ClusterName != "" {
			if len(scope.ClusterName) > 63 || !awsClusterNamePattern.MatchString(scope.ClusterName) {
				return fmt.Errorf("scope %q clusterName %q must be a lowercase DNS label with at most 63 characters", scopeRef, scope.ClusterName)
			}
			if previousScope, exists := clusterNames[scope.ClusterName]; exists {
				return fmt.Errorf("scopes %q and %q use the same clusterName %q", previousScope, scopeRef, scope.ClusterName)
			}
			clusterNames[scope.ClusterName] = scopeRef
		}
		if !awsRegionPattern.MatchString(scope.Region) {
			return fmt.Errorf("scope %q region %q is not a valid AWS region name", scopeRef, scope.Region)
		}
		if err := validateAWSScopeVPC(scopeRef, scope.VPC); err != nil {
			return err
		}
	}

	if defaultScopeCount != 1 {
		return fmt.Errorf("exactly one deployment scope must set default: true; found %d", defaultScopeCount)
	}
	return nil
}

func validateAWSScopeDisplayName(scopeRef, name string) error {
	if name == "" {
		return nil
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("scope %q name must not have leading or trailing whitespace", scopeRef)
	}
	if utf8.RuneCountInString(name) > awsScopeDisplayNameMaxLen {
		return fmt.Errorf("scope %q name must not exceed %d characters", scopeRef, awsScopeDisplayNameMaxLen)
	}
	for _, value := range name {
		if unicode.IsControl(value) {
			return fmt.Errorf("scope %q name must not contain control characters", scopeRef)
		}
	}
	return nil
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
	case awsVPCModeCAPI:
		if vpc.Name != "" || vpc.CIDR != "" {
			return fmt.Errorf("scope %q cannot set vpc.name or vpc.cidr when vpc.mode is %q", scopeRef, awsVPCModeCAPI)
		}
		if vpc.ID != "" && !awsVPCIDPattern.MatchString(vpc.ID) {
			return fmt.Errorf("scope %q vpc.id %q must be a valid VPC ID", scopeRef, vpc.ID)
		}
	case awsVPCModeExisting:
		if !awsVPCIDPattern.MatchString(vpc.ID) {
			return fmt.Errorf("scope %q requires a valid vpc.id when vpc.mode is %q", scopeRef, awsVPCModeExisting)
		}
		if vpc.Name != "" || vpc.CIDR != "" {
			return fmt.Errorf("scope %q cannot set vpc.name or vpc.cidr when vpc.mode is %q", scopeRef, awsVPCModeExisting)
		}
	default:
		return fmt.Errorf("scope %q vpc.mode must be one of %q, %q, or %q", scopeRef, awsVPCModeDittocloud, awsVPCModeCAPI, awsVPCModeExisting)
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
