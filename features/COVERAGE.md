# Requirement coverage

Generated from the `@FR-*` and `@NFR-*` tags in the feature files. Regenerate it when tags change. Every requirement in [REQUIREMENTS.md](../docs/REQUIREMENTS.md) has at least one feature.

Managed-mode features tagged `@managed` run in the acceptance suite against the engine on one instance with the stores in memory (`test/acceptance/managed*.go`): the trusted issuer, Tool app keys, org match, roles, global writes, the metadata scope, org state, handles and aliases, handle routing, tenant isolation, the global layer and the org rate limit. Features still tagged `@saas` are not run yet; a comment above each says what is missing. The in-process parts of FR-9.3 to FR-9.6b and FR-11.x are tested in Go instead, in `internal/app/managed_test.go` and the `internal/auth`, `internal/tenant`, `internal/usage` and `internal/audit` packages. The Postgres, Redis and S3 drivers are built and tested in Go: the object-store drivers share one contract suite (`internal/blob/blobtest`) and are tested against an S3 fake and, for failures, against Shadow Mock itself as a failing store; the counters share a suite with the in-memory ones (`internal/countertest`); the Postgres tests and the full managed-mode test on Postgres, Redis and object storage run in CI against a Postgres service container. Not built yet: Redis for shadow-mock sessions and the hot journal, the warm journal, and persisting usage.

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
| FR-9.3 | `04_saas/auth/trusted_issuer.feature` |
| FR-9.3a | `04_saas/auth/trusted_issuer.feature`, `04_saas/auth/tool_app_keys.feature` |
| FR-9.3b | `04_saas/auth/tool_app_keys.feature` |
| FR-9.4 | `04_saas/auth/roles_and_permissions.feature`, `04_saas/tenancy/tenant_isolation.feature` |
| FR-9.5 | `04_saas/auth/roles_and_permissions.feature` |
| FR-9.5a | `04_saas/auth/global_writes_restriction.feature`, `05_management_app/global_mappings.feature` |
| FR-9.6 | `04_saas/tenancy/handle_aliases.feature`, `04_saas/tenancy/org_handle_routing.feature`, `05_management_app/org_settings.feature` |
| FR-9.6a | `04_saas/auth/org_match.feature` |
| FR-9.6b | `03_standalone/listeners_and_allowlist.feature`, `04_saas/network/client_ip.feature`, `04_saas/network/ip_allowlist.feature`, `05_management_app/org_settings.feature` |
| FR-9.7 | `04_saas/tenancy/org_handle_routing.feature`, `04_saas/tenancy/tenant_isolation.feature`, `04_saas/tenancy/org_state.feature` |
| FR-9.8 | `04_saas/data_sync/change_propagation.feature`, `04_saas/data_sync/change_listener.feature`, `05_management_app/global_mappings.feature` |
| FR-10.1 | `01_wiremock_parity/request_matching/jsonpath_dialect.feature` (filters, functions and how their results print) |
| FR-10.2 | `01_wiremock_parity/request_matching/jsonpath_dialect.feature` |
| FR-10.3 | `01_wiremock_parity/responses/template_blocks.feature` |
| FR-10.4, FR-10.5 | `01_wiremock_parity/responses/template_helper_parity.feature` |
| FR-10.6 | `01_wiremock_parity/responses/template_request_model.feature` |
| FR-10.7 | `01_wiremock_parity/admin/admin_journal_queries.feature` |
| FR-10.8 | `01_wiremock_parity/admin/admin_wiremock_endpoints.feature` |
| FR-10.9, FR-1.4 | `01_wiremock_parity/mappings/strict_mappings.feature` |
| FR-10.10 | `01_wiremock_parity/responses/response_framing.feature`, `01_wiremock_parity/responses/gzip.feature` |
| FR-10.11 | `01_wiremock_parity/responses/server_behaviours.feature`, `01_wiremock_parity/responses/proxying.feature` |
| FR-10.14a, FR-10.14e | `03_standalone/tls.feature` and `01_wiremock_parity/request_matching/auth_host_port_scheme.feature` (scheme and port); `--disable-http` and the separate admin listener in `cmd/shadowmock/tls_test.go` |
| FR-10.14b | `03_standalone/tls.feature` (PEM, PKCS12 and JKS, wrong passwords, missing files) and `internal/tlsconf` |
| FR-10.14c | `03_standalone/tls.feature` (PKCS12 and JKS truststores, required and not) and `cmd/shadowmock/tls_test.go` |
| FR-10.14d | `03_standalone/tls.feature` (HTTP/2 over TLS, the journal's protocol, switching it off); h2c in `cmd/shadowmock/tls_test.go` |
| FR-10.13 | `03_standalone/wiremock_flag_settings.feature` (every flag and the setting it makes, the Java-server flags, and the flags that need another), `03_standalone/wiremock_flags.feature` (what the settings do), and `internal/config/wmflags_test.go`, which checks every flag in WireMock's `--help` |
| FR-10.12 | `01_wiremock_parity/record_playback/snapshot.feature` (and `test/conformance/recording_differential_test.go`) |
| FR-10.15a to FR-10.15e | `03_standalone/browser_proxy.feature` (stubs and pass-through over HTTP and through a tunnel, a refused tunnel, the stored authority, trusted and untrusted origins, the journal, start-up checks), with the certificate chain checked end to end in `cmd/shadowmock/browserproxy_test.go` and the authority in `internal/browserproxy` |
| FR-11.1, FR-11.9, FR-11.16 | `04_saas/operations/managed_mode_guards.feature`, `04_saas/operations/egress_policy.feature` |
| FR-11.2 | `04_saas/auth/trusted_issuer.feature` |
| FR-11.3 | `04_saas/auth/tool_app_keys.feature` |
| FR-11.4 | `04_saas/auth/metadata_scope.feature` |
| FR-11.5 | `04_saas/tenancy/org_state.feature` |
| FR-11.6 | `04_saas/plans_and_limits/org_rate_limit.feature` |
| FR-11.7 | `04_saas/operations/shared_runtime_state.feature` |
| FR-11.8 | `04_saas/data_sync/change_listener.feature` |
| FR-11.10, FR-11.11 | `05_management_app/admin_api_for_management_app.feature` |
| FR-11.12 | `04_saas/plans_and_limits/usage_metering.feature` |
| FR-11.13, FR-11.14 | `04_saas/operations/file_storage_and_degraded_operation.feature` |
| FR-11.15 | `04_saas/operations/audit_purge_and_anonymise.feature` |
| FR-11.17 | `03_standalone/console_and_cli.feature` (not built yet, `@wip`) |
| FR-10.16a to FR-10.16d | `01_wiremock_parity/responses/extensions.feature` (jwt claims, signatures and key set; every kind of faker expression; graphql equality; start-up checks), with the helpers in `internal/respond/template/extensions_test.go` and the matcher in `internal/graphql` |
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
| NFR-4.1 | `04_saas/data_sync/change_propagation.feature`, `04_saas/operations/shared_runtime_state.feature` |
| NFR-4.2 | `04_saas/plans_and_limits/rate_and_session_limits.feature`, `04_saas/plans_and_limits/org_rate_limit.feature` |
| NFR-4.3 | `04_saas/plans_and_limits/usage_metering.feature`, `05_management_app/billing_and_plans.feature` |
| NFR-4.4 | `04_saas/journal/hot_journal.feature` |
| NFR-5.1 | `02_shadow_mocks/key_based/lifecycle.feature`, `02_shadow_mocks/url_based/lifecycle.feature`, `06_non_functional/security.feature` |
| NFR-5.2 | `03_standalone/listeners_and_allowlist.feature` |
| NFR-5.3 | `01_wiremock_parity/responses/proxying.feature`, `01_wiremock_parity/responses/response_templating.feature`, `04_saas/operations/egress_policy.feature`, `06_non_functional/security.feature` |
| NFR-5.4 | `06_non_functional/security.feature`, `03_standalone/server_settings.feature` |
| NFR-5.5 | `03_standalone/browser_proxy.feature`, `04_saas/operations/managed_mode_guards.feature` |
| NFR-5.6 | `01_wiremock_parity/responses/extensions.feature`, `04_saas/operations/managed_mode_guards.feature` |
| NFR-5.7 | `04_saas/auth/trusted_issuer.feature`, `04_saas/auth/tool_app_keys.feature`, `04_saas/tenancy/tenant_isolation.feature`, `04_saas/auth/metadata_scope.feature` |
| NFR-6.1 | `03_standalone/configuration.feature` |
| NFR-6.2 | `03_standalone/configuration.feature` |
| NFR-6.3 | `03_standalone/metrics.feature`, `03_standalone/tracing.feature` |
| NFR-6.4 | `04_saas/operations/health_and_shutdown.feature` |

## Platform requirements on the engine

The Kaizentis platform's Shadow Mock requirements (`kaizentis-platform/apps/shadow-mock/docs/REQUIREMENTS.md`) list sixteen requirements on the engine, which it calls the Tool app. REQUIREMENTS.md §3.10 answers each as an FR-11 requirement. Features carry the `@TA-n` tag of the platform requirement, and the scenarios that follow its end-to-end flows (`docs/USE_CASES.md`) are in `08_platform_flows`, tagged `@flow-Fn`.

| Platform requirement | Engine requirement | Feature files |
|---|---|---|
| TA-1 Global layer is the merge of the org's mock sets | FR-11.10 | `04_saas/tenancy/global_layer_merge.feature` |
| TA-2 Tool app keys | FR-11.3 | `04_saas/auth/tool_app_keys.feature` |
| TA-3 Complete admin API, global layer read-only when hosted | FR-11.11, FR-9.5a | `05_management_app/admin_api_for_management_app.feature`, `04_saas/auth/global_writes_restriction.feature` |
| TA-4 No UI in managed mode | FR-11.16 | `04_saas/operations/managed_mode_guards.feature` |
| TA-5 Creator shown as the key's label | FR-11.3 | `04_saas/auth/tool_app_keys.feature` |
| TA-6 The owning set in the journal and near misses | FR-11.10 | `04_saas/tenancy/global_layer_merge.feature`, `05_management_app/admin_api_for_management_app.feature` |
| TA-7 Embedded console, TA-8 command line | FR-11.17 | `03_standalone/console_and_cli.feature` (`@wip`) |
| TA-9 Trusted issuer, org match, admin address | FR-9.3, FR-9.6, FR-9.6a, FR-11.2 | `04_saas/auth/trusted_issuer.feature`, `04_saas/auth/org_match.feature`, `04_saas/tenancy/handle_rules.feature` |
| TA-10 Any instance answers any admin call | FR-11.7 | `04_saas/operations/shared_runtime_state.feature` |
| TA-11 Egress policy | FR-11.9 | `04_saas/operations/egress_policy.feature`, `04_saas/operations/managed_mode_guards.feature` |
| TA-12 A configuration change is never missed | FR-11.8 | `04_saas/data_sync/change_listener.feature`, `04_saas/data_sync/database_contract.feature` |
| TA-13 The `metadata` scope | FR-11.4 | `04_saas/auth/metadata_scope.feature` |
| TA-14 A deleted org stays deleted | FR-11.5 | `04_saas/tenancy/org_state.feature` |
| TA-15 Real client address, protected metrics | FR-9.6b | `04_saas/network/client_ip.feature`, `04_saas/network/ip_allowlist.feature`, `04_saas/network/metrics_and_health_exposure.feature` |
| TA-16 Per-org rate limit | FR-11.6 | `04_saas/plans_and_limits/org_rate_limit.feature` |
| Usage endpoint, hourly buckets | FR-11.12 | `04_saas/plans_and_limits/usage_metering.feature` |
| Object store, degraded operation | FR-11.13, FR-11.14 | `04_saas/operations/file_storage_and_degraded_operation.feature` |
| Audit trail, org purge, person anonymise | FR-11.15 | `04_saas/operations/audit_purge_and_anonymise.feature` |

| Platform flow | Feature |
|---|---|
| F1, F7, F11, F12, F13, F17, F30, F32 | `08_platform_flows/people_support_and_deletion_flows.feature` |
| F4, F5, F6, F8, F10, F19 | `08_platform_flows/pipelines_and_debugging.feature` |
| F9, F15, F16, F18, F22, F24 | `08_platform_flows/plans_and_limits_flows.feature` |
| F2, F3, F14, F20, F21, F23, F25 to F29, F31, F33 | Not the engine's: the SaaS app and the management app own them |
