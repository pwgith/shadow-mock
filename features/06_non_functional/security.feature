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

    @P5
    Examples: CI secrets are minted by the management app
      | kind                  | bits |
      | CI client secrets     | 256  |

  @P0
  Scenario: Request bodies above the hard limit are rejected
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxBodyBytes: 10485760 }
      """
    When I send a "POST" request to "/x" with an 11 MiB body
    Then the response status is 413
    And the response JSON at "$.error" is "body_too_large"

  # Needs a separate engine process to measure; the in-process harness shares memory with the client.
  @wip @P0
  Scenario: A rejected oversized body does not grow the engine's memory
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxBodyBytes: 10485760 }
      """
    When I send a "POST" request to "/x" with an 11 MiB body
    Then the engine's memory did not grow by more than 2 MiB for that request

  # Needs key redaction and hashed key storage (P4).
  @wip @P1
  Scenario: Session keys never appear in logs in clear text
    Given a key-based shadow mock "S1"
    When I send a request with key "${S1.key}" and the engine logs it
    Then the logs contain "smk_" followed by at most 4 visible characters of the key
    And the database stores only a SHA-256 hash of the key

  @saas @P5
  Scenario: The engine holds no signing keys or platform secrets
    When I inspect the engine's environment and configuration
    Then it contains only the trusted issuers' public key locations or pinned public keys
    And it contains no Clerk key, no launch-token key and no mock set key

  @P3
  Scenario: Template rendering cannot reach the network or file system
    Given the global mapping:
      """json
      { "request": { "urlPath": "/t" }, "response": { "status": 200, "body": "{{file '/etc/passwd'}}{{http 'http://example.com'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/t"
    Then the response contains no file contents and no fetched content
