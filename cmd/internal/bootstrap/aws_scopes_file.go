package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type scopesFileLockMetadata struct {
	PID                     int       `json:"pid"`
	Hostname                string    `json:"hostname"`
	StartedAt               time.Time `json:"startedAt"`
	CanonicalScopesFilePath string    `json:"canonicalScopesFilePath"`
	Operation               string    `json:"operation"`
}

type scopesFileLock struct {
	canonicalPath string
	lockPath      string
	file          *os.File

	mu       sync.Mutex
	released bool
}

type awsDeploymentScopesDocument struct {
	path        string
	document    *yaml.Node
	scopes      AWSDeploymentScopes
	empty       bool
	permissions os.FileMode
}

type scopesFilePersistenceError struct {
	DestinationPath string
	RecoveryPath    string
	Err             error
}

func (err *scopesFilePersistenceError) Error() string {
	if err.RecoveryPath != "" {
		return fmt.Sprintf(
			"unable to atomically persist AWS scopes file to %q: %v; recovery file retained at %q",
			err.DestinationPath,
			err.Err,
			err.RecoveryPath,
		)
	}
	return fmt.Sprintf("unable to atomically persist AWS scopes file to %q: %v", err.DestinationPath, err.Err)
}

func (err *scopesFilePersistenceError) Unwrap() error {
	return err.Err
}

var (
	replaceScopesFile       = atomicReplaceFile
	syncScopesFileDirectory = syncParentDirectory
)

func canonicalizeScopesFilePath(scopesFilePath string) (string, error) {
	return canonicalizeLocalPath(scopesFilePath, "scopes file")
}

func acquireScopesFileLock(scopesFilePath, operation string) (*scopesFileLock, error) {
	canonicalPath, err := canonicalizeScopesFilePath(scopesFilePath)
	if err != nil {
		return nil, err
	}
	lockPath := canonicalPath + operationLockSuffix
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("unable to open Dittocloud scopes-file lock %q: %w", lockPath, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = lockFile.Close()
		}
	}()

	if err := lockFile.Chmod(0600); err != nil {
		return nil, fmt.Errorf("unable to secure Dittocloud scopes-file lock %q: %w", lockPath, err)
	}
	if err := tryAdvisoryLock(lockFile); err != nil {
		if isAdvisoryLockContention(err) {
			return nil, scopesFileLockContentionError(canonicalPath, lockPath)
		}
		return nil, fmt.Errorf("unable to acquire Dittocloud scopes-file lock %q: %w", lockPath, err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	metadata := scopesFileLockMetadata{
		PID:                     os.Getpid(),
		Hostname:                hostname,
		StartedAt:               time.Now().UTC(),
		CanonicalScopesFilePath: canonicalPath,
		Operation:               operation,
	}
	if err := writeScopesFileLockMetadata(lockFile, metadata); err != nil {
		_ = releaseAdvisoryLock(lockFile)
		return nil, fmt.Errorf("unable to write Dittocloud scopes-file lock metadata to %q: %w", lockPath, err)
	}

	closeOnError = false
	return &scopesFileLock{canonicalPath: canonicalPath, lockPath: lockPath, file: lockFile}, nil
}

func writeScopesFileLockMetadata(lockFile *os.File, metadata scopesFileLockMetadata) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := lockFile.Truncate(0); err != nil {
		return err
	}
	if _, err := lockFile.Seek(0, 0); err != nil {
		return err
	}
	if _, err := lockFile.Write(encoded); err != nil {
		return err
	}
	return lockFile.Sync()
}

func scopesFileLockContentionError(canonicalPath, lockPath string) error {
	content, err := os.ReadFile(lockPath)
	if err == nil {
		var metadata scopesFileLockMetadata
		if json.Unmarshal(content, &metadata) == nil && metadata.PID != 0 {
			return fmt.Errorf(
				"dittocloud scopes-file operation already in progress for %q: pid %d on %s started %s (%s)",
				canonicalPath,
				metadata.PID,
				metadata.Hostname,
				metadata.StartedAt.Format(time.RFC3339),
				metadata.Operation,
			)
		}
	}
	return fmt.Errorf("dittocloud scopes-file operation already in progress for %q; lock owner metadata is unavailable", canonicalPath)
}

func (lock *scopesFileLock) Release() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.released {
		return nil
	}
	lock.released = true
	return errors.Join(releaseAdvisoryLock(lock.file), lock.file.Close())
}

func loadAWSDeploymentScopesDocument(path string) (*awsDeploymentScopesDocument, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newAWSDeploymentScopesDocument(path, 0600), nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to read AWS scopes file %q: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("unable to inspect AWS scopes file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("AWS scopes file %q must be a regular file", path)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return newAWSDeploymentScopesDocument(path, info.Mode().Perm()), nil
	}

	scopes, err := decodeAWSDeploymentScopes(content, path)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("unable to decode AWS scopes document %q: %w", path, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("unable to decode AWS scopes document %q: %w", path, err)
		}
		return nil, fmt.Errorf("AWS scopes file %q must contain exactly one YAML document", path)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("AWS scopes file %q must contain one root mapping", path)
	}
	return &awsDeploymentScopesDocument{
		path:        path,
		document:    &document,
		scopes:      scopes,
		permissions: info.Mode().Perm(),
	}, nil
}

func newAWSDeploymentScopesDocument(path string, permissions os.FileMode) *awsDeploymentScopesDocument {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	return &awsDeploymentScopesDocument{
		path:        path,
		document:    &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}},
		scopes:      AWSDeploymentScopes{},
		empty:       true,
		permissions: permissions,
	}
}

func (document *awsDeploymentScopesDocument) appendScope(scopeRef string, scope AWSDeploymentScope) ([]byte, error) {
	if _, exists := document.scopes[scopeRef]; exists {
		return nil, fmt.Errorf("scope reference %q already exists in AWS scopes file %q", scopeRef, document.path)
	}
	scope = scope.withManagedVPCDefaults()
	document.document.Content[0].Content = append(
		document.document.Content[0].Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: scopeRef},
		awsDeploymentScopeYAMLNode(scope),
	)
	document.scopes[scopeRef] = scope
	return document.encodeValidated()
}

func (document *awsDeploymentScopesDocument) enableScopeTagPolicyVersionOne(
	scopeRef string,
	updatedScope AWSDeploymentScope,
) ([]byte, error) {
	currentScope, exists := document.scopes[scopeRef]
	if !exists {
		return nil, fmt.Errorf("AWS scopes file %q does not contain scope %q", document.path, scopeRef)
	}
	if currentScope.ScopeTagPolicyVersion != 0 {
		return nil, fmt.Errorf("scope %q must use scopeTagPolicyVersion 0 before enablement", scopeRef)
	}
	if updatedScope.ScopeTagPolicyVersion != 1 || updatedScope.ClusterName == "" {
		return nil, fmt.Errorf("scope %q enablement requires scopeTagPolicyVersion 1 and one exact clusterName", scopeRef)
	}
	if err := validateAWSDeploymentScopeFields(scopeRef, updatedScope); err != nil {
		return nil, err
	}

	root := document.document.Content[0]
	var scopeNode *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == scopeRef {
			scopeNode = root.Content[index+1]
			break
		}
	}
	if scopeNode == nil || scopeNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("AWS scopes file %q has no writable mapping for scope %q", document.path, scopeRef)
	}

	setScalarMappingField(scopeNode, "scopeTagPolicyVersion", "1", "!!int", "")
	setScalarMappingField(scopeNode, "clusterName", updatedScope.ClusterName, "!!str", "clusterType")
	document.scopes[scopeRef] = updatedScope
	return document.encodeValidated()
}

func setScalarMappingField(mapping *yaml.Node, name, value, tag, insertBefore string) {
	for index := 0; index < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != name {
			continue
		}
		mapping.Content[index+1].Kind = yaml.ScalarNode
		mapping.Content[index+1].Tag = tag
		mapping.Content[index+1].Value = value
		return
	}

	insertAt := len(mapping.Content)
	if insertBefore != "" {
		for index := 0; index < len(mapping.Content); index += 2 {
			if mapping.Content[index].Value == insertBefore {
				insertAt = index
				break
			}
		}
	}
	field := []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	}
	mapping.Content = append(mapping.Content, nil, nil)
	copy(mapping.Content[insertAt+2:], mapping.Content[insertAt:len(mapping.Content)-2])
	copy(mapping.Content[insertAt:insertAt+2], field)
}

func (document *awsDeploymentScopesDocument) encodeValidated() ([]byte, error) {
	if err := document.scopes.Validate(); err != nil {
		return nil, fmt.Errorf("updated AWS scopes file %q is invalid: %w", document.path, err)
	}

	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(document.document); err != nil {
		return nil, fmt.Errorf("unable to encode updated AWS scopes file %q: %w", document.path, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("unable to finish encoding updated AWS scopes file %q: %w", document.path, err)
	}
	if _, err := decodeAWSDeploymentScopes(encoded.Bytes(), document.path); err != nil {
		return nil, fmt.Errorf("encoded AWS scopes file failed validation: %w", err)
	}
	return encoded.Bytes(), nil
}

func encodeAWSDeploymentScopesDocument(path string, scopes AWSDeploymentScopes) ([]byte, error) {
	if err := scopes.Validate(); err != nil {
		return nil, fmt.Errorf("recovered AWS scopes configuration is invalid: %w", err)
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(scopes) {
		root.Content = append(
			root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: scopeRef},
			awsDeploymentScopeYAMLNode(scopes[scopeRef]),
		)
	}
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}

	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("unable to encode recovered AWS scopes file %q: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("unable to finish encoding recovered AWS scopes file %q: %w", path, err)
	}
	if _, err := decodeAWSDeploymentScopes(encoded.Bytes(), path); err != nil {
		return nil, fmt.Errorf("encoded recovered AWS scopes file failed validation: %w", err)
	}
	return encoded.Bytes(), nil
}

func awsDeploymentScopeYAMLNode(scope AWSDeploymentScope) *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendScalarYAMLField := func(name, value, tag string) {
		root.Content = append(
			root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
		)
	}
	if scope.Default {
		appendScalarYAMLField("default", "true", "!!bool")
	}
	if scope.ClusterName != "" {
		appendScalarYAMLField("clusterName", scope.ClusterName, "!!str")
	}
	appendScalarYAMLField("clusterType", scope.ClusterType, "!!str")
	appendScalarYAMLField("region", scope.Region, "!!str")
	appendScalarYAMLField("scopeTagPolicyVersion", strconv.Itoa(scope.ScopeTagPolicyVersion), "!!int")

	vpc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendVPCField := func(name, value string) {
		vpc.Content = append(
			vpc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
		)
	}
	appendIntVPCField := func(name string, value int) {
		vpc.Content = append(
			vpc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)},
		)
	}
	appendVPCField("mode", scope.VPC.Mode)
	if scope.VPC.Name != "" {
		appendVPCField("name", scope.VPC.Name)
	}
	if scope.VPC.CIDR != "" {
		appendVPCField("cidr", scope.VPC.CIDR)
	}
	if scope.VPC.ID != "" {
		appendVPCField("id", scope.VPC.ID)
	}
	if scope.VPC.NATGatewayName != "" {
		appendVPCField("natGatewayName", scope.VPC.NATGatewayName)
	}
	if scope.VPC.SecondaryCIDR != "" {
		appendVPCField("secondaryCidr", scope.VPC.SecondaryCIDR)
	}
	if scope.VPC.PublicSubnetNetmask != 0 {
		appendIntVPCField("publicSubnetNetmask", scope.VPC.PublicSubnetNetmask)
	}
	if scope.VPC.PrivateSubnetNetmask != 0 {
		appendIntVPCField("privateSubnetNetmask", scope.VPC.PrivateSubnetNetmask)
	}
	if len(scope.VPC.NATGatewayEIPAllocationIDs) > 0 {
		allocations := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, allocationID := range scope.VPC.NATGatewayEIPAllocationIDs {
			allocations.Content = append(
				allocations.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: allocationID},
			)
		}
		vpc.Content = append(
			vpc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "natGatewayEipAllocationIds"},
			allocations,
		)
	}
	root.Content = append(
		root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "vpc"},
		vpc,
	)
	return root
}

func persistAWSDeploymentScopesFile(path string, content []byte, permissions os.FileMode) error {
	canonicalPath, err := canonicalizeScopesFilePath(path)
	if err != nil {
		return &scopesFilePersistenceError{DestinationPath: path, Err: err}
	}
	directory := filepath.Dir(canonicalPath)
	temporaryFile, err := os.CreateTemp(directory, "."+filepath.Base(canonicalPath)+".dittocloud-*")
	if err != nil {
		return &scopesFilePersistenceError{
			DestinationPath: canonicalPath,
			Err:             fmt.Errorf("unable to create sibling scopes file: %w", err),
		}
	}
	temporaryPath := temporaryFile.Name()
	temporaryOpen := true
	closeTemporary := func() error {
		if !temporaryOpen {
			return nil
		}
		temporaryOpen = false
		return temporaryFile.Close()
	}
	failure := func(cause error) error {
		_ = closeTemporary()
		return &scopesFilePersistenceError{
			DestinationPath: canonicalPath,
			RecoveryPath:    temporaryPath,
			Err:             cause,
		}
	}

	if err := temporaryFile.Chmod(permissions.Perm()); err != nil {
		return failure(fmt.Errorf("unable to set sibling scopes-file permissions: %w", err))
	}
	if _, err := temporaryFile.Write(content); err != nil {
		return failure(fmt.Errorf("unable to write sibling scopes file: %w", err))
	}
	if err := temporaryFile.Sync(); err != nil {
		return failure(fmt.Errorf("unable to flush sibling scopes file: %w", err))
	}
	if err := closeTemporary(); err != nil {
		return failure(fmt.Errorf("unable to close sibling scopes file: %w", err))
	}
	if err := replaceScopesFile(temporaryPath, canonicalPath); err != nil {
		return failure(fmt.Errorf("unable to replace destination scopes file: %w", err))
	}
	if err := syncScopesFileDirectory(directory); err != nil {
		return &scopesFilePersistenceError{
			DestinationPath: canonicalPath,
			RecoveryPath:    canonicalPath,
			Err:             fmt.Errorf("scopes file was replaced but the parent directory could not be flushed: %w", err),
		}
	}
	return nil
}
