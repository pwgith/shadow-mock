# Requirement coverage

Generated from the `@FR-*` and `@NFR-*` tags in the feature files. Regenerate it when tags change. Every requirement in [REQUIREMENTS.md](../docs/REQUIREMENTS.md) has at least one feature.

| Requirement | Feature files |
|---|---|
| FR-1.1 | `01_wiremock_parity/mappings/mapping_file_loading.feature` |
| FR-1.2 | `01_wiremock_parity/mappings/mapping_file_loading.feature`, `02_shadow_mocks/common/session_files.feature` |
| FR-1.3 | `01_wiremock_parity/mappings/mapping_file_loading.feature`, `07_stateful_responses/scenarios_not_supported.feature` |
| FR-1.4 | `01_wiremock_parity/mappings/mapping_file_loading.feature` |
| FR-1.5 | `01_wiremock_parity/mappings/mapping_file_loading.feature` |
| FR-2.1 | `01_wiremock_parity/request_matching/url_matching.feature` |
| FR-2.2 | `01_wiremock_parity/request_matching/method_matching.feature` |
| FR-2.3 | `01_wiremock_parity/request_matching/auth_host_port_scheme.feature`, `01_wiremock_parity/request_matching/headers_query_cookies.feature` |
| FR-2.4 | `01_wiremock_parity/request_matching/value_matchers.feature` |
| FR-2.5 | `01_wiremock_parity/request_matching/body_json.feature`, `01_wiremock_parity/request_matching/body_other.feature`, `01_wiremock_parity/request_matching/body_xml.feature` |
| FR-2.6 | `01_wiremock_parity/mappings/priority.feature` |
| FR-2.7 | `01_wiremock_parity/request_matching/custom_matchers.feature` |
| FR-3.1 | `01_wiremock_parity/responses/response_definition.feature` |
| FR-3.2 | `01_wiremock_parity/responses/delays.feature` |
| FR-3.3 | `01_wiremock_parity/responses/faults.feature` |
| FR-3.4 | `01_wiremock_parity/responses/response_templating.feature`, `01_wiremock_parity/responses/template_helpers.feature`, `07_stateful_responses/call_count_responses.feature` |
| FR-3.5 | `01_wiremock_parity/responses/proxying.feature` |
| FR-3.5a | `01_wiremock_parity/responses/gzip.feature` |
| FR-3.6 | `01_wiremock_parity/responses/unmatched_requests.feature` |
| FR-4.1 | `07_stateful_responses/call_count_responses.feature` |
| FR-4.2 | `02_shadow_mocks/key_based/call_count_isolation.feature`, `07_stateful_responses/call_count_responses.feature` |
| FR-4.3 | `05_management_app/sessions_and_runtime.feature`, `07_stateful_responses/call_count_responses.feature` |
| FR-4.4 | `05_management_app/sessions_and_runtime.feature`, `07_stateful_responses/scenarios_not_supported.feature` |
| FR-5.1 | `01_wiremock_parity/journal/request_journal.feature` |
| FR-5.2 | `01_wiremock_parity/journal/verification.feature` |
| FR-5.3 | `01_wiremock_parity/journal/near_misses.feature`, `05_management_app/journal_browsing.feature` |
| FR-5.4 | `01_wiremock_parity/journal/request_journal.feature` |
| FR-5.5 | `01_wiremock_parity/journal/verification.feature`, `04_saas/journal/hot_journal.feature` |
| FR-5.6 | `04_saas/journal/warm_journal.feature`, `05_management_app/journal_browsing.feature`, `05_management_app/org_settings.feature` |
| FR-6.1 | `01_wiremock_parity/record_playback/recording.feature`, `01_wiremock_parity/record_playback/snapshot.feature`, `02_shadow_mocks/common/recording_into_sessions.feature` |
| FR-6.2 | `01_wiremock_parity/webhooks/webhooks.feature` |
| FR-6.3 | `01_wiremock_parity/admin/admin_misc.feature`, `01_wiremock_parity/mappings/mapping_admin_api.feature` |
| FR-6.4 | `01_wiremock_parity/mappings/mapping_admin_api.feature`, `01_wiremock_parity/admin/admin_diagnostics.feature`, `05_management_app/sessions_and_runtime.feature` |
| FR-6.5 | `01_wiremock_parity/admin/admin_misc.feature`, `03_standalone/server_settings.feature`, `04_saas/auth/global_writes_restriction.feature` |
| FR-7.1 | `02_shadow_mocks/key_based/lifecycle.feature` |
| FR-7.2 | `02_shadow_mocks/key_based/lifecycle.feature`, `02_shadow_mocks/url_based/lifecycle.feature` |
| FR-7.3 | `02_shadow_mocks/key_based/session_admin_behaviour.feature`, `02_shadow_mocks/key_based/session_scoped_admin.feature` |
| FR-7.4 | `02_shadow_mocks/key_based/detection_designated_fields.feature`, `02_shadow_mocks/key_based/detection_scanning.feature` |
| FR-7.5 | `02_shadow_mocks/key_based/layered_resolution.feature` |
| FR-7.6 | `02_shadow_mocks/key_based/call_count_isolation.feature`, `02_shadow_mocks/url_based/resolution.feature` |
| FR-7.7 | `02_shadow_mocks/key_based/override_and_mask.feature` |
| FR-7.8 | `02_shadow_mocks/key_based/override_and_mask.feature` |
| FR-7.9 | `02_shadow_mocks/key_based/match_tokens.feature` |
| FR-7.10 | `02_shadow_mocks/key_based/ambiguity.feature` |
| FR-7.11 | `02_shadow_mocks/key_based/journal_isolation.feature` |
| FR-7.12 | `02_shadow_mocks/key_based/layered_resolution.feature` |
| FR-8.1 | `02_shadow_mocks/url_based/lifecycle.feature` |
| FR-8.2 | `02_shadow_mocks/url_based/routing.feature`, `03_standalone/standalone_mode.feature` |
| FR-8.3 | `02_shadow_mocks/url_based/resolution.feature` |
| FR-8.4 | `02_shadow_mocks/url_based/resolution.feature` |
| FR-8.5 | `02_shadow_mocks/url_based/nesting.feature` |
| FR-8.6 | `02_shadow_mocks/url_based/routing.feature` |
| FR-8.7 | `02_shadow_mocks/common/seeding.feature` |
| FR-9.1 | `03_standalone/standalone_mode.feature` |
| FR-9.2 | `03_standalone/standalone_mode.feature`, `03_standalone/admin_auth.feature` |
| FR-9.3 | `04_saas/auth/jwt_authentication.feature` |
| FR-9.3a | `04_saas/auth/jwt_authentication.feature` |
| FR-9.3b | `04_saas/auth/ci_tokens.feature`, `05_management_app/ci_keys.feature`, `05_management_app/ci_token_exchange.feature` |
| FR-9.4 | `04_saas/auth/roles_and_permissions.feature`, `04_saas/tenancy/tenant_isolation.feature` |
| FR-9.5 | `04_saas/auth/roles_and_permissions.feature` |
| FR-9.5a | `04_saas/auth/global_writes_restriction.feature`, `05_management_app/global_mappings.feature` |
| FR-9.6 | `04_saas/tenancy/handle_aliases.feature`, `04_saas/tenancy/org_handle_routing.feature`, `05_management_app/org_settings.feature` |
| FR-9.6a | `04_saas/auth/org_match.feature` |
| FR-9.6b | `03_standalone/listeners_and_allowlist.feature`, `04_saas/network/client_ip.feature`, `04_saas/network/ip_allowlist.feature`, `05_management_app/org_settings.feature` |
| FR-9.7 | `04_saas/tenancy/org_handle_routing.feature`, `04_saas/tenancy/tenant_isolation.feature` |
| FR-9.8 | `04_saas/data_sync/change_propagation.feature`, `05_management_app/global_mappings.feature` |
| FR-10.1 | `01_wiremock_parity/request_matching/jsonpath_dialect.feature` |
| FR-10.2 | `01_wiremock_parity/request_matching/jsonpath_dialect.feature` |
| FR-10.3 | `01_wiremock_parity/responses/template_blocks.feature` |
| FR-10.4 to FR-10.16 | planned, see [PARITY_PLAN.md](../docs/PARITY_PLAN.md); each gets a feature as its workstream is built |
| NFR-1.1 | `06_non_functional/compatibility.feature` |
| NFR-1.2 | `02_shadow_mocks/key_based/session_scoped_admin.feature`, `06_non_functional/compatibility.feature` |
| NFR-2.1 | `06_non_functional/performance.feature` |
| NFR-2.2 | `02_shadow_mocks/key_based/detection_scanning.feature`, `06_non_functional/performance.feature` |
| NFR-2.3 | `02_shadow_mocks/key_based/detection_scanning.feature` |
| NFR-2.4 | `04_saas/data_sync/change_propagation.feature`, `06_non_functional/performance.feature` |
| NFR-2.5 | `06_non_functional/performance.feature` |
| NFR-3.1 | `03_standalone/storage_backends.feature`, `04_saas/journal/object_storage.feature` |
| NFR-3.2 | `03_standalone/storage_backends.feature` |
| NFR-3.3 | `03_standalone/storage_backends.feature`, `04_saas/data_sync/change_propagation.feature` |
| NFR-4.1 | `04_saas/data_sync/change_propagation.feature` |
| NFR-4.2 | `04_saas/plans_and_limits/rate_and_session_limits.feature` |
| NFR-4.3 | `04_saas/plans_and_limits/usage_metering.feature`, `05_management_app/billing_and_plans.feature` |
| NFR-4.4 | `04_saas/journal/hot_journal.feature` |
| NFR-5.1 | `02_shadow_mocks/key_based/lifecycle.feature`, `02_shadow_mocks/url_based/lifecycle.feature`, `06_non_functional/security.feature` |
| NFR-5.2 | `03_standalone/listeners_and_allowlist.feature` |
| NFR-5.3 | `01_wiremock_parity/responses/proxying.feature`, `01_wiremock_parity/responses/response_templating.feature`, `04_saas/operations/egress_policy.feature`, `06_non_functional/security.feature` |
| NFR-5.4 | `06_non_functional/security.feature`, `03_standalone/server_settings.feature` |
| NFR-6.1 | `03_standalone/configuration.feature` |
| NFR-6.2 | `03_standalone/configuration.feature` |
| NFR-6.3 | `03_standalone/metrics.feature`, `03_standalone/tracing.feature` |
| NFR-6.4 | `04_saas/operations/health_and_shutdown.feature` |
