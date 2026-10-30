@saas @P5 @FR-9.6 @FR-11.5 @TA-9
Feature: What a handle can be, and what the reserved prefix protects
  The handle is the first path segment of every hosted URL. The prefix `/__` belongs to the engine: admin, health,
  readiness and metrics. No handle can start with `_`, and no mapping can reach it.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"

  Scenario Outline: Handle format
    When the management app creates org "o1" with handle "<handle>"
    Then the write is <result>

    Examples:
      | handle                                   | result                       |
      | acme                                     | accepted                     |
      | acme-qa                                  | accepted                     |
      | a1b2c3                                   | accepted                     |
      | abc                                      | accepted                     |
      | ab                                       | refused as "handle_invalid"  |
      | 1acme                                    | refused as "handle_invalid"  |
      | -acme                                    | refused as "handle_invalid"  |
      | acme-                                    | refused as "handle_invalid"  |
      | Acme                                     | refused as "handle_invalid"  |
      | ac_me                                    | refused as "handle_invalid"  |
      | _acme                                    | refused as "handle_invalid"  |
      | __admin                                  | refused as "handle_invalid"  |
      | ac me                                    | refused as "handle_invalid"  |
      | acme/other                               | refused as "handle_invalid"  |
      | a-very-long-handle-that-goes-on-and-on-and-on | refused as "handle_invalid" |

  Scenario Outline: Reserved words are refused
    When the management app creates org "o1" with handle "<handle>"
    Then the write is refused as "handle_reserved"

    Examples:
      | handle |
      | www    |
      | api    |
      | admin  |
      | app    |
      | mail   |
      | static |

  Scenario: A handle equal to another org's handle or alias is refused
    Given the org "acme" with handle "acme" and the alias "acme-old"
    When the management app creates org "other" with handle "acme-old"
    Then the write is refused as "handle_taken"

  Scenario: Aliases are permanent
    Given the org "acme" with handle "acme-qa" and the alias "acme"
    When the SUT sends "GET /acme/hello"
    Then it reaches org "acme"
    And the alias cannot be removed through the engine's configuration tables

  Scenario Outline: The reserved prefix is the engine's
    Given the org "acme" with handle "acme" on plan "Team"
    And the management app writes mapping "sneaky" for "<mapping>" returning "mine"
    When I request "<request>"
    Then the response is not "mine"

    Examples:
      | mapping                  | request                      |
      | GET /__admin/sessions    | /acme/__admin/sessions       |
      | GET /__health            | /__health                    |
      | GET /__ready             | /__ready                     |
      | GET /__metrics           | /__metrics                   |

  Scenario: Health, readiness and metrics are at the root without an org
    Then "/__health" and "/__ready" answer 200 without a credential and without an org
    And "/__metrics" is not served without a credential (see metrics_and_health_exposure.feature)

  Scenario: An unknown handle gives nothing away
    When the SUT sends "GET /nobody/hello"
    Then the response status is 404
    And the response does not say whether other handles exist

  Scenario: A new org works as soon as its row is committed
    When the management app commits the org "newco" with handle "newco" and a mapping for "GET /hello"
    Then within 1 second "GET /newco/hello" is served
    And no restart, DNS change or certificate was needed
