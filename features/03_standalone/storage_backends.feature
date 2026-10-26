@standalone @P0 @NFR-3.1 @NFR-3.2 @NFR-3.3
Feature: Pluggable storage backends in standalone mode
  The storage backend for each concern is a configuration choice. The request
  path never reads the store directly.

  Scenario Outline: Global mappings from each backend behave identically
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { global: { driver: <driver> } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200, "body": "<driver>" } }
      """
    When I send a "GET" request to "/b"
    Then the response body is "<driver>"

    Examples:
      | driver |
      | fs     |
      | memory |

  Scenario: Sessions are ephemeral by default and lost on restart
    Given Shadow Mock is running with default storage
    And a key-based shadow mock "S1"
    When Shadow Mock restarts
    Then admin "GET" "/sessions/${S1.id}" returns status 404

  Scenario: Sessions can optionally be persisted to the file system
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { sessions: { driver: fs } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/p" }, "response": { "status": 200 } }
      """
    When Shadow Mock restarts
    Then admin "GET" "/sessions/${S1.id}/mappings" lists 1 mappings

  Scenario: The request path does not touch the store
    Given Shadow Mock is running with a store that counts reads
    And the global mapping:
      """json
      { "request": { "urlPath": "/hot" }, "response": { "status": 200 } }
      """
    When I send 1000 "GET" requests to "/hot"
    Then the store recorded 0 reads during those requests

  Scenario: Blobs use a local directory in standalone mode
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { blobs: { driver: fs, dir: ./blobs } }
      """
    When I call admin "PUT" "/files/big.bin" with 5 MB of random bytes
    Then the directory "./blobs" contains the file's content
