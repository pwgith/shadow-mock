@saas @P5 @FR-11.1 @FR-11.9 @FR-11.16 @security @TA-4 @TA-11
Feature: Managed mode refuses settings that are unsafe on a shared host
  Managed mode is a multi-tenant service. It refuses to start with a setting that would let one org reach another,
  or the host's network, and it serves no pages of its own.

  Scenario Outline: Settings managed mode will not start with
    Given the configuration file contains:
      """yaml
      mode: managed
      <settings>
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "<mentions>"

    Examples:
      | settings                                       | mentions                    |
      | auth: { issuers: [] }                          | auth.issuers                |
      | admin: { globalWrites: allow }                 | admin.globalWrites          |
      | mappings: { unknownFields: reject }            | mappings.unknownFields      |
      | egress: { blockPrivate: false }                | egress.blockPrivate         |
      | proxy: { browser: { enabled: true } }          | proxy.browser               |
      | tls: { port: 8443 }                            | tls.port                    |
      | templating: { extensions: [jwt] }              | templating.jwt              |
      | console: { enabled: true }                     | console                     |
      | storage: { counters: { driver: memory } }      | storage.counters            |
      | storage: { global: { driver: fs } }            | storage.global              |
      | storage: { blobs: { driver: fs } }             | storage.blobs               |

  Scenario: Egress blocking cannot be turned off by an environment variable or a flag
    Given Shadow Mock is configured for managed mode
    And the environment variable "SHADOWMOCK_EGRESS_BLOCKPRIVATE" is "false"
    When Shadow Mock starts
    Then start-up fails with an error mentioning "egress.blockPrivate"

  Scenario: The JWT helper's keys must be given, because instances differ
    Given Shadow Mock is configured for managed mode
    And templating.extensions is "jwt" with the RS256 and HS256 keys set
    When Shadow Mock starts
    Then start-up succeeds

  Scenario Outline: A proxy or webhook target inside the platform is refused
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a mapping in "S1" with the response "<response>"
    When the SUT requests it
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"

    Examples:
      | response                                                          |
      | { "proxyBaseUrl": "http://127.0.0.1:6379" }                       |
      | { "proxyBaseUrl": "http://169.254.169.254" }                      |
      | { "proxyBaseUrl": "http://10.0.0.5" }                             |
      | { "proxyBaseUrl": "http://100.100.100.200" }                      |
      | { "proxyBaseUrl": "http://internal.example" } where it resolves to 10.0.0.9 |
      | { "proxyBaseUrl": "file:///etc/passwd" }                          |
      | { "proxyBaseUrl": "gopher://example.com" }                        |

  Scenario: A recording cannot be aimed at the platform's network
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    When I start a recording from "http://127.0.0.1:5432"
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"

  Scenario: The address that was checked is the address that is used
    Given a name that resolves to a public address when checked and to 10.0.0.9 when connected to
    When the engine proxies to it
    Then the request is refused

  Scenario: A body file is only ever the org's own
    Given the org "acme" with handle "acme" on plan "Team"
    And the org "globex" has a file "secret.json"
    And a mapping in "S1" whose response has "bodyFileName": "../globex/secret.json"
    When the SUT requests it
    Then the response status is 500
    And the response does not contain "globex"

  Scenario Outline: Managed mode serves no pages of its own
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    When I request "<path>" with a valid token
    Then the response status is 404

    Examples:
      | path                       |
      | /acme/__admin/docs         |
      | /acme/__admin/swagger-ui/  |
      | /acme/__admin/docs/swagger |
      | /acme/__console/           |
      | /__console/                |

  Scenario: The admin API, mock traffic, health and metrics still answer
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    Then "/__health" and "/__ready" answer 200 without an org
    And "/acme/__admin/sessions" answers 200 with a valid token
    And "/acme/hello" is mock traffic

  Scenario: Shutdown and save are disabled
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    When I call admin "POST" "/shutdown" with a token for "alice" in org "acme"
    Then the response status is 403
    When I call admin "POST" "/mappings/save" with a token for "alice" in org "acme"
    Then the response status is 403
