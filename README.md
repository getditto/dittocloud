# Ditto Cloud

A command-line tool to bootstrap your cloud infrastructure with the necessary resources and configurations required for Ditto deployment. This tool prepares your AWS or GCP environment with the proper networking, IAM roles, and security settings needed to deploy and run Ditto services.

## Overview

The Ditto Cloud tool automates the setup of cloud infrastructure components that are prerequisites for Ditto deployment. It creates the foundation layer including VPCs, IAM roles, service accounts, and security configurations tailored for Ditto's requirements.

### What it creates:

**AWS:**
- VPC with proper networking configuration
- Cross-account IAM roles for Ditto services
- Security groups and network ACLs
- IAM permissions for cluster management

**GCP:**
- VPC networks with subnets and secondary ranges for Kubernetes
- Service accounts with appropriate IAM bindings
- Project-level IAM roles and custom roles
- Resource tagging for access control
- Firewall rules for secure communication
- Support for CAPG (Cluster API Provider GCP) and Crossplane

## Installation

### Download Pre-built Binaries

Download the latest release from the [releases page](https://github.com/getditto/dittocloud/releases):

```bash
# For macOS (Apple Silicon)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Darwin_arm64.tar.gz
tar -xzf dittocloud_Darwin_arm64.tar.gz

# For macOS (Intel)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Darwin_x86_64.tar.gz
tar -xzf dittocloud_Darwin_x86_64.tar.gz

# For Linux (x86_64)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Linux_x86_64.tar.gz
tar -xzf dittocloud_Linux_x86_64.tar.gz
```

### Build from Source

```bash
git clone https://github.com/getditto/dittocloud.git
cd dittocloud
go build -o dittocloud ./cmd/dittocloud
```

## Prerequisites

### AWS Requirements
- AWS CLI configured with appropriate credentials
- An AWS profile with sufficient permissions to create:
  - VPCs, subnets, and networking resources
  - IAM roles and policies
  - Security groups
- A region with at least 3 Availability Zones

### GCP Requirements
- Google Cloud CLI (`gcloud`) installed and authenticated
- A GCP project with billing enabled
- Sufficient permissions to create:
  - VPC networks and subnets
  - Service accounts and IAM bindings
  - Custom IAM roles
  - Firewall rules
  - Project-level tags

## Usage

### Bootstrap AWS

```bash
# Interactive mode - the tool will prompt for required values
dittocloud bootstrap aws

# With command-line flags
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --vpc-name ditto-vpc \
  --vpc-cidr 10.0.0.0/16
```

### Bootstrap GCP

```bash
# Interactive mode - the tool will prompt for required values
dittocloud bootstrap gcp

# With command-line flags
dittocloud bootstrap gcp \
  --project-id my-project-id \
  --region us-central1 \
  --vpc-name ditto-vpc
```

## Output

After successful execution, the tool displays important resource information that you'll need for Ditto deployment, including:

**For AWS:**
- AWS Account ID and region
- VPC configuration details
- IAM role information

**For GCP:**
- Project ID and available zones
- VPC network and subnet details (including secondary ranges for Kubernetes)
- Service account details for control plane and worker nodes
- Custom IAM role information for CAPG, Crossplane, and Velero
- Resource tagging information for access control

The outputs are displayed in the console in JSON format for easy consumption by other tools or scripts.

## State Management

The tool uses Terraform state files to track the infrastructure it creates. By default, it looks for and creates a `terraform.tfstate` file in your current directory. You can specify a custom location using the `--state` flag.

**Important:** Keep your state file safe and backed up, as it's required for any future updates or destruction of the created resources.

## Architecture

For detailed information about the infrastructure components created by this tool, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Development

### Building

```bash
# Build for current platform
go build -o dittocloud ./cmd/dittocloud

# Build for all platforms (requires GoReleaser)
goreleaser release --snapshot --clean
```

### Testing

```bash
go test ./...
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Support

For questions, issues, or support:
- Open an issue on [GitHub](https://github.com/getditto/dittocloud/issues)
- Contact the Ditto team through your support channels

## Security

If you discover a security vulnerability, please report it responsibly by contacting the Ditto security team directly rather than opening a public issue.
