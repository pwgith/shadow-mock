@managed @P5 @FR-11.10 @FR-9.5a @FR-9.7 @TA-1
Feature: The org's global layer is whatever the management app wrote
  The management app groups an org's mappings into mock sets. The engine has no notion of a set: the global layer is
  the mappings stored for the org, matched by the usual priority rules. A `metadata.source` label says which set a
  mapping came from, and losing it loses only the label.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: Mappings from different sets are served together
    Given the management app writes mapping "claims-get" for "GET /claims/1" with metadata source "claims-api"
    And the management app writes mapping "orders-get" for "GET /orders/1" with metadata source "orders-api"
    Then "GET /acme/claims/1" and "GET /acme/orders/1" are both served

  Scenario: Priority decides when mappings from two sets match the same request
    Given the management app writes mapping "broad" for "GET /claims/.*" at priority 5 with metadata source "claims-api"
    And the management app writes mapping "narrow" for "GET /claims/123" at priority 1 with metadata source "claims-batch"
    When the SUT sends "GET /acme/claims/123"
    Then it is served by "narrow"
    When the SUT sends "GET /acme/claims/999"
    Then it is served by "broad"

  Scenario: The engine enforces no set access rules
    Given the management app writes mapping "m" with metadata source "claims-api"
    When the management app writes mapping "n" with metadata source "someone-elses-set" that buries "m"
    Then the engine stores and serves it
    # Overlap checks and access lists belong to the management app (MA-B.3, MA-B.11).

  Scenario: Removing one set's mappings leaves the others
    Given mappings "a1" and "a2" with metadata source "claims-api" and "b1" with metadata source "orders-api"
    When the management app removes the mappings whose metadata source is "claims-api"
    Then "a1" and "a2" are gone
    And "b1" is still served

  Scenario: A mapping without a source is served the same
    Given the management app writes mapping "plain" for "GET /plain" with no metadata
    Then "GET /acme/plain" is served
    And its journal entry has no "source"

  Scenario: The source is shown for the mapping that served and for near misses
    Given the management app writes mapping "claims-get" for "GET /claims/1" with metadata source "claims-api"
    When the SUT sends "GET /acme/claims/2"
    And I open the unmatched request's near-miss analysis
    Then the candidate "claims-get" shows source "claims-api"

  Scenario: Other metadata is kept and returned unchanged
    Given the management app writes mapping "m" with metadata:
      | key    | value               |
      | source | claims-api          |
      | owner  | {"team":"platform"} |
    When I read the mapping "m" through the admin API
    Then the metadata is returned exactly as written

  Scenario: A shadow mock still overrides the global layer, whatever its source
    Given the management app writes mapping "claims-get" for "GET /claims/1" with metadata source "claims-api"
    And a key-based shadow mock "S1" with its own mapping for "GET /claims/1"
    When the SUT sends "GET /acme/claims/1" carrying the key of "S1"
    Then it is served by the shadow mock's mapping
