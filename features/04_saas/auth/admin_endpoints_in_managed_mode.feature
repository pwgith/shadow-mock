@managed @P5 @FR-6.3 @FR-6.5 @FR-11.16 @TA-3
Feature: Admin endpoints that managed mode disables or leaves to the management app
  The WireMock admin API has endpoints that stop the process, write the host's disk or change the global layer. On a
  shared host they are not available, or they are left to the management app (see
  01_wiremock_parity/admin/admin_misc.feature for what they do standalone).

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And I am "alice" in org "acme" with app role "admin"

  # System control is reserved to the management app, so authorisation refuses these before the endpoint's own
  # "not available" answer (a 404 not_available_in_saas) can be reached; see internal/auth/auth_test.go.
  Scenario Outline: Endpoints that are disabled or restricted in SaaS
    When I call admin "<method>" "<path>"
    Then the response status is <status>
    And the response JSON at "$.error" is "<error>"

    Examples:
      | method | path             | status | error                            |
      | POST   | /shutdown        | 403    | global_writes_via_management_app |
      | POST   | /mappings/save   | 403    | global_writes_via_management_app |
      | POST   | /reset           | 403    | global_writes_via_management_app |
      | PUT    | /files/x.json    | 403    | global_writes_via_management_app |
      | POST   | /settings        | 403    | global_writes_via_management_app |
