#!/usr/bin/env bash

set -euo pipefail

readonly scope_ref="dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
readonly module_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly result_file="$(mktemp)"

trap 'rm -f "${result_file}"' EXIT

if ! terraform -chdir="${module_dir}" test \
  -filter=tests/legacy_to_default.tftest.hcl \
  -verbose \
  -json >"${result_file}"; then
  jq -r 'select(.type == "diagnostic") | .["@message"]' "${result_file}" >&2
  exit 1
fi

readonly seed_changes="$(jq -c -s '
  [
    .[]
    | select(
        .type == "test_plan" and
        .["@testrun"] == "plan_default_scope_registry_seed"
      )
    | .test_plan.resource_changes[]?
    | select(.change.actions != ["no-op"])
    | {
        address,
        module_address,
        mode,
        type,
        name,
        index,
        provider_name,
        previous_address,
        deposed,
        actions: .change.actions,
        importing: .change.importing
      }
  ]
' "${result_file}")"
readonly expected_seed_changes="[{\"address\":\"terraform_data.scope_registry[\\\"${scope_ref}\\\"]\",\"module_address\":null,\"mode\":\"managed\",\"type\":\"terraform_data\",\"name\":\"scope_registry\",\"index\":\"${scope_ref}\",\"provider_name\":\"terraform.io/builtin/terraform\",\"previous_address\":null,\"deposed\":null,\"actions\":[\"create\"],\"importing\":null}]"

if [[ "${seed_changes}" != "${expected_seed_changes}" ]]; then
  echo "Registry-seed plan contained changes other than the default-scope sentinel:" >&2
  jq . <<<"${seed_changes}" >&2
  exit 1
fi

readonly final_plan_count="$(jq -s '
  [
    .[]
    | select(
        .type == "test_plan" and
        .["@testrun"] == "plan_equivalent_default_scope"
      )
  ]
  | length
' "${result_file}")"
readonly final_changes="$(jq -c -s '
  [
    .[]
    | select(
        .type == "test_plan" and
        .["@testrun"] == "plan_equivalent_default_scope"
      )
    | .test_plan.resource_changes[]?
    | select(.change.actions != ["no-op"])
  ]
' "${result_file}")"

if [[ "${final_plan_count}" != "1" ]]; then
  echo "Expected one equivalent default-scope plan, found ${final_plan_count}." >&2
  exit 1
fi

readonly unexpected_final_changes="$(jq -c \
  --arg scope_ref "${scope_ref}" \
  --arg marker_address "terraform_data.scope_tag_policy[\"${scope_ref}\"]" \
  --arg configuration_address "terraform_data.scope_configuration[\"${scope_ref}\"]" \
  '
  [
    .[]
    | select(
        if .address == $marker_address then
          .change.actions != ["create"] or
          .mode != "managed" or
          .type != "terraform_data" or
          .name != "scope_tag_policy" or
          .index != $scope_ref or
          .provider_name != "terraform.io/builtin/terraform" or
          .change.after.input != {
            schema_version: 1,
            scope_ref: $scope_ref,
            policy_version: 0
          }
        elif .address == $configuration_address then
          .change.actions != ["create"] or
          .mode != "managed" or
          .type != "terraform_data" or
          .name != "scope_configuration" or
          .index != $scope_ref or
          .provider_name != "terraform.io/builtin/terraform" or
          .change.after.input != {
            schema_version: 2,
            scope_ref: $scope_ref,
            configuration: {
              default: true,
              cluster_name: "migration-eks",
              cluster_type: "eks",
              region: "ap-southeast-2",
              scope_tag_policy_version: 0,
              vpc: {
                mode: "dittocloud",
                name: "migration-vpc",
                cidr: "10.220.0.0/16",
                secondary_cidr: null,
                public_subnet_netmask: 24,
                private_subnet_netmask: 23,
                id: null,
                nat_gateway_name: null,
                nat_gateway_eip_allocation_ids: []
              }
            }
          }
        elif .address == "data.aws_iam_policy_document.karpenter_interruption[0]" then
          .change.actions != ["read"]
        elif .address == "aws_sqs_queue_policy.karpenter_interruption[0]" then
          .change.actions != ["update"] or
          (.change.before | del(.policy)) != .change.after or
          .change.after_unknown != {policy: true}
        else
          .change.actions != ["update"] or
          (.change.before | del(.tags, .tags_all)) != (.change.after | del(.tags, .tags_all)) or
          .change.after.tags["ditto.live/scope-ref"] != $scope_ref
        end
      )
    | {address, actions: .change.actions}
  ]
  ' <<<"${final_changes}")"

if [[ "${unexpected_final_changes}" != "[]" ]]; then
  echo "Equivalent default-scope plan contained a change other than the applied policy marker, applied configuration snapshot, and in-place scope identity tags:" >&2
  jq . <<<"${unexpected_final_changes}" >&2
  exit 1
fi

echo "Legacy-to-default migration verified: registry seed only, then one policy marker, one applied configuration snapshot, and in-place identity tags without replacement."
