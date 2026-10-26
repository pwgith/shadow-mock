@saas @management-app @P5 @FR-6.4 @FR-4.3 @FR-4.4
Feature: Sessions and runtime operations from the management app
  The app manages sessions and calls the engine's runtime API for call
  counters, dry-runs, validation, recordings and live stats.

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And I am signed in to the management app as "erin" in org "acme" with role "org:member"

  Scenario: Create a URL-based shadow mock from the UI
    When I create a URL-based shadow mock named "claims-api-ci" with a TTL of 30 days
    Then I see its base URL "https://shadowmock.kaizentis.xyz/acme/d/<id>"
    And I am told it counts as a concurrent session while active

  Scenario: List and filter sessions
    Given org "acme" has 3 key-based and 2 URL-based shadow mocks
    When I filter the sessions list by "URL-based"
    Then I see 2 sessions with their creator, expiry and request counts

  Scenario: Dry-run a mapping against a sample request
    Given a key-based shadow mock "S1" with a mapping for "POST /payments"
    When I dry-run "POST /payments" with body '{"amount":10}' against "S1"
    Then I see which layer and mapping would serve it, and the rendered response

  Scenario: View, set and reset call counters
    Given a key-based shadow mock "S1" whose stub for "POST /payments" has been called 2 times
    When I open the counters tab of "S1"
    Then I see the stub for "POST /payments" with count 2
    When I set its count to 9
    Then the engine reports count 9 for that stub in "S1"
    When I reset the counters of "S1"
    Then the engine reports count 0 for that stub in "S1"

  Scenario: Building a mapping with call-count responses
    When I add a rule "calls 1-2 → 503" and a rule "every 3 → 500" to a mapping in the editor
    Then the saved mapping contains the matching "callResponses" rules in that order
    And mappings containing WireMock scenario fields cannot be saved, with a hint to use call-count responses

  Scenario: Start and stop a recording into a session
    Given a URL-based shadow mock "U1"
    When I start recording into "U1" from "https://api.upstream.test"
    And traffic flows through "U1"
    And I stop the recording
    Then the recorded mappings appear in "U1"

  Scenario: Live stats
    When I open the stats dashboard
    Then I see requests per minute by layer, match rate, top unmatched paths, latency p50 and p99, detection stage hits, active sessions and IP allowlist rejections

  Scenario: Usage against plan limits
    When I open the usage page
    Then I see this period's mock requests and p95 concurrent sessions against the Team plan's included amounts
    And I see a warning banner when usage passes 80 %
