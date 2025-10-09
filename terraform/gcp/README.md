# IAM policies

## constraining IAM policies to prevent privilege escelation and lateral movement

Using project based IAM policies ensures that a IAM principal doesn't have access to things outside of that project.
If a principal is added at a higher level than the project (folder, org), we should use PAB to make sure to scope privilege down to particular project(s)

If a project can have non-Ditto resources, we can further scope policies to target Ditto resources by [tag based and other resource attribute conditions](https://cloud.google.com/iam/docs/conditions-attribute-reference).

## IAM for Management Plane components

### CAPG

- Management cluster is trusted by the Ditto owned ops project by way of workload identity federation
- The ops project contains the WIF pool and provider with the management cluster's service account issuer oidc configuration.
- This allows the management cluster to impersonate service accounts in the ops project.
- This service account is then added to the client project with permissions required by CAPG.

### Crossplane

- Service account from the Ditto owned ops project is directly added to the client project with appropriate permissions, same as CAPG.
- Crossplane is allowed to grant new workloads access to the client project's resources, i.e manage the project's IAM policies
- A strech goal is to restrict IAM management capabilities of crossplane by only allowing it to create IAM policies for the workload cluster WIF identities.

## IAM for Workload Cluster

- Workload cluster's control plane and dataplane VMs shall not rely on default GCE service account, and will use service accounts will appropriate miminal roles.
