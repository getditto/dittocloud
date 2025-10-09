# ARCHITECTURE

## Design Brief

> In order to support self-service creation of big peer deployments we need to provide some solution to guide the customer through "bootstrapping" their cloud account with all of the required resources that need to exist for cluster API and our systems to create clusters.

## Top Level

This module implements a flexible cloud infrastructure setup with the following features:

1. **Top-level selection mechanism**: Allows choosing which cloud provider to use
    (AWS, Azure, GCP, etc.) through configuration parameters.

2. **Sensible defaults**: Each provider implementation comes with carefully selected
    default configurations that follow best practices for security and performance.

3. **Variable passing**: All required variables and configuration parameters are properly
    passed through to the appropriate cloud provider-specific module, ensuring
    consistent deployment across different environments.

This architecture enables cloud provider flexibility while maintaining consistent
infrastructure configuration patterns.

## Cloud Provider Configuration

### AWS

This module is responsible for creating AWS roles and VPCs. It provides the necessary infrastructure components to define and deploy secure and isolated network environments within AWS. These components include:

1. **Roles**: IAM roles are created to manage permissions and access control for AWS services.
2. **VPCs**: Virtual Private Clouds are provisioned to ensure isolated and secure networking environments.

The module ensures that these resources are configured following AWS best practices for security and scalability.
