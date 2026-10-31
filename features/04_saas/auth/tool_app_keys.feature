@managed @P5 @FR-9.3a @FR-9.3b @FR-9.5 @FR-11.3 @security @TA-2 @TA-5
Feature: Tool app keys for pipelines
  A pipeline creates and deletes shadow mocks with a Tool app key, `smt_<id>_<secret>`. The management app creates
  the key and stores its hash in `engine.api_keys`; the engine only reads the table. A mock set key (`smk_`) belongs
  to the management app's upload API and is never accepted here.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"

  Scenario: A key with the sessions scope manages shadow mocks
    When I call admin "POST" "/sessions/key-based" with the key "ci-tests" and body "{}"
    Then the response status is 201
    When I call admin "GET" "/sessions" with the key "ci-tests"
    Then the response status is 200

  Scenario: The key's label is the creator
    Given I create a shadow mock "S1" with the key "ci-tests"
    When I call admin "GET" "/sessions/${S1.id}" as a member
    Then the response JSON at "$.createdBy" is "ci-tests"
    And the response JSON at "$.createdByKey" is "ci-tests"

  Scenario Outline: Scopes limit what a key can do
    Given a Tool app key "k" for org "acme" with scopes "<scopes>"
    When I call admin "<method>" "<path>" with the key "k" and body "<body>"
    Then the response status is <status>

    Examples:
      | scopes            | method | path                    | body | status |
      | read              | GET    | /sessions               |      | 200    |
      | read              | POST   | /sessions/key-based     | {}   | 403    |
      | sessions,read     | POST   | /sessions/key-based     | {}   | 201    |
      | validate          | POST   | /mappings/validate      | a minimal mapping | 200 |
      | validate          | POST   | /sessions/key-based     | {}   | 403    |
      | sessions,read     | POST   | /mappings               | {}   | 403    |
      | sessions,read     | POST   | /settings               | {}   | 403    |
      | sessions,read     | DELETE | /requests               |      | 403    |
      | sessions,read     | POST   | /mappings/validate      | {}   | 403    |

  Scenario: A key can never write the global layer, settings or the journal
    When I call admin "POST" "/mappings" with the key "ci-tests" and body "{}"
    Then the response status is 403
    And the response JSON at "$.error" is "global_writes_via_management_app"

  Scenario: A key for one org cannot reach another
    Given the org "globex" with handle "globex" on plan "Team"
    When I call admin "GET" "/sessions" on org "globex" with the key "ci-tests"
    Then the response status is 403
    And the response JSON at "$.error" is "org_mismatch"
    And the response contains no mappings, sessions or journal entries

  Scenario Outline: A key that is not valid gives no hint why
    Given the key is <state>
    When I call admin "GET" "/sessions" with the key
    Then the response status is 401
    And the response JSON at "$.error" is "invalid_token"

    Examples:
      | state                         |
      | made up                       |
      | the right id and a wrong secret |
      | expired                       |
      | revoked longer than a minute ago |

  Scenario: A revoked key stops within a minute
    Given the key "ci-tests" has been used
    When the management app revokes the key
    And 61 seconds pass
    And I call admin "GET" "/sessions" with the key "ci-tests"
    Then the response status is 401

  Scenario: A mock set key is never accepted
    Given a mock set key "smk_ABCDEFGHIJKLMNOP_secret"
    When I call admin "GET" "/sessions" with that key
    Then the response status is 401
    And the key is not looked up in the database

  Scenario: A credential that is neither a token nor a key is refused without a hint
    When I call admin "GET" "/sessions" with the credential "abc.def"
    Then the response status is 401
    And the response JSON at "$.error" is "invalid_token"

  Scenario: The secret is compared in constant time and never logged
    When I call admin "GET" "/sessions" with a key that has the wrong secret
    Then the response is the same as for a key that does not exist
    And no log line contains the secret

  Scenario: Last use is recorded without causing a reload
    When I call admin "GET" "/sessions" with the key "ci-tests" from "203.0.113.4"
    And one minute passes
    Then the key's last used time and address "203.0.113.4" are written to the key-usage table
    And no configuration reload happens

  Scenario: The org allowlist applies to keys when it applies to admin calls
    Given the org's IP allowlist is "198.51.100.0/24" and applies to the admin API
    When I call admin "GET" "/sessions" with the key "ci-tests" from "203.0.113.4"
    Then the response status is 403
    And the response JSON at "$.error" is "ip_not_allowed"
    But a call with a token from the trusted issuer from "203.0.113.4" is allowed
