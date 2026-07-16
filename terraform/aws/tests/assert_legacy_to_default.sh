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
    | {address, actions: .change.actions}
  ]
' "${result_file}")"

if [[ "${final_plan_count}" != "1" ]]; then
  echo "Expected one equivalent default-scope plan, found ${final_plan_count}." >&2
  exit 1
fi

if [[ "${final_changes}" != "[]" ]]; then
  echo "Equivalent default-scope plan changed existing infrastructure:" >&2
  jq . <<<"${final_changes}" >&2
  exit 1
fi

echo "Legacy-to-default migration verified: registry seed only, then zero infrastructure changes."
