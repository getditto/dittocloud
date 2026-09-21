terraform {
  # This module's own floor: the subnet netmask variables validate against
  # vpc_cidr, which is cross-variable validation and requires 1.9. The root
  # module declares a higher floor for the configuration as a whole.
  required_version = ">= 1.9"
}
