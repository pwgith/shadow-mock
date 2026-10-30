@saas @P5 @flow-F4 @flow-F5 @flow-F6 @flow-F8 @flow-F10 @flow-F19 @FR-11.2 @FR-11.3 @FR-11.10 @FR-11.11 @TA-2 @TA-3 @TA-5 @TA-6
Feature: The engine's part of the pipeline and debugging flows
  The platform's end-to-end flows (kaizentis-platform `docs/USE_CASES.md`) cross three applications. These
  scenarios are what the engine must do in each, so a flow cannot pass in the platform while the engine fails it.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  # F4: CI keeps the set up to date. The management app does the replace; the engine sees ordinary row changes.
  Scenario: F4 - A set replaced in one transaction is served once, whole
    Given the management app has written mappings "a" and "b" with metadata source "claims"
    When the management app commits one transaction that deletes "a", changes "b" and adds "c"
    Then the engine reloads once
    And "a" is gone, "b" has its new response and "c" is served
    And mappings with other sources are untouched

  Scenario: F4 - Validate is the same code the engine serves with
    Given a Tool app key "validate-only" for org "acme" with scopes "validate"
    When I call admin "POST" "/mappings/validate" with the key "validate-only" and a mapping with only one of url and urlPath
    Then the response status is 422
    And the response says which field is wrong
    When I call admin "POST" "/mappings/validate" with the key "validate-only" and a valid mapping
    Then the response status is 200
    And nothing was stored

  # F5: a pipeline runs tests against a per-test shadow mock.
  Scenario: F5 - A per-test shadow mock layered over the global layer
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    And the global layer has "GET /claims/1" returning "global"
    When the pipeline creates a key-based shadow mock "T1" with the key "ci-tests" and a time to live of 10 minutes
    And adds a mapping to "T1" for "GET /claims/1" returning "test"
    And the SUT sends "GET /acme/claims/1" carrying the key of "T1"
    Then the response body is "test"
    When the SUT sends "GET /acme/claims/1" without the key
    Then the response body is "global"
    When the pipeline deletes "T1" with the key "ci-tests"
    And the SUT sends "GET /acme/claims/1" carrying the key of "T1"
    Then the response body is "global"

  Scenario: F5 - A pipeline that crashes leaves nothing behind
    Given the pipeline created a shadow mock "T2" with a time to live of 10 minutes and then stopped
    When 11 minutes pass
    Then "T2" is gone
    And it no longer counts as an active session in the usage

  Scenario: F5 - The creator shows who made it
    Given the pipeline created "T3" with the key "ci-tests"
    And Erin created "T4" through the management app
    When a member lists the shadow mocks
    Then "T3" shows the creator "ci-tests" and "T4" shows "erin"

  # F6: Erin debugs a failing test.
  Scenario: F6 - An unmatched request is found, explained, and names the owning set
    Given the global layer has "claims-get" for "GET /claims/1" with metadata source "claims-api"
    And a key-based shadow mock "T1"
    When the SUT sends "GET /acme/claims/2" carrying the key of "T1"
    And a member searches the journal for unmatched requests of "T1"
    Then the entry is listed with its full request and the response the engine gave
    When the member opens its near-miss analysis
    Then "claims-get" is the closest candidate, with why it missed and its source "claims-api"

  # F8: a key leaks.
  Scenario: F8 - A leaked Tool app key stops within a minute and its use is on record
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    And the key was used from "203.0.113.4"
    When the management app revokes the key
    And 61 seconds pass
    Then calls with the key are refused with 401
    And the key's last used time and address "203.0.113.4" are in the key-usage table

  # F10: an open-source user moves to hosted.
  Scenario: F10 - A mapping loaded through the admin API into the global layer is refused when hosted
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    When I call admin "POST" "/mappings" with the key "ci-tests" and a valid mapping
    Then the response status is 403
    And the response JSON at "$.error" is "global_writes_via_management_app"

  Scenario: F10 - The same mapping works as a shadow mock
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    And a shadow mock "T1" made with the key
    When I add the mapping to "T1" through the admin API with the key
    Then the response status is 201

  Scenario: F10 - Paths after the handle are the ones the open-source server serves
    Given the global layer has "GET /payments/1" returning "ok"
    Then "GET /acme/payments/1" is served exactly as "GET /payments/1" is on a standalone server

  # F19: Erin keeps what she recorded.
  Scenario: F19 - A recording is made, listed and its mappings and files can be read for promotion
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    And a shadow mock "T1" made with the key
    And an upstream test server returning "payload" for "GET /pay/1"
    When the pipeline starts a recording into "T1" from the upstream with the key
    And the SUT sends "GET /acme/pay/1" carrying the key of "T1"
    And the pipeline stops the recording
    And a member reads the mappings and files of "T1"
    Then the recorded mapping and the file holding "payload" are returned
    And the shadow mock is unchanged and still expires on its time to live

  Scenario: F19 - A recording cannot be aimed at the platform's network
    Given a shadow mock "T1"
    When I start a recording into "T1" from "http://169.254.169.254"
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"
