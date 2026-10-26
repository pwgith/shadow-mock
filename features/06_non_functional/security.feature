@nfr @security @NFR-5.1 @NFR-5.3 @NFR-5.4
Feature: Security properties

  @P1
  Scenario Outline: Identifier entropy
    When I generate 100000 <kind>
    Then each has at least <bits> bits of randomness from a cryptographic source
    And there are no collisions

    Examples:
      | kind                  | bits |
      | session keys          | 128  |
      | URL-based mock ids    | 80   |
      | CI client secrets     | 256  |

  @P0
  Scenario: Request bodies above the hard limit are rejected
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxBodyBytes: 10485760 }
      """
    When I send a "POST" request to "/x" with an 11 MiB body
    Then the response status is 413
    And the engine's memory did not grow by more than 2 MiB for that request

  @P1
  Scenario: Session keys never appear in logs in clear text
    Given a key-based shadow mock "S1"
    When I send a request with key "${S1.key}" and the engine logs it
    Then the logs contain "smk_" followed by at most 4 visible characters of the key
    And the database stores only a SHA-256 hash of the key

  @saas @P5
  Scenario: The engine holds no Clerk secrets
    When I inspect the engine's environment and configuration
    Then it contains only public JWKS URLs and no Clerk secret keys

  @saas @P5
  Scenario: The management app's Clerk machine secret never leaves the server
    When I inspect every response and client bundle of the management app
    Then none contains the Clerk machine secret key

  @P3
  Scenario: Template rendering cannot reach the network or file system
    Given the global mapping:
      """json
      { "request": { "urlPath": "/t" }, "response": { "status": 200, "body": "{{file '/etc/passwd'}}{{http 'http://example.com'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/t"
    Then the response contains no file contents and no fetched content
