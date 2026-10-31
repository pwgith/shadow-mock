@managed @P5 @FR-11.6 @NFR-4.2 @TA-16
Feature: A rate limit for each org
  One runaway test must not slow other orgs on a shared instance, and the customer must be told what happened.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team" limited to 10 requests per second with a burst of 20
    And the org "globex" with handle "globex" on plan "Team"
    And a mapping "hello" in the global layer for "GET /hello" in both orgs

  Scenario: Requests over the limit get 429 and say which limit
    When the SUT sends 100 requests in one second to "GET /acme/hello"
    Then some responses are 429
    And each 429 has the error "rate_limited"
    And each 429 has a "Retry-After" header
    And each 429 has the header "X-Shadow-Mock-Limit" with the value "requests-per-second"

  Scenario: Other orgs are not affected
    Given the SUT is sending 100 requests a second to "GET /acme/hello"
    When the SUT sends "GET /globex/hello"
    Then the response status is 200

  Scenario: A 429 does not wait for the request body
    Given the SUT has used up the org's burst
    Then a request whose body never finishes still gets its 429 at once

  Scenario: Rejected requests are not billable and are counted apart
    When the SUT sends 100 requests in one second to "GET /acme/hello"
    Then the usage endpoint's "requests" is the number of responses that were not 429
    And the usage endpoint's "rateLimited" is the number of 429 responses
    And the stats show the rate-limited requests apart from served requests

  Scenario: The limit comes from the plan and follows a plan change
    When the management app raises the limit for org "acme" to 100 requests per second
    Then within 1 second 50 requests in one second are all served

  Scenario: Admin calls have a bucket of their own
    Given the SUT is sending 100 requests a second to "GET /acme/hello"
    When I call admin "GET" "/sessions" with a token for "alice" in org "acme"
    Then the response status is 200

  # Admin and mock traffic share one body limit (limits.maxBodyBytes); separate limits are not built.
  @saas
  Scenario: Admin and mock traffic have separate body size limits
    When I send a 2 MiB body to "POST /acme/hello"
    And I send a 2 MiB body to admin "POST" "/mappings/validate"
    Then each is judged against its own limit
